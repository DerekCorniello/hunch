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
	Socket        string   `toml:"socket"`
	DBPath        string   `toml:"db_path"`
	AcceptKeys    []string `toml:"accept_keys"`
	DaemonBin     string   `toml:"daemon_bin"`
	HalfLifeHours int      `toml:"half_life_hours"`
	Alpha         float64  `toml:"alpha"`
	Beta          float64  `toml:"beta"`    // CWD-affinity boost strength
	Gamma         float64  `toml:"gamma"`   // failure-rate suppression strength
	Delta         float64  `toml:"delta"`   // prior-outcome boost strength
	Epsilon       float64  `toml:"epsilon"` // confirmed-acceptance boost strength
	// MinConfidence is the score a suggestion must reach before it is
	// offered from a generalized (fallback) context. The exact-context
	// match is always trusted; broader matches must clear this bar so
	// widening coverage does not turn into guessing.
	MinConfidence float64 `toml:"min_confidence"`
	// MinCount is how many times a transition must have been observed before
	// it is offered at all. A transition seen once is the only candidate for
	// its state, so additive smoothing scores it 1.0: maximum probability on
	// minimum evidence. Confidence cannot separate a habit from a one-off,
	// so this does.
	MinCount     int      `toml:"min_count"`
	ExtraParents []string `toml:"extra_parents"`
	Ignore       []string `toml:"ignore"` // extra regexes for sensitive commands to never record
	LogLevel     string   `toml:"log_level"`
	SeedPath     string   `toml:"-"` // CLI-only, not in config file
}

// defaults returns an Options with built-in defaults applied.
func defaults() Options {
	return Options{
		HalfLifeHours: 720,
		Alpha:         0.5,
		Beta:          0.75,
		Gamma:         0.5,
		Delta:         0.5,
		Epsilon:       0.5,
		MinConfidence: 0.20,
		MinCount:      2,
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
//	HUNCH_BETA, HUNCH_GAMMA, HUNCH_DELTA, HUNCH_EPSILON,
//	HUNCH_EXTRA_PARENTS (comma-sep), HUNCH_IGNORE (comma-sep regexes),
//	HUNCH_LOG_LEVEL
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
	if v := os.Getenv("HUNCH_BETA"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			opts.Beta = f
		}
	}
	if v := os.Getenv("HUNCH_GAMMA"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			opts.Gamma = f
		}
	}
	if v := os.Getenv("HUNCH_DELTA"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			opts.Delta = f
		}
	}
	if v := os.Getenv("HUNCH_EPSILON"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			opts.Epsilon = f
		}
	}
	if v := os.Getenv("HUNCH_MIN_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			opts.MinCount = n
		}
	}
	if v := os.Getenv("HUNCH_MIN_CONFIDENCE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			opts.MinConfidence = f
		}
	}
	if v := os.Getenv("HUNCH_EXTRA_PARENTS"); v != "" {
		opts.ExtraParents = strings.Split(v, ",")
	}
	if v := os.Getenv("HUNCH_IGNORE"); v != "" {
		opts.Ignore = strings.Split(v, ",")
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
