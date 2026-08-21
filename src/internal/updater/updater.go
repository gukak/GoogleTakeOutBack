// Package updater implements the self-update command. It downloads the latest
// release assets from GitHub, verifies binary checksums and atomically replaces
// the running binary along with its companion files.
package updater

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Update checks GitHub Releases for a newer version and installs it.
// If --version vX.Y.Z is provided, that exact version is installed instead.
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

	// Resolve the latest tag via the GitHub API. This is more reliable than
	// following the /releases/latest redirect, which can be cached or blocked.
	latest, _, err := resolveLatestRelease(client)
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

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// resolveLatestRelease returns the tag name of the latest GitHub release and
// its asset list.
func resolveLatestRelease(client *http.Client) (string, []releaseAsset, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", app.OwnerRepo)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "takeoutback/"+app.Version)

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release struct {
		TagName string         `json:"tag_name"`
		Assets  []releaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", nil, fmt.Errorf("cannot parse release metadata: %w", err)
	}
	return release.TagName, release.Assets, nil
}

// fetchReleaseAssets returns the assets for a specific tag.
func fetchReleaseAssets(client *http.Client, version string) ([]releaseAsset, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", app.OwnerRepo, version)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "takeoutback/"+app.Version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release struct {
		Assets []releaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("cannot parse release metadata: %w", err)
	}
	return release.Assets, nil
}

func installVersion(env *app.Env, client *http.Client, version string) error {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	assets, err := fetchReleaseAssets(client, version)
	if err != nil {
		return err
	}
	assetMap := make(map[string]releaseAsset, len(assets))
	for _, a := range assets {
		assetMap[a.Name] = a
	}

	binName := app.BinaryName()
	otherBinName := otherPlatformBinaryName()
	for _, name := range []string{binName, binName + ".sha256", otherBinName, otherBinName + ".sha256"} {
		if _, ok := assetMap[name]; !ok {
			return fmt.Errorf("release %s is missing asset %s", version, name)
		}
	}

	tmpDir := filepath.Join(filepath.Dir(env.BinaryPath()), ".tmp", "update-"+version)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// GitHub asset downloads redirect to a CDN. Use a client that follows
	// redirects for the actual file downloads. The timeout is long because
	// users on slow links need time to download the multi-megabyte binaries.
	downloadClient := &http.Client{Timeout: 15 * time.Minute}

	env.Summary("Downloading %s...", version)

	// Download all non-checksum assets. Checksums are fetched alongside the
	// binaries they verify.
	for _, a := range assets {
		if strings.HasSuffix(a.Name, ".sha256") {
			continue
		}
		dst := filepath.Join(tmpDir, a.Name)
		if isBinaryAsset(a.Name) {
			sumAsset := assetMap[a.Name+".sha256"]
			if err := downloadBinary(downloadClient, a.URL, sumAsset.URL, dst, version); err != nil {
				return err
			}
			continue
		}
		if err := downloadTextFile(downloadClient, a.URL, dst, a.Name); err != nil {
			return err
		}
	}

	// The launcher script cannot be replaced while it is running. If the
	// release contains a different launcher than the local one, refuse the
	// update and ask the user to update manually.
	launcherName := "takeOutBack.sh"
	if runtime.GOOS == "windows" {
		launcherName = "takeOutBack.bat"
	}
	localLauncher := filepath.Join(env.Root, launcherName)
	newLauncher := filepath.Join(tmpDir, launcherName)
	if _, err := os.Stat(localLauncher); err == nil {
		changed, err := filesDiffer(localLauncher, newLauncher)
		if err != nil {
			return err
		}
		if changed {
			return fmt.Errorf("%s has changed in %s and cannot be replaced by a running process. Please update manually (see Installation.md)", launcherName, version)
		}
	}

	// Apply all non-config files atomically.
	type target struct{ src, dst string }
	targets := []target{
		{filepath.Join(tmpDir, binName), env.BinaryPath()},
		{filepath.Join(tmpDir, otherBinName), otherBinaryPath(env)},
		{filepath.Join(tmpDir, "takeOutBack.sh"), filepath.Join(env.Root, "takeOutBack.sh")},
		{filepath.Join(tmpDir, "takeOutBack.bat"), filepath.Join(env.Root, "takeOutBack.bat")},
		{filepath.Join(tmpDir, "install.sh"), filepath.Join(env.AppRoot, app.ScriptsDir, "install.sh")},
		{filepath.Join(tmpDir, "install.ps1"), filepath.Join(env.AppRoot, app.ScriptsDir, "install.ps1")},
		{filepath.Join(tmpDir, "README.md"), filepath.Join(env.Root, "README.md")},
		{filepath.Join(tmpDir, "CHANGELOG.md"), filepath.Join(env.Root, "CHANGELOG.md")},
	}
	for _, doc := range []string{"Architecture.md", "Installation.md", "Usage.md", "Development.md", "Troubleshooting.md"} {
		targets = append(targets, target{filepath.Join(tmpDir, doc), filepath.Join(env.AppRoot, app.DocsDir, doc)})
	}

	for _, t := range targets {
		if _, err := os.Stat(t.src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := os.MkdirAll(filepath.Dir(t.dst), 0755); err != nil {
			return err
		}
		if err := replaceFile(t.src, t.dst); err != nil {
			return err
		}
		// Restore executable bit on binaries; the temp file was created with
		// default permissions and GitHub releases do not carry mode metadata.
		if isBinaryAsset(filepath.Base(t.src)) {
			if err := os.Chmod(t.dst, 0755); err != nil {
				return err
			}
		}
	}

	// Merge new default fields into existing user configuration files so that
	// custom settings are never overwritten.
	if err := mergeSettings(filepath.Join(tmpDir, "settings.json"), filepath.Join(env.ConfigDir, app.SettingsName)); err != nil {
		return err
	}
	if err := mergePolicy(filepath.Join(tmpDir, "policy.json"), filepath.Join(env.ConfigDir, app.PolicyName)); err != nil {
		return err
	}

	env.Summary("Updated to %s", version)
	return nil
}

func isBinaryAsset(name string) bool {
	return name == "takeoutback-linux-amd64" || name == "takeoutback-windows-amd64.exe"
}

func otherPlatformBinaryName() string {
	if runtime.GOOS == "windows" {
		return "takeoutback-linux-amd64"
	}
	return "takeoutback-windows-amd64.exe"
}

func otherBinaryPath(env *app.Env) string {
	if runtime.GOOS == "windows" {
		return env.ToolsLinux
	}
	return env.ToolsWin
}

// downloadBinary downloads a release binary and verifies its SHA-256 checksum.
func downloadBinary(client *http.Client, url, sumURL, dst, label string) error {
	if err := downloadFile(client, url, dst, label); err != nil {
		return err
	}
	expected, err := downloadText(client, sumURL)
	if err != nil {
		return fmt.Errorf("cannot fetch checksum: %w", err)
	}
	expected = strings.Fields(expected)[0]
	got, err := fileSHA256(dst)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s got %s", filepath.Base(dst), expected, got)
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

func downloadTextFile(client *http.Client, url, dst, label string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	fmt.Printf("  downloading %s...\n", label)
	_, err = io.Copy(out, resp.Body)
	return err
}

func downloadText(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return "", err
	}
	return strings.Fields(buf.String())[0], nil
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

// replaceFile atomically replaces dst with src.
func replaceFile(src, dst string) error {
	// Windows does not allow renaming over an existing file.
	if runtime.GOOS == "windows" {
		_ = os.Remove(dst)
	}
	return os.Rename(src, dst)
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
	// Deep-merge the nested safe_mode_storage object.
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

// mergeMap returns a copy of dst with any missing top-level keys filled from src.
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

// ApplyStagedUpdate replaces the running binary with a previously staged
// <binary>.next file. This is used on Windows where a running executable cannot
// be overwritten directly; the updater writes the new binary next to the old one
// and the replacement happens on the next startup.
func ApplyStagedUpdate() error {
	current, err := os.Executable()
	if err != nil {
		return err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return err
	}
	next := current + ".next"
	if _, err := os.Stat(next); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	old := current + ".old"
	if err := os.Rename(current, old); err != nil {
		return fmt.Errorf("rename current binary: %w", err)
	}
	if err := os.Rename(next, current); err != nil {
		_ = os.Rename(old, current)
		return fmt.Errorf("rename staged binary: %w", err)
	}
	_ = os.Remove(old)
	return nil
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
