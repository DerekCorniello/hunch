package normalize

// WRAPPER commands that should be stripped during Phase 1 normalization.
var wrappers = map[string]bool{
	"chroot":      true,
	"doas":        true,
	"env":         true,
	"exec":        true,
	"fakeroot":    true,
	"flock":       true,
	"ionice":      true,
	"nice":        true,
	"nohup":       true,
	"numactl":     true,
	"pkexec":      true,
	"prlimit":     true,
	"setsid":      true,
	"stdbuf":      true,
	"sudo":        true,
	"systemd-run": true,
	"taskset":     true,
	"time":        true,
	"unshare":     true,
	"watch":       true,
}

// unwrapPrefixTokens strips leading wrapper commands from the token list.
// Recurse until no more wrappers are found.
func unwrapPrefixTokens(tokens []string) []string {
	for len(tokens) > 1 && isWrapper(tokens[0]) {
		cmdIdx := wrapperCommandIndex(tokens)
		if cmdIdx < 0 || cmdIdx >= len(tokens) {
			return tokens
		}
		tokens = tokens[cmdIdx:]
	}
	return tokens
}

func isWrapper(tok string) bool {
	return wrappers[tok]
}

func wrapperCommandIndex(tokens []string) int {
	if len(tokens) < 2 {
		return -1
	}
	wrapper := tokens[0]
	switch wrapper {
	case "env":
		return envCommandIndex(tokens)
	case "sudo", "doas":
		return sudoCommandIndex(tokens)
	case "chroot":
		return chrootCommandIndex(tokens)
	case "flock":
		return flockCommandIndex(tokens)
	case "systemd-run":
		return systemdRunIndex(tokens)
	case "watch":
		return watchCommandIndex(tokens)
	case "nice", "ionice", "taskset", "numactl", "prlimit", "unshare", "stdbuf", "time":
		return flagfulWrapperIndex(tokens)
	default:
		return 1
	}
}

// envCommandIndex: env [VAR=val ...] [--] cmd [args...]
func envCommandIndex(tokens []string) int {
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "--" {
			if i+1 < len(tokens) {
				return i + 1
			}
			return -1
		}
		if isEnvAssignment(tokens[i]) {
			continue
		}
		return i
	}
	return -1
}

func isEnvAssignment(tok string) bool {
	for i, ch := range tok {
		if ch == '=' && i > 0 {
			return true
		}
	}
	return false
}

// sudoCommandIndex: sudo [flags ...] [VAR=val ...] command [args...]
func sudoCommandIndex(tokens []string) int {
	for i := 1; i < len(tokens); i++ {
		if isSudoFlagWithValue(tokens[i]) {
			i++ // skip value
			continue
		}
		if isFlag(tokens[i]) {
			continue
		}
		// Skip env assignments like EDITOR=vim
		if isEnvAssignment(tokens[i]) {
			continue
		}
		return i
	}
	return -1
}

// flagfulWrapperIndex: wrapper [own-flags ...] [--] command [args...]
// Works for nice, ionice, taskset, numactl, prlimit, unshare, stdbuf, time.
// For numactl and taskset, handles both short and long flag-with-value patterns.
func flagfulWrapperIndex(tokens []string) int {
	wrapper := tokens[0]
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "--" {
			if i+1 < len(tokens) {
				return i + 1
			}
			return -1
		}
		if isFlag(tokens[i]) {
			if flagTakesValue(tokens[i], wrapper) {
				i++
			}
			continue
		}
		return i
	}
	return -1
}

// chrootCommandIndex: chroot [OPTION] NEWROOT [COMMAND [ARG]...]
// The first non-flag token is NEWROOT, the second is COMMAND.
func chrootCommandIndex(tokens []string) int {
	nonFlagCount := 0
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "--" {
			continue
		}
		if isFlag(tokens[i]) {
			if flagTakesValue(tokens[i], "chroot") {
				i++
			}
			continue
		}
		nonFlagCount++
		if nonFlagCount >= 2 {
			return i
		}
	}
	return -1
}

// flockCommandIndex: flock [OPTIONS] file command [args...]
//   - With -c/--command: flock [opts] file -c command [args]
//   - Without -c: the second non-flag arg after the lock file is the command.
func flockCommandIndex(tokens []string) int {
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "-c" || tokens[i] == "--command" {
			if i+1 < len(tokens) {
				return i + 1
			}
			return -1
		}
		if isFlag(tokens[i]) {
			continue
		}
		// First non-flag is the lock file — look for next non-flag
		for j := i + 1; j < len(tokens); j++ {
			if tokens[j] == "-c" || tokens[j] == "--command" {
				if j+1 < len(tokens) {
					return j + 1
				}
				return -1
			}
			if isFlag(tokens[j]) {
				continue
			}
			// Second non-flag is the command
			return j
		}
		return -1
	}
	return -1
}

// systemdRunIndex: systemd-run [flags ...] command [args ...]
func systemdRunIndex(tokens []string) int {
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "--" {
			if i+1 < len(tokens) {
				return i + 1
			}
			return -1
		}
		if isFlag(tokens[i]) {
			if isSystemdFlagWithValue(tokens[i]) {
				i++
			}
			continue
		}
		return i
	}
	return -1
}

// watchCommandIndex: watch [flags ...] command [args ...]
func watchCommandIndex(tokens []string) int {
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "--" {
			if i+1 < len(tokens) {
				return i + 1
			}
			return -1
		}
		if isFlag(tokens[i]) {
			if isWatchFlagWithValue(tokens[i]) {
				i++
			}
			continue
		}
		return i
	}
	return -1
}

func isFlag(tok string) bool {
	return len(tok) >= 2 && tok[0] == '-'
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

func flagTakesValue(tok string, wrapper string) bool {
	switch wrapper {
	case "nice", "ionice", "prlimit":
		return len(tok) > 1 && tok[0] == '-' && tok[1] != '-'
	case "stdbuf":
		return false
	case "unshare":
		switch tok {
		case "-R", "--root", "-S", "--setuid", "--setgroups",
			"--map-user", "--map-group", "--mount-proc",
			"-C", "--cgroup", "--propagation",
			"--pid", "--time":
			return true
		default:
			return len(tok) > 1 && tok[0] == '-' && tok[1] != '-'
		}
	case "taskset", "numactl":
		if len(tok) == 2 && tok[0] == '-' && tok[1] != '-' {
			return true
		}
		switch tok {
		case "--cpu-list", "--membind", "--cpunodebind", "--physcpubind",
			"--interleave", "--preferred", "--localalloc",
			"--show", "--hardware":
			return true
		}
		return false
	case "time":
		return tok == "-f" || tok == "--format" ||
			tok == "-o" || tok == "--output" ||
			tok == "-a" || tok == "--append"
	case "chroot":
		switch tok {
		case "--userspec", "--groups", "-u", "-g":
			return true
		}
		return false
	default:
		return len(tok) > 1 && tok[0] == '-' && tok[1] != '-'
	}
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
