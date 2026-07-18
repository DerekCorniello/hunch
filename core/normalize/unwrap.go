package normalize

// wrapperConfig describes how a wrapper's own arguments are parsed to
// locate the wrapped command.
type wrapperConfig struct {
	// skipFlags causes -x and --xxx tokens to be skipped.
	skipFlags bool
	// shortFlagValue treats short flags (-x) as taking a value.
	shortFlagValue bool
	// longFlagValue reports whether a long flag takes a value.
	// If nil, no long flag takes a value.
	longFlagValue func(flag string) bool
	// skipEnvAssignment skips VAR=val style tokens.
	skipEnvAssignment bool
	// honorDoubleDash treats `--` as a separator; the token after
	// it is the wrapped command.
	honorDoubleDash bool
	// positionalSkip is the number of non-flag positional arguments
	// to skip before reaching the wrapped command (e.g. chroot
	// skips the NEWROOT path).
	positionalSkip int
}

// wrapperConfigs is the set of wrapper commands and how each strips
// its own arguments to reach the wrapped command.
//
// Wrappers that have no flags of their own (exec, fakeroot, nohup,
// pkexec, setsid) have an empty config - the wrapped command is
// always at index 1. flock is a special case handled inline in
// wrapperCommandIndex because it accepts both `-c CMD` and
// `lock-file CMD` forms; its empty config is never consulted.
var wrapperConfigs = map[string]wrapperConfig{
	"sudo": {
		skipFlags:         true,
		longFlagValue:     isSudoFlagWithValue,
		skipEnvAssignment: true,
		honorDoubleDash:   true,
	},
	"doas": {
		skipFlags:         true,
		longFlagValue:     isDoasFlagWithValue,
		skipEnvAssignment: true,
		honorDoubleDash:   true,
	},
	"env": {
		skipEnvAssignment: true,
		honorDoubleDash:   true,
	},
	"chroot": {
		skipFlags:       true,
		longFlagValue:   isChrootFlagWithValue,
		honorDoubleDash: true,
		positionalSkip:  1, // skip NEWROOT
	},
	"systemd-run": {
		skipFlags:       true,
		longFlagValue:   isSystemdFlagWithValue,
		honorDoubleDash: true,
	},
	"watch": {
		skipFlags:       true,
		longFlagValue:   isWatchFlagWithValue,
		honorDoubleDash: true,
	},
	"nice": {
		skipFlags:       true,
		shortFlagValue:  true,
		honorDoubleDash: true,
	},
	"ionice": {
		skipFlags:       true,
		shortFlagValue:  true,
		honorDoubleDash: true,
	},
	"taskset": {
		skipFlags:       true,
		shortFlagValue:  true,
		longFlagValue:   isTasksetFlagWithValue,
		honorDoubleDash: true,
	},
	"numactl": {
		skipFlags:       true,
		shortFlagValue:  true,
		longFlagValue:   isNumactlFlagWithValue,
		honorDoubleDash: true,
	},
	"prlimit": {
		skipFlags:       true,
		shortFlagValue:  true,
		honorDoubleDash: true,
	},
	"unshare": {
		skipFlags:       true,
		shortFlagValue:  true,
		longFlagValue:   isUnshareFlagWithValue,
		honorDoubleDash: true,
	},
	"stdbuf": {
		skipFlags: true,
	},
	"time": {
		skipFlags:       true,
		longFlagValue:   isTimeFlagWithValue,
		honorDoubleDash: true,
	},

	// Wrappers with no flags of their own - inner command at index 1.
	"exec":     {},
	"fakeroot": {},
	"nohup":    {},
	"pkexec":   {},
	"setsid":   {},

	// flock is dispatched specially in wrapperCommandIndex.
	"flock": {},
}

// unwrapPrefixTokens strips leading wrapper commands from the token list,
// recursing until no more wrappers are found. Leading bare env assignments
// (FOO=bar) are also stripped before wrapper processing.
func unwrapPrefixTokens(tokens []string) []string {
	// Strip leading bare env assignments: FOO=bar BAZ=qux make
	// Guard with !isFlag to avoid stripping --flag=value tokens.
	for len(tokens) > 1 && !isFlag(tokens[0]) && isEnvAssignment(tokens[0]) {
		tokens = tokens[1:]
	}

	for len(tokens) > 1 && isWrapper(tokens[0]) {
		next := wrapperCommandIndex(tokens)
		if next < 1 || next >= len(tokens) {
			return tokens
		}
		tokens = tokens[next:]
	}
	return tokens
}

func isWrapper(tok string) bool {
	_, ok := wrapperConfigs[tok]
	return ok
}

// wrapperCommandIndex returns the index in tokens of the wrapped command,
// or -1 if it cannot be found. flock is dispatched specially because it
// accepts both `-c CMD` and `lock-file CMD` forms.
func wrapperCommandIndex(tokens []string) int {
	if len(tokens) < 2 {
		return -1
	}
	if tokens[0] == "flock" {
		return flockCommandIndex(tokens)
	}
	cfg, ok := wrapperConfigs[tokens[0]]
	if !ok {
		return -1
	}
	return findInnerCommand(tokens, cfg)
}

// findInnerCommand walks tokens starting at index 1, skipping flags,
// env assignments, and any configured positional arguments, to find
// the index of the wrapped command. Returns -1 if the tokens end
// before the command is found.
func findInnerCommand(tokens []string, cfg wrapperConfig) int {
	skipped := 0
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]

		if cfg.honorDoubleDash && tok == "--" {
			if i+1 < len(tokens) {
				return i + 1
			}
			return -1
		}

		if cfg.skipFlags && isFlag(tok) {
			switch {
			case cfg.longFlagValue != nil && cfg.longFlagValue(tok):
				i++ // consume the value
			case cfg.shortFlagValue && len(tok) == 2:
				i++ // consume the value
			}
			continue
		}

		if cfg.skipEnvAssignment && isEnvAssignment(tok) {
			continue
		}

		if skipped < cfg.positionalSkip {
			skipped++
			continue
		}

		return i
	}
	return -1
}

// flockCommandIndex handles flock's two command-specifying forms:
//   - flock [opts] file -c command [args]
//   - flock [opts] --command command [args]
//   - flock [opts] file command [args]   (lock-file, then command)
//
// flock uses `-` as a stdin lock file, so it is not treated as a flag.
func flockCommandIndex(tokens []string) int {
	sawNonFlag := false
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "-c" || tok == "--command" {
			if i+1 < len(tokens) {
				return i + 1
			}
			return -1
		}
		if isFlockFlag(tok) {
			continue
		}
		if !sawNonFlag {
			sawNonFlag = true
			continue
		}
		return i
	}
	return -1
}

// isFlag reports whether tok looks like a command-line flag:
// `-`, `-x`, or `--xxx`. Matches the same set used in classifyToken.
func isFlag(tok string) bool {
	return len(tok) >= 1 && tok[0] == '-'
}

// isFlockFlag reports whether tok is a flock flag. flock uses `-` as
// a stdin lock file, so it is excluded here.
func isFlockFlag(tok string) bool {
	return isFlag(tok) && tok != "-"
}

func isSudoFlagWithValue(tok string) bool {
	switch tok {
	case "-u", "--user", "-g", "--group",
		"-p", "--prompt", "-h", "--host",
		"-r", "--role", "-t", "--type",
		"-C", "--close-from", "-D", "--chdir":
		return true
	}
	return false
}

func isDoasFlagWithValue(tok string) bool {
	switch tok {
	case "-u", "--user", "-g", "--group",
		"-p", "--prompt", "-C", "--chdir":
		return true
	}
	return false
}

func isChrootFlagWithValue(tok string) bool {
	switch tok {
	case "--userspec", "--groups", "-u", "-g":
		return true
	}
	return false
}

func isSystemdFlagWithValue(tok string) bool {
	switch tok {
	case "-p", "--property", "-P", "--pipe",
		"--working-directory", "-d", "--same-dir",
		"--unit", "-u", "--uid", "-g", "--gid",
		"--description", "-n", "-E", "--setenv",
		"-t", "--pty", "--slice", "-r", "--remain-after-exit",
		"--send-sighup", "--service-type", "-q", "--quiet":
		return true
	}
	return false
}

func isWatchFlagWithValue(tok string) bool {
	switch tok {
	case "-n", "--interval", "-p", "--precise", "-e", "--exec",
		"-t", "--no-title", "-b", "--beep", "-x",
		"-g", "--chgexit", "--color", "-E", "--errexit":
		return true
	}
	return false
}

func isTasksetFlagWithValue(tok string) bool {
	switch tok {
	case "--cpu-list":
		return true
	}
	return false
}

func isNumactlFlagWithValue(tok string) bool {
	switch tok {
	case "--membind", "--cpunodebind", "--physcpubind",
		"--interleave", "--preferred", "--localalloc",
		"--show", "--hardware":
		return true
	}
	return false
}

func isUnshareFlagWithValue(tok string) bool {
	switch tok {
	case "-R", "--root", "-S", "--setuid", "--setgroups",
		"--map-user", "--map-group", "--mount-proc",
		"-C", "--cgroup", "--propagation",
		"--pid", "--time":
		return true
	}
	return false
}

func isTimeFlagWithValue(tok string) bool {
	switch tok {
	case "-f", "--format", "-o", "--output", "-a", "--append":
		return true
	}
	return false
}

func isEnvAssignment(tok string) bool {
	for i, ch := range tok {
		if ch == '=' && i > 0 {
			return true
		}
	}
	return false
}
