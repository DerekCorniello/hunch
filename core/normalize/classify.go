package normalize

import "regexp"

// DefaultParents is the default set of known parent commands
// whose immediate subcommand is kept verbatim during normalization.
//
// Only includes tools that have meaningful subcommands (verbs).
// Tools where the first argument is a file, URL, filter, or patch
// (curl, jq, editors, runtimes, etc.) are NOT included.
var DefaultParents = []string{
	// Version control
	"git", "hg", "svn", "pijul", "fossil",
	// Package managers & build tools
	"cargo", "npm", "yarn", "pnpm", "npx", "yarnpkg",
	"pip", "pip3", "pipenv", "poetry", "uv",
	"go", "rustup",
	"mvn", "gradle", "sbt", "ant",
	"dotnet", "nuget",
	"brew", "apt", "apt-get", "apt-cache", "dpkg",
	"dnf", "yum", "rpm",
	"pacman", "pamac", "yay", "paru",
	"snap", "flatpak",
	"choco", "scoop", "winget",
	// Container & orchestration
	"docker", "podman", "lxc", "lxd",
	"kubectl", "kubectx", "kubens", "helm",
	"istioctl", "linkerd",
	// Cloud & infrastructure
	"aws", "gcloud", "az", "heroku", "flyctl", "doctl",
	"terraform", "tofu", "packer", "waypoint",
	"ansible", "ansible-playbook",
	"vault", "consul", "nomad", "boundary",
	// System management
	"systemctl", "journalctl", "loginctl", "timedatectl",
	// Terminal multiplexers
	"tmux", "screen", "zellij",
	// GitHub/GitLab CLI
	"gh", "glab", "bw",
	// Security
	"pass", "gpg", "ssh-keygen", "openssl",
	// Mobile
	"adb", "fastboot",
	// JS runtimes (have subcommands like run, test, fmt, install)
	"deno", "bun",
	// Scheduling
	"crontab", "at", "systemd-analyze",
}

// Token class patterns.
var (
	flagRe = regexp.MustCompile(`^-{1,2}`)
	pathRe = regexp.MustCompile(`/`)
	repoRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*://|www\.)|@.*:|\.git$`)
	hashRe = regexp.MustCompile(`^[0-9a-fA-F]{6,40}$`)
	numRe  = regexp.MustCompile(`^-?\d+\.?\d*$`)
)

// classifyTokens runs Phase 2 token-type classification and collapses
// consecutive tokens of the same type.
func classifyTokens(tokens []string, parents []string) []string {
	if len(tokens) == 0 {
		return nil
	}

	parentSet := makeSet(parents)

	// Phase 2a: classify each token individually
	classified := make([]classification, len(tokens))
	for i, tok := range tokens {
		classified[i] = classifyToken(tok, i, tokens, parentSet)
	}

	// Phase 2b: collapse consecutive same-type tokens
	return collapseTokens(tokens, classified)
}

type classification int

const (
	classPlain  classification = iota // kept verbatim
	classFlag                         // collapsed to FLAG
	classPath                         // collapsed to PATH
	classRepo                         // collapsed to REPO
	classHash                         // collapsed to HASH
	classNum                          // collapsed to NUM
	classStr                          // collapsed to STR (unmatched args)
	classKwargs                       // -- everything after KWARGS
)

// classifyToken determines the classification for a single token.
func classifyToken(tok string, idx int, tokens []string, parents map[string]bool) classification {
	if tok == "--" {
		return classKwargs
	}

	// Flag: starts with - or -- (must be at least 2 chars)
	if flagRe.MatchString(tok) {
		return classFlag
	}

	// The first non-flag token is the command itself.
	if idx == 0 {
		return classPlain
	}

	// Known parent: the immediate subcommand (second token) is kept verbatim.
	if idx == 1 && parents[tokens[0]] {
		return classPlain
	}

	// URL or git remote — check BEFORE path to catch URLs with slashes.
	if repoRe.MatchString(tok) {
		return classRepo
	}

	// Git hash (6 to 40 hex chars).
	if hashRe.MatchString(tok) {
		return classHash
	}

	// Numeric.
	if numRe.MatchString(tok) {
		return classNum
	}

	// Path: contains / or starts with . or ~
	if pathRe.MatchString(tok) || (len(tok) > 0 && (tok[0] == '.' || tok[0] == '~')) {
		return classPath
	}

	// Unmatched tokens after the command: treat as generic STR.
	return classStr
}

// collapseTokens merges consecutive tokens of the same classification,
// keeping one representative token.
func collapseTokens(tokens []string, classified []classification) []string {
	if len(tokens) == 0 {
		return nil
	}

	result := []string{tokens[0]}
	inKwargs := false

	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "--" {
			if !inKwargs {
				result = append(result, "KWARGS")
				inKwargs = true
			}
			continue
		}

		if inKwargs {
			continue
		}

		cls := classified[i]
		switch cls {
		case classPlain:
			result = append(result, tokens[i])
		case classFlag:
			result = collapseAppend(result, "FLAG")
		case classPath:
			result = collapseAppend(result, "PATH")
		case classRepo:
			result = collapseAppend(result, "REPO")
		case classHash:
			result = collapseAppend(result, "HASH")
		case classNum:
			result = collapseAppend(result, "NUM")
		case classStr:
			result = collapseAppend(result, "STR")
		case classKwargs:
			// already handled above
		}
	}

	return result
}

// collapseAppend adds token to result, merging if the previous token is the same.
func collapseAppend(result []string, token string) []string {
	if n := len(result); n > 0 && result[n-1] == token {
		return result
	}
	return append(result, token)
}

func makeSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}
