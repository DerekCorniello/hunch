package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Endpoints are variables rather than constants so tests can redirect them
// at a local server.
var (
	latestReleaseAPI = "https://api.github.com/repos/DerekCorniello/hunch/releases/latest"
	latestAssetURL   = "https://github.com/DerekCorniello/hunch/releases/latest/download/"
)

const (
	// downloadTimeout covers fetching a release binary, roughly 16 MB.
	downloadTimeout = 5 * time.Minute
	// checksumFile is the digest manifest published alongside the binaries.
	checksumFile = "SHA256SUMS"
	// sha256HexLen is the length of a SHA-256 digest in hex.
	sha256HexLen = 64
	releasesPage = "https://github.com/DerekCorniello/hunch/releases/latest"
)

func cmdUpdate() error {
	fmt.Println("hunch update")
	fmt.Println()
	fmt.Printf("current version: %s\n", Version)

	latest, err := fetchLatestVersion()
	if err != nil {
		return fmt.Errorf("check latest version: %w", err)
	}
	fmt.Printf("latest version:  %s\n", latest)

	if Version != "dev" && Version == latest {
		fmt.Println("\nAlready up to date.")
		return nil
	}

	asset, err := assetName()
	if err != nil {
		return err
	}

	exe, err := currentExecutable()
	if err != nil {
		return err
	}

	// Fetch the expected digest before the binary, so a release without
	// checksums fails before anything is written to disk.
	wantSum, err := fetchChecksum(asset)
	if err != nil {
		return fmt.Errorf("verify %s: %w", asset, err)
	}

	fmt.Printf("\nDownloading %s...\n", asset)
	tmp, err := downloadAsset(latestAssetURL+asset, filepath.Dir(exe))
	if err != nil {
		return withPermissionHint(fmt.Errorf("download %s: %w", asset, err), exe)
	}
	defer os.Remove(tmp) // no-op once the rename below succeeds

	if err := verifyChecksum(tmp, wantSum); err != nil {
		return fmt.Errorf("verify %s: %w", asset, err)
	}

	if err := replaceExecutable(tmp, exe); err != nil {
		return withPermissionHint(fmt.Errorf("replace %s: %w", exe, err), exe)
	}

	fmt.Printf("Updated %s to %s.\n", exe, latest)
	fmt.Println("Restarting daemon...")
	if err := restartDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: restart daemon: %v\n", err)
		fmt.Println("Restart manually: hunch daemon stop && hunch daemon start")
	}
	return nil
}

// fetchChecksum returns the expected SHA-256 digest for asset, read from the
// release's SHA256SUMS file.
//
// This fails closed. Replacing the running executable with an unverified
// download is the one operation where a silent fallback would be indefensible,
// so a missing or unparseable checksum file aborts the update rather than
// proceeding on HTTPS alone.
func fetchChecksum(asset string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(latestAssetURL + checksumFile)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s is not published for this release (GitHub returned %d); download the binary manually from %s", checksumFile, resp.StatusCode, releasesPage)
	}

	// Each line is "<hex digest>  <asset name>", as written by sha256sum.
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		digest, name, found := strings.Cut(strings.TrimSpace(sc.Text()), "  ")
		if found && name == asset {
			if len(digest) != sha256HexLen {
				return "", fmt.Errorf("malformed digest for %s in %s", asset, checksumFile)
			}
			return digest, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s has no entry for %s", checksumFile, asset)
}

// verifyChecksum reports whether the file at path hashes to want.
func verifyChecksum(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s. The download may be corrupt or tampered with; it has not been installed", want, got)
	}
	return nil
}

// withPermissionHint annotates a permission failure with the reason it is
// usually hit: hunch installed into a directory the user cannot write.
func withPermissionHint(err error, exe string) error {
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w\n\n%s is not writable by the current user. Re-run with elevated privileges, or download the new binary manually from %s", err, filepath.Dir(exe), releasesPage)
}

// releasePlatforms mirrors the build matrix in .github/workflows/release.yml.
// A platform absent here has no published binary and cannot self-update.
var releasePlatforms = map[string]bool{
	"linux/amd64":   true,
	"linux/arm64":   true,
	"darwin/amd64":  true,
	"darwin/arm64":  true,
	"windows/amd64": true,
	"windows/arm64": true,
}

func assetName() (string, error) {
	return assetNameFor(runtime.GOOS, runtime.GOARCH)
}

// assetNameFor returns the release asset published for a platform.
func assetNameFor(goos, goarch string) (string, error) {
	if !releasePlatforms[goos+"/"+goarch] {
		return "", fmt.Errorf("no release binary is published for %s/%s; reinstall from source with: go install github.com/DerekCorniello/hunch@latest", goos, goarch)
	}

	name := "hunch-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

// currentExecutable resolves the path of the running binary, following any
// symlink so the update lands on the real file rather than the link.
func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", exe, err)
	}
	return resolved, nil
}

// downloadAsset streams url into a new file in dir and returns its path. The
// file is created alongside the target executable so the subsequent rename
// stays on one filesystem, which is what makes the swap atomic.
func downloadAsset(url, dir string) (string, error) {
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned %d", resp.StatusCode)
	}

	f, err := os.CreateTemp(dir, ".hunch-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmp := f.Name()

	n, err := io.Copy(f, resp.Body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if n == 0 {
		os.Remove(tmp)
		return "", fmt.Errorf("downloaded an empty file")
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// replaceExecutable moves tmp onto exe. On Unix a rename over a running
// binary is safe: the running process keeps its open inode. Windows refuses
// to overwrite a running image, so the old file is moved aside first and
// removed on a later run, once it is no longer mapped.
func replaceExecutable(tmp, exe string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(tmp, exe)
	}

	old := exe + ".old"
	os.Remove(old) // left behind by a previous update; safe to ignore
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		if restoreErr := os.Rename(old, exe); restoreErr != nil {
			return fmt.Errorf("%w (and restoring %s failed: %v)", err, exe, restoreErr)
		}
		return err
	}
	return nil
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(latestReleaseAPI)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	if release.TagName == "" {
		return "", fmt.Errorf("release has no tag name")
	}
	return release.TagName, nil
}

func restartDaemon() error {
	if err := cmdDaemonStop(); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	if err := cmdDaemonStart(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	return nil
}
