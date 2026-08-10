// Package updater implements the self-update command. It downloads the latest
// release asset from GitHub, verifies its checksum and atomically replaces the
// running binary.
package updater

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
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

	// Resolve the latest tag.
	latestURL := fmt.Sprintf("https://github.com/%s/releases/latest", app.OwnerRepo)
	req, err := http.NewRequest("HEAD", latestURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		env.Summary("Update check failed: %v", err)
		return nil
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 302 && resp.StatusCode != 301 {
		env.Summary("No update available or offline (status %d)", resp.StatusCode)
		return nil
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		env.Summary("Could not determine latest release")
		return nil
	}
	parts := strings.Split(loc, "/")
	latest := parts[len(parts)-1]
	if latest == "" {
		env.Summary("Could not parse latest release tag")
		return nil
	}
	if compareVersion(latest, app.Version) <= 0 {
		env.Summary("Already up to date (%s)", app.Version)
		return nil
	}
	return installVersion(env, client, latest)
}

func installVersion(env *app.Env, client *http.Client, version string) error {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	binName := app.BinaryName()
	assetURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", app.OwnerRepo, version, binName)
	sumURL := assetURL + ".sha256"

	// GitHub asset downloads redirect to a CDN. Use a client that follows
	// redirects for the actual file downloads, while the caller's client is
	// kept redirect-less for the /releases/latest HEAD resolution.
	downloadClient := &http.Client{Timeout: 60 * time.Second}

	env.Summary("Downloading %s...", version)
	binPath := filepath.Join(filepath.Dir(env.BinaryPath()), ".tmp", binName)
	if err := os.MkdirAll(filepath.Dir(binPath), 0755); err != nil {
		return err
	}
	if err := downloadFile(downloadClient, assetURL, binPath); err != nil {
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

func downloadFile(client *http.Client, url, path string) error {
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
	_, err = io.Copy(out, resp.Body)
	if cerr := out.Close(); cerr != nil && err == nil {
		err = cerr
	}
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

