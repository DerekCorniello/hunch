package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Options holds the daemon configuration.
//
// Zero values indicate "use the default"; call LoadConfig to populate
// from config file + environment with proper defaults applied.
type Options struct {
	Socket        string        `toml:"socket"`
	DBPath        string        `toml:"db_path"`
	AcceptKeys    []string      `toml:"accept_keys"`
	DaemonBin     string        `toml:"daemon_bin"`
	HalfLifeHours int           `toml:"half_life_hours"`
	Alpha         float64       `toml:"alpha"`
	ExtraParents  []string      `toml:"extra_parents"`
	LogLevel      string        `toml:"log_level"`
	SeedPath      string        `toml:"-"` // CLI-only, not in config file
}

// defaults returns an Options with built-in defaults applied.
func defaults() Options {
	return Options{
		HalfLifeHours: 720,
		Alpha:         0.5,
		LogLevel:      "info",
	}
}

// LoadConfig reads the TOML config file and applies environment overrides.
// If the config file is missing or unreadable, it silently falls back to
// defaults. Missing fields in the file also fall back to defaults.
//
// Env overrides (applied after config file):
//
//	HUNCH_SOCKET, HUNCH_DB_PATH, HUNCH_ACCEPT_KEYS (comma-sep),
//	HUNCH_DAEMON_BIN, HUNCH_HALF_LIFE_HOURS, HUNCH_ALPHA,
//	HUNCH_EXTRA_PARENTS (comma-sep), HUNCH_LOG_LEVEL
func LoadConfig() Options {
	opts := defaults()
	log := slog.Default()

	cfgDir, err := ConfigDir()
	if err == nil {
		cfgPath := filepath.Join(cfgDir, "hunch", "config.toml")
		data, err := os.ReadFile(cfgPath)
		if err == nil {
			if err := toml.Unmarshal(data, &opts); err != nil {
				log.Debug("failed to parse config file, using defaults", "path", cfgPath, "error", err)
			}
		}
	}

	if v := os.Getenv("HUNCH_SOCKET"); v != "" {
		opts.Socket = v
	}
	if v := os.Getenv("HUNCH_DB_PATH"); v != "" {
		opts.DBPath = v
	}
	if v := os.Getenv("HUNCH_ACCEPT_KEYS"); v != "" {
		opts.AcceptKeys = strings.Split(v, ",")
	}
	if v := os.Getenv("HUNCH_DAEMON_BIN"); v != "" {
		opts.DaemonBin = v
	}
	if v := os.Getenv("HUNCH_HALF_LIFE_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.HalfLifeHours = n
		}
	}
	if v := os.Getenv("HUNCH_ALPHA"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			opts.Alpha = f
		}
	}
	if v := os.Getenv("HUNCH_EXTRA_PARENTS"); v != "" {
		opts.ExtraParents = strings.Split(v, ",")
	}
	if v := os.Getenv("HUNCH_LOG_LEVEL"); v != "" {
		opts.LogLevel = v
	}

	// Resolve default paths from platform shim if not set.
	if opts.Socket == "" {
		if cacheDir, err := CacheDir(); err == nil {
			opts.Socket = filepath.Join(cacheDir, "hunch.sock")
		}
	}
	if opts.DBPath == "" {
		if dataDir, err := DataDir(); err == nil {
			opts.DBPath = filepath.Join(dataDir, "hunch.db")
		}
	}

	return opts
}

// HalfLife returns the decay half-life as a time.Duration.
func (o Options) HalfLife() time.Duration {
	return time.Duration(o.HalfLifeHours) * time.Hour
}
