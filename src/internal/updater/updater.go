// Package updater implements the self-update command. It downloads the latest
// release asset from GitHub, verifies its checksum and atomically replaces the
// running binary.
package updater

import (
	"bufio"
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
	binName := app.BinaryName()
	baseURL := fmt.Sprintf("https://github.com/%s/releases/download/%s", app.OwnerRepo, version)
	assetURL := baseURL + "/" + binName
	sumURL := assetURL + ".sha256"

	// GitHub asset downloads redirect to a CDN. Use a client that follows
	// redirects for the actual file downloads. The timeout is long because
	// users on slow links need time to download the multi-megabyte binaries.
	downloadClient := &http.Client{Timeout: 15 * time.Minute}

	env.Summary("Downloading %s...", version)
	binPath := filepath.Join(filepath.Dir(env.BinaryPath()), ".tmp", binName)
	if err := os.MkdirAll(filepath.Dir(binPath), 0755); err != nil {
		return err
	}
	if err := downloadFile(downloadClient, assetURL, binPath, version); err != nil {
		return err
	}
	defer os.Remove(binPath)

	expected, err := downloadText(downloadClient, sumURL)
	if err != nil {
		return fmt.Errorf("cannot fetch checksum: %w", err)
	}
	expected = strings.Fields(expected)[0]
	got, err := fileSHA256(binPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("checksum mismatch: expected %s got %s", expected, got)
	}

	target := env.BinaryPath()
	// On Windows a running executable cannot be overwritten directly.
	if runtime.GOOS == "windows" {
		next := target + ".next"
		if err := os.Remove(next); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(binPath, next); err != nil {
			return err
		}
		env.Summary("Update staged to %s; restart to complete", next)
		return nil
	}
	if err := os.Chmod(binPath, 0755); err != nil {
		return err
	}
	if err := os.Rename(binPath, target); err != nil {
		return err
	}
	env.Summary("Updated to %s", version)
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
	r   io.Reader
	sent int64
	bar *progressbar.Byte
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.sent += int64(n)
		pr.bar.Add(int64(n))
	}
	return n, err
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
	scanner := bufio.NewScanner(resp.Body)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", scanner.Err()
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

