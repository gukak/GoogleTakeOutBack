// Package updater implements the self-update command. It downloads the release
// archive from GitHub, validates binary checksums, stages the new files and
// asks the user to restart. The launcher scripts then apply the staged update
// before starting the application.
package updater

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gukak/GoogleTakeOutBack/internal/app"
	"github.com/gukak/GoogleTakeOutBack/internal/progressbar"
)

// ErrRestartRequired is returned when the update has been staged successfully
// and the application must be restarted to complete the replacement.
var ErrRestartRequired = errors.New("restart required to complete the update")

// Update checks GitHub Releases for a newer version and stages it.
// If --version vX.Y.Z is provided, that exact version is staged instead.
func Update(env *app.Env, args []string) error {
	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var target string
	for i := 0; i < len(args); i++ {
		if args[i] == "--version" && i+1 < len(args) {
			target = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--version=") {
			target = strings.TrimPrefix(args[i], "--version=")
			continue
		}
		if target == "" && looksLikeVersion(args[i]) {
			target = args[i]
		}
	}

	if target != "" {
		if !strings.HasPrefix(target, "v") {
			target = "v" + target
		}
		if target == app.Version {
			env.Summary("Already on %s", app.Version)
			return nil
		}
		return installVersion(env, client, target)
	}

	latest, err := resolveLatestRelease(client)
	if err != nil {
		env.Summary("Update check failed: %v", err)
		return nil
	}
	if latest == "" {
		env.Summary("Could not determine latest release")
		return nil
	}
	if compareVersion(latest, app.Version) <= 0 {
		env.Summary("Already up to date (%s)", app.Version)
		return nil
	}
	return installVersion(env, client, latest)
}

// resolveLatestRelease returns the tag name of the latest GitHub release.
func resolveLatestRelease(client *http.Client) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", app.OwnerRepo)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "takeoutback/"+app.Version)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("cannot parse release metadata: %w", err)
	}
	return release.TagName, nil
}

func installVersion(env *app.Env, client *http.Client, version string) error {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	archiveName := "takeoutback-" + version + ".zip"
	archiveURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", app.OwnerRepo, version, archiveName)

	// GitHub asset downloads redirect to a CDN. Use a client that follows
	// redirects for the actual file downloads. The timeout is long because
	// users on slow links need time to download the multi-megabyte archive.
	downloadClient := &http.Client{Timeout: 15 * time.Minute}

	env.Summary("Downloading %s...", version)

	tmpDir := filepath.Join(filepath.Dir(env.BinaryPath()), ".tmp", "update-"+version)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, archiveName)
	if err := downloadFile(downloadClient, archiveURL, archivePath, version); err != nil {
		return err
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := extractZip(archivePath, extractDir); err != nil {
		return fmt.Errorf("cannot extract archive: %w", err)
	}

	if err := verifyBinaries(extractDir); err != nil {
		return err
	}

	// The launcher scripts cannot be replaced while they are running. They are
	// expected to stay identical across releases so that the launcher itself can
	// apply staged updates. If they changed, the user must update manually.
	for _, launcher := range []string{"takeOutBack.sh", "takeOutBack.bat"} {
		local := filepath.Join(env.Root, launcher)
		remote := filepath.Join(extractDir, launcher)
		if _, err := os.Stat(local); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		changed, err := filesDiffer(local, remote)
		if err != nil {
			return err
		}
		if changed {
			return fmt.Errorf("%s has changed in %s. The launcher script is responsible for applying updates and must stay identical across releases. Please update manually", launcher, version)
		}
	}

	// Merge user configuration with the new defaults so custom settings are kept.
	if err := mergeSettings(filepath.Join(extractDir, "settings.json"), filepath.Join(env.ConfigDir, app.SettingsName)); err != nil {
		return err
	}
	if err := mergePolicy(filepath.Join(extractDir, "policy.json"), filepath.Join(env.ConfigDir, app.PolicyName)); err != nil {
		return err
	}

	// Stage all files for the launcher to apply on the next start.
	stageDir := filepath.Join(env.AppRoot, ".update")
	if err := os.RemoveAll(stageDir); err != nil {
		return err
	}

	stageTargets := []struct{ src, dst string }{
		{filepath.Join(extractDir, app.BinaryName()), filepath.Join(stageDir, app.ToolsDir, "linux", app.LinuxBinaryName)},
		{filepath.Join(extractDir, otherPlatformBinaryName()), filepath.Join(stageDir, app.ToolsDir, "windows", app.WindowsBinaryName)},
		{filepath.Join(extractDir, "takeOutBack.sh"), filepath.Join(stageDir, "..", "takeOutBack.sh")},
		{filepath.Join(extractDir, "takeOutBack.bat"), filepath.Join(stageDir, "..", "takeOutBack.bat")},
		{filepath.Join(extractDir, "install.sh"), filepath.Join(stageDir, app.ScriptsDir, "install.sh")},
		{filepath.Join(extractDir, "install.ps1"), filepath.Join(stageDir, app.ScriptsDir, "install.ps1")},
		{filepath.Join(extractDir, "README.md"), filepath.Join(stageDir, "..", "README.md")},
		{filepath.Join(extractDir, "CHANGELOG.md"), filepath.Join(stageDir, "..", "CHANGELOG.md")},
	}
	for _, doc := range []string{"Architecture.md", "Installation.md", "Usage.md", "Development.md", "Troubleshooting.md"} {
		stageTargets = append(stageTargets, struct{ src, dst string }{
			filepath.Join(extractDir, doc),
			filepath.Join(stageDir, app.DocsDir, doc),
		})
	}

	for _, t := range stageTargets {
		if _, err := os.Stat(t.src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := os.MkdirAll(filepath.Dir(t.dst), 0755); err != nil {
			return err
		}
		if err := copyFile(t.src, t.dst); err != nil {
			return err
		}
	}

	// Copy the merged configuration files into the stage area.
	for _, cfg := range []struct{ src, dst string }{
		{filepath.Join(env.ConfigDir, app.SettingsName), filepath.Join(stageDir, app.ConfigDir, app.SettingsName)},
		{filepath.Join(env.ConfigDir, app.PolicyName), filepath.Join(stageDir, app.ConfigDir, app.PolicyName)},
	} {
		if _, err := os.Stat(cfg.src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := os.MkdirAll(filepath.Dir(cfg.dst), 0755); err != nil {
			return err
		}
		if err := copyFile(cfg.src, cfg.dst); err != nil {
			return err
		}
	}

	// Create the pending flag so the launcher knows it must apply the update.
	pendingFlag := filepath.Join(stageDir, "pending")
	if err := os.WriteFile(pendingFlag, []byte(version+"\n"), 0644); err != nil {
		return err
	}

	env.Summary("Update to %s staged. Restart TakeOutBack to complete the update.", version)
	return ErrRestartRequired
}

func otherPlatformBinaryName() string {
	if runtime.GOOS == "windows" {
		return "takeoutback-linux-amd64"
	}
	return "takeoutback-windows-amd64.exe"
}

func verifyBinaries(dir string) error {
	for _, name := range []string{"takeoutback-linux-amd64", "takeoutback-windows-amd64.exe"} {
		binPath := filepath.Join(dir, name)
		sumPath := binPath + ".sha256"
		expected, err := readFirstField(sumPath)
		if err != nil {
			return fmt.Errorf("cannot read checksum for %s: %w", name, err)
		}
		got, err := fileSHA256(binPath)
		if err != nil {
			return fmt.Errorf("cannot hash %s: %w", name, err)
		}
		if !strings.EqualFold(got, expected) {
			return fmt.Errorf("checksum mismatch for %s: expected %s got %s", name, expected, got)
		}
	}
	return nil
}

func downloadFile(client *http.Client, url, path, label string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}

	var reader io.Reader = resp.Body
	if resp.ContentLength > 0 {
		bar := progressbar.NewByte(resp.ContentLength, label)
		defer bar.Finish()
		reader = &progressReader{resp.Body, 0, bar}
	}

	_, err = io.Copy(out, reader)
	if cerr := out.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

type progressReader struct {
	r    io.Reader
	sent int64
	bar  *progressbar.Byte
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.sent += int64(n)
		pr.bar.Add(int64(n))
	}
	return n, err
}

func extractZip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	for _, f := range r.File {
		path := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(path, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal zip path: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, f.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			_ = out.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		_ = rc.Close()
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readFirstField(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum file %s", path)
	}
	return fields[0], nil
}

func filesDiffer(a, b string) (bool, error) {
	fa, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	fb, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(fa, fb), nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

func mergeSettings(defaultsPath, localPath string) error {
	defaults, err := readJSON(defaultsPath)
	if err != nil {
		return err
	}
	local, err := readJSON(localPath)
	if err != nil {
		return err
	}
	merged := mergeMap(defaults, local)
	if defStorage, ok := defaults["safe_mode_storage"].(map[string]any); ok {
		if localStorage, ok := local["safe_mode_storage"].(map[string]any); ok {
			merged["safe_mode_storage"] = mergeMap(defStorage, localStorage)
		}
	}
	return writeJSON(localPath, merged)
}

func mergePolicy(defaultsPath, localPath string) error {
	defaults, err := readJSON(defaultsPath)
	if err != nil {
		return err
	}
	local, err := readJSON(localPath)
	if err != nil {
		return err
	}
	return writeJSON(localPath, mergeMap(defaults, local))
}

func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	return m, nil
}

func writeJSON(path string, m map[string]any) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func mergeMap(src, dst map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	for k, v := range src {
		out[k] = v
	}
	for k, v := range dst {
		out[k] = v
	}
	return out
}

func looksLikeVersion(s string) bool {
	s = strings.TrimPrefix(s, "v")
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// compareVersion compares two semantic version strings starting with 'v'.
// Returns >0 if a is newer than b, 0 if equal, <0 if older.
func compareVersion(a, b string) int {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, _ := strconv.Atoi(as[i])
		bi, _ := strconv.Atoi(bs[i])
		if ai > bi {
			return 1
		}
		if ai < bi {
			return -1
		}
	}
	return len(as) - len(bs)
}
