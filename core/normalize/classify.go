package normalize

import (
	"regexp"
	"strings"
	"sync"
)

// DefaultParents is the default set of known parent commands whose
// immediate subcommand (second token) is kept verbatim during normalization.
//
// A tool belongs here only when its first non-flag argument is typically
// a verb that distinguishes workflows (git push vs git pull, docker
// run vs docker build, kubectl get vs kubectl describe). Tools where the
// first argument is usually a file, URL, filter, or patch are excluded —
// for them, the first argument is classified like any other token and
// collapsed into STR / PATH / REPO as appropriate.
var DefaultParents = []string{
	// Version control — verbs like commit, push, pull, checkout, merge
	"git", "hg", "svn", "pijul", "fossil", "jj",

	// Package managers & build tools
	// npx, bunx, pnpx are intentionally excluded: their first
	// argument is a package name, not a subcommand verb.
	"cargo", "npm", "yarn", "pnpm", "yarnpkg",
	"pip", "pip3", "pipenv", "poetry", "uv",
	"go", "rustup",
	"mvn", "gradle", "sbt", "ant",
	"dotnet", "nuget",
	"brew", "apt", "apt-get", "apt-cache", "dpkg",
	"dnf", "yum", "rpm",
	"pacman", "pamac", "yay", "paru",
	"snap", "flatpak",
	"choco", "scoop", "winget",
	"apk", "zypper", "xbps", "emerge",
	"corepack",

	// Build systems
	// cmake is intentionally excluded: its first argument is
	// typically a source/build directory (cmake ..) or a flag
	// (--build, -E), not a subcommand verb.
	"bazel", "bazelisk", "meson", "please",

	// Nix-family
	"nix", "nix-env", "guix",

	// Version managers
	"mise", "rtx", "asdf",
	"fnm", "volta", "n",
	"pyenv", "rbenv", "jenv", "nodenv", "goenv", "tfenv",

	// Container & orchestration
	"docker", "podman", "lxc", "lxd", "incus",
	"kubectl", "kubectx", "kubens", "helm",
	"istioctl", "linkerd",
	"k9s", "stern", "kustomize", "argocd", "flux",
	"kind", "k3d", "minikube",
	"skaffold", "tilt",
	"buildah", "skopeo", "crane",

	// Cloud & infrastructure
	"aws", "gcloud", "az", "heroku", "flyctl", "doctl",
	"oci", "ibmcloud", "hcloud", "vultr", "scaleway",
	"terraform", "tofu", "packer", "waypoint",
	"pulumi", "bicep",
	"ansible", "ansible-playbook",
	"vault", "consul", "nomad", "boundary",

	// System management (systemd, network, bluetooth)
	"systemctl", "journalctl", "loginctl", "timedatectl",
	"hostnamectl", "localectl", "networkctl", "resolvectl",
	"bluetoothctl",

	// Terminal multiplexers
	"tmux", "screen", "zellij", "tmuxp",

	// GitHub / GitLab / Bitwarden CLIs
	"gh", "glab", "bw",

	// Security
	"pass", "gpg", "ssh-keygen", "openssl",
	"trivy", "op", "cosign", "sops",

	// Observability
	"amtool", "promtool", "logcli", "vector",

	// Mobile
	"adb", "fastboot",
	"expo", "flutter", "dart", "react-native",
	"cordova", "capacitor", "ionic",

	// Desktop / cross-platform
	"electron", "electron-builder", "electron-forge",

	// JS/TS runtimes — deno and bun have first-class subcommand structures
	// (run, test, fmt, install). node is intentionally excluded because
	// its first argument is typically a script file rather than a verb;
	// treating it as a script runner (like python) yields cleaner templates.
	"deno", "bun",

	// Modern JS/TS tooling
	"vite", "webpack", "rollup", "parcel", "esbuild",
	"nx", "turbo", "lerna", "rush",
	"biome",
	"vitest", "jest", "mocha", "playwright", "cypress",
	"storybook",

	// Database CLIs
	"sqlite3", "mysql", "psql", "mongosh", "redis-cli",

	// Backup / sync
	"rclone", "restic", "kopia",

	// Scheduling
	"crontab", "at", "systemd-analyze",
}

// Token class patterns.
var (
	flagRe = regexp.MustCompile(`^-{1,2}`)
	repoRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*://|www\.)|@.*:|\.git$`)
	hashRe = regexp.MustCompile(`^[0-9a-fA-F]{6,40}$`)
	numRe  = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
)

// defaultParentSet is the precomputed lookup for DefaultParents, avoiding
// a per-call map allocation on the hot path.
var defaultParentSet = makeSet(DefaultParents)

// classifiedPool reduces allocations on repeated classifyToken calls.
var classifiedPool = sync.Pool{
	New: func() any { return make([]classification, 0, 32) },
}

// classifyTokens runs Phase 2 token-type classification and collapses
// consecutive tokens of the same type.
func classifyTokens(tokens []string, parents []string) []string {
	if len(tokens) == 0 {
		return nil
	}

	// Reuse the precomputed set when the caller passed DefaultParents
	// (the common case), avoiding a per-call map allocation.
	// We compare by pointer identity: the Normalize function assigns
	// DefaultParents when parents is nil, so this catches the common
	// path without allocation.
	var parentSet map[string]struct{}
	if len(parents) == len(DefaultParents) && len(parents) > 0 && &parents[0] == &DefaultParents[0] {
		parentSet = defaultParentSet
	} else {
		parentSet = makeSet(parents)
	}

	// Phase 2a: classify each token individually.
	n := len(tokens)
	pooled := classifiedPool.Get().([]classification)
	if cap(pooled) < n {
		pooled = make([]classification, n)
	} else {
		pooled = pooled[:n]
	}
	firstTok := tokens[0]
	for i, tok := range tokens {
		pooled[i] = classifyToken(tok, i, firstTok, parentSet)
	}

	// Phase 2b: collapse consecutive same-type tokens.
	result := collapseTokens(tokens, pooled)
	pooled = pooled[:0]
	classifiedPool.Put(pooled)
	return result
}

type classification int

const (
	classPlain classification = iota // kept verbatim
	classFlag                        // collapsed to FLAG
	classPath                        // collapsed to PATH
	classRepo                        // collapsed to REPO
	classHash                        // collapsed to HASH
	classNum                         // collapsed to NUM
	classStr                         // collapsed to STR
)

// classifyToken determines the classification for a single token.
// firstTok is the command's leading token, supplied separately so
// this function does not need the full tokens slice.
func classifyToken(tok string, idx int, firstTok string, parents map[string]struct{}) classification {
	if tok == "--" {
		return classPlain
	}

	if flagRe.MatchString(tok) {
		return classFlag
	}

	if idx == 0 {
		return classPlain
	}

	if idx == 1 {
		if _, ok := parents[firstTok]; ok {
			return classPlain
		}
	}

	// URL or git remote — check before path so URLs with slashes
	// are caught as REPO rather than PATH.
	if repoRe.MatchString(tok) {
		return classRepo
	}

	// Check NUM before HASH: a 6+ digit pure number (e.g. a PID)
	// matches hashRe too, but is far more likely to be a number.
	if numRe.MatchString(tok) {
		return classNum
	}

	if hashRe.MatchString(tok) {
		return classHash
	}

	if strings.ContainsRune(tok, '/') || (len(tok) > 0 && (tok[0] == '.' || tok[0] == '~')) {
		return classPath
	}

	return classStr
}

// collapseTokens merges consecutive tokens of the same classification,
// keeping one representative token. The `--` separator is handled
// inline: everything from `--` onward collapses to a single KWARGS.
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

		switch classified[i] {
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
		}
	}

	return result
}

func collapseAppend(result []string, token string) []string {
	if n := len(result); n > 0 && result[n-1] == token {
		return result
	}
	return append(result, token)
}

func makeSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}
