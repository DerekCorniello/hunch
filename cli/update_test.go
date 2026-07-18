package cli

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Asset names must match the artifacts the release workflow uploads exactly,
// or update silently 404s on the platforms nobody tested by hand.
func TestAssetNameForPublishedPlatforms(t *testing.T) {
	want := map[string]string{
		"linux/amd64":   "hunch-linux-amd64",
		"linux/arm64":   "hunch-linux-arm64",
		"darwin/amd64":  "hunch-darwin-amd64",
		"darwin/arm64":  "hunch-darwin-arm64",
		"windows/amd64": "hunch-windows-amd64.exe",
		"windows/arm64": "hunch-windows-arm64.exe",
	}
	if len(want) != len(releasePlatforms) {
		t.Fatalf("releasePlatforms has %d entries, test covers %d", len(releasePlatforms), len(want))
	}

	for platform, wantName := range want {
		goos, goarch, _ := strings.Cut(platform, "/")
		got, err := assetNameFor(goos, goarch)
		if err != nil {
			t.Errorf("assetNameFor(%q) returned error: %v", platform, err)
			continue
		}
		if got != wantName {
			t.Errorf("assetNameFor(%q) = %q, want %q", platform, got, wantName)
		}
	}
}

func TestAssetNameForRejectsUnpublishedPlatform(t *testing.T) {
	for _, platform := range []string{"freebsd/amd64", "linux/386", "plan9/amd64"} {
		goos, goarch, _ := strings.Cut(platform, "/")
		_, err := assetNameFor(goos, goarch)
		if err == nil {
			t.Errorf("assetNameFor(%q) succeeded, want an error", platform)
			continue
		}
		if !strings.Contains(err.Error(), "go install") {
			t.Errorf("error for %q should point at the source install, got %q", platform, err)
		}
	}
}

func TestAssetNameUsesRuntimePlatform(t *testing.T) {
	got, err := assetName()
	want, wantErr := assetNameFor(runtime.GOOS, runtime.GOARCH)
	if (err == nil) != (wantErr == nil) || got != want {
		t.Errorf("assetName() = (%q, %v), want (%q, %v)", got, err, want, wantErr)
	}
}

func TestDownloadAssetWritesExecutableFile(t *testing.T) {
	const body = "#!/bin/sh\necho hunch\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	tmp, err := downloadAsset(srv.URL, dir)
	if err != nil {
		t.Fatalf("downloadAsset: %v", err)
	}

	if got := filepath.Dir(tmp); got != dir {
		t.Errorf("downloaded into %q, want %q", got, dir)
	}
	got, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body {
		t.Errorf("content = %q, want %q", got, body)
	}

	info, err := os.Stat(tmp)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want the executable bit set", info.Mode().Perm())
	}
}

func TestDownloadAssetRejectsNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dir := t.TempDir()
	if _, err := downloadAsset(srv.URL, dir); err == nil {
		t.Fatal("expected an error for a 404 response")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("left %d file(s) behind after a failed download", len(entries))
	}
}

func TestDownloadAssetRejectsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	if _, err := downloadAsset(srv.URL, dir); err == nil {
		t.Fatal("expected an error for an empty body")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("left %d file(s) behind after an empty download", len(entries))
	}
}

func TestReplaceExecutableSwapsContent(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hunch")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, ".hunch-update-1")
	if err := os.WriteFile(tmp, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceExecutable(tmp, exe); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("temp file should be gone after the rename")
	}
}

func TestFetchLatestVersionParsesTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	defer srv.Close()

	t.Cleanup(func(orig string) func() {
		return func() { latestReleaseAPI = orig }
	}(latestReleaseAPI))
	latestReleaseAPI = srv.URL

	got, err := fetchLatestVersion()
	if err != nil {
		t.Fatalf("fetchLatestVersion: %v", err)
	}
	if got != "v9.9.9" {
		t.Errorf("got %q, want %q", got, "v9.9.9")
	}
}

func TestFetchLatestVersionRejectsEmptyTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	t.Cleanup(func(orig string) func() {
		return func() { latestReleaseAPI = orig }
	}(latestReleaseAPI))
	latestReleaseAPI = srv.URL

	if _, err := fetchLatestVersion(); err == nil {
		t.Fatal("expected an error when tag_name is missing")
	}
}

func TestFetchLatestVersionRejectsNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Cleanup(func(orig string) func() {
		return func() { latestReleaseAPI = orig }
	}(latestReleaseAPI))
	latestReleaseAPI = srv.URL

	if _, err := fetchLatestVersion(); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestWithPermissionHintOnlyAnnotatesPermissionErrors(t *testing.T) {
	permErr := withPermissionHint(fs.ErrPermission, "/usr/local/bin/hunch")
	if !strings.Contains(permErr.Error(), "/usr/local/bin") {
		t.Errorf("permission error should name the directory, got %q", permErr)
	}
	if !errors.Is(permErr, fs.ErrPermission) {
		t.Error("wrapping should preserve errors.Is(fs.ErrPermission)")
	}

	other := errors.New("connection reset")
	if got := withPermissionHint(other, "/usr/local/bin/hunch"); got.Error() != "connection reset" {
		t.Errorf("non-permission error was modified: %q", got)
	}
}

func TestCurrentExecutableResolves(t *testing.T) {
	exe, err := currentExecutable()
	if err != nil {
		t.Fatalf("currentExecutable: %v", err)
	}
	if !filepath.IsAbs(exe) {
		t.Errorf("expected an absolute path, got %q", exe)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Errorf("resolved path does not exist: %v", err)
	}
}
