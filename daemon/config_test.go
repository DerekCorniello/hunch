package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	// Clear HUNCH_ env vars set by other tests.
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "HUNCH_") {
			t.Setenv(e[:strings.IndexByte(e, '=')], "")
		}
	}

	opts := LoadConfig()

	if opts.HalfLifeHours != 720 {
		t.Errorf("HalfLifeHours = %d, want 720", opts.HalfLifeHours)
	}
	if opts.Alpha != 0.5 {
		t.Errorf("Alpha = %f, want 0.5", opts.Alpha)
	}
	if opts.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", opts.LogLevel, "info")
	}
	if opts.HalfLife() == 0 {
		t.Error("HalfLife() returned zero duration")
	}
}

func TestLoadConfigEnvOverrides(t *testing.T) {
	t.Setenv("HUNCH_SOCKET", "/tmp/test.sock")
	t.Setenv("HUNCH_DB_PATH", "/tmp/test.db")
	t.Setenv("HUNCH_HALF_LIFE_HOURS", "168")
	t.Setenv("HUNCH_ALPHA", "1.0")
	t.Setenv("HUNCH_LOG_LEVEL", "debug")
	t.Setenv("HUNCH_ACCEPT_KEYS", "tab,right")
	t.Setenv("HUNCH_EXTRA_PARENTS", "mycli,teamtool")

	opts := LoadConfig()

	if opts.Socket != "/tmp/test.sock" {
		t.Errorf("Socket = %q, want %q", opts.Socket, "/tmp/test.sock")
	}
	if opts.DBPath != "/tmp/test.db" {
		t.Errorf("DBPath = %q, want %q", opts.DBPath, "/tmp/test.db")
	}
	if opts.HalfLifeHours != 168 {
		t.Errorf("HalfLifeHours = %d, want 168", opts.HalfLifeHours)
	}
	if opts.Alpha != 1.0 {
		t.Errorf("Alpha = %f, want 1.0", opts.Alpha)
	}
	if opts.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", opts.LogLevel, "debug")
	}
	if len(opts.AcceptKeys) != 2 || opts.AcceptKeys[0] != "tab" || opts.AcceptKeys[1] != "right" {
		t.Errorf("AcceptKeys = %v, want [tab right]", opts.AcceptKeys)
	}
	if len(opts.ExtraParents) != 2 || opts.ExtraParents[0] != "mycli" || opts.ExtraParents[1] != "teamtool" {
		t.Errorf("ExtraParents = %v, want [mycli teamtool]", opts.ExtraParents)
	}
}

func TestLoadConfigEnvInvalidNumbers(t *testing.T) {
	t.Setenv("HUNCH_HALF_LIFE_HOURS", "abc")
	t.Setenv("HUNCH_ALPHA", "xyz")

	opts := LoadConfig()

	if opts.HalfLifeHours != 720 {
		t.Errorf("HalfLifeHours = %d, want 720", opts.HalfLifeHours)
	}
	if opts.Alpha != 0.5 {
		t.Errorf("Alpha = %f, want 0.5", opts.Alpha)
	}
}

func TestLoadConfigTOMLParse(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "hunch")
	cfgPath := filepath.Join(cfgDir, "config.toml")

	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`
half_life_hours = 100
alpha = 0.25
log_level = "debug"
accept_keys = ["right"]
extra_parents = ["custom-tool"]
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)

	opts := LoadConfig()

	if opts.HalfLifeHours != 100 {
		t.Errorf("HalfLifeHours = %d, want 100", opts.HalfLifeHours)
	}
	if opts.Alpha != 0.25 {
		t.Errorf("Alpha = %f, want 0.25", opts.Alpha)
	}
}

func TestLoadConfigEnvOverridesTOML(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "hunch")
	cfgPath := filepath.Join(cfgDir, "config.toml")

	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`half_life_hours = 100`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	t.Setenv("HUNCH_HALF_LIFE_HOURS", "500")

	opts := LoadConfig()

	if opts.HalfLifeHours != 500 {
		t.Errorf("HalfLifeHours = %d, want 500 (env overrides TOML)", opts.HalfLifeHours)
	}
}

func TestLoadConfigMalformedTOML(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "hunch")
	cfgPath := filepath.Join(cfgDir, "config.toml")

	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`this is not valid toml {{{`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)

	opts := LoadConfig()

	if opts.HalfLifeHours != 720 {
		t.Errorf("HalfLifeHours = %d, want 720", opts.HalfLifeHours)
	}
}
