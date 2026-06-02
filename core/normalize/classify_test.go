package normalize

import (
	"reflect"
	"testing"
)

func TestClassifyTokens(t *testing.T) {
	tests := []struct {
		name    string
		tokens  []string
		parents []string
		want    []string
	}{
		// --- Basic patterns ---
		{
			name:   "mkdir foo -> mkdir STR",
			tokens: []string{"mkdir", "foo"},
			want:   []string{"mkdir", "STR"},
		},
		{
			name:   "mkdir foo bar -> mkdir STR",
			tokens: []string{"mkdir", "foo", "bar"},
			want:   []string{"mkdir", "STR"},
		},
		{
			name:   "git commit -m init -> git commit FLAG STR",
			tokens: []string{"git", "commit", "-m", "init"},
			want:   []string{"git", "commit", "FLAG", "STR"},
		},
		{
			name:   "cargo build -- --target x86_64 -> cargo build KWARGS",
			tokens: []string{"cargo", "build", "--", "--target", "x86_64"},
			want:   []string{"cargo", "build", "KWARGS"},
		},

		// --- FLAG classification ---
		{
			name:   "short flag -a",
			tokens: []string{"ls", "-a"},
			want:   []string{"ls", "FLAG"},
		},
		{
			name:   "combined short flags -la",
			tokens: []string{"ls", "-la"},
			want:   []string{"ls", "FLAG"},
		},
		{
			name:   "long flag --help",
			tokens: []string{"cmd", "--help"},
			want:   []string{"cmd", "FLAG"},
		},
		{
			name:   "long flag with value --output dir",
			tokens: []string{"cmd", "--output", "dir"},
			want:   []string{"cmd", "FLAG", "STR"},
		},
		{
			name:   "flag with equals --target=x86_64",
			tokens: []string{"cmd", "--target=x86_64"},
			want:   []string{"cmd", "FLAG"},
		},
		{
			name:   "multiple flags collapsed",
			tokens: []string{"cmd", "-v", "-v", "-v"},
			want:   []string{"cmd", "FLAG"},
		},
		{
			name:   "single dash is stdin placeholder",
			tokens: []string{"cat", "-"},
			want:   []string{"cat", "FLAG"},
		},

		// --- PATH classification ---
		{
			name:   "absolute path",
			tokens: []string{"ls", "/home/user/docs"},
			want:   []string{"ls", "PATH"},
		},
		{
			name:   "relative path with /",
			tokens: []string{"cp", "src/main.go", "dst/"},
			want:   []string{"cp", "PATH"},
		},
		{
			name:   "dot prefix",
			tokens: []string{"source", "./setup.sh"},
			want:   []string{"source", "PATH"},
		},
		{
			name:   "dot-dot path",
			tokens: []string{"cd", "../parent"},
			want:   []string{"cd", "PATH"},
		},
		{
			name:   "tilde path",
			tokens: []string{"ls", "~/projects"},
			want:   []string{"ls", "PATH"},
		},
		{
			name:   "tilde slash path",
			tokens: []string{"vim", "~/.bashrc"},
			want:   []string{"vim", "PATH"},
		},
		{
			name:   "dot path",
			tokens: []string{"ls", "."},
			want:   []string{"ls", "PATH"},
		},
		{
			name:   "dot file",
			tokens: []string{"cat", ".gitignore"},
			want:   []string{"cat", "PATH"},
		},
		{
			name:   "multiple paths collapsed",
			tokens: []string{"cp", "a.txt", "b.txt", "c.txt", "/dst"},
			want:   []string{"cp", "STR", "PATH"},
		},

		// --- REPO classification ---
		{
			name:   "https URL",
			tokens: []string{"git", "clone", "https://github.com/user/repo"},
			want:   []string{"git", "clone", "REPO"},
		},
		{
			name:   "https URL with .git",
			tokens: []string{"git", "clone", "https://github.com/user/repo.git"},
			want:   []string{"git", "clone", "REPO"},
		},
		{
			name:   "git SSH remote",
			tokens: []string{"git", "remote", "add", "origin", "git@github.com:user/repo.git"},
			want:   []string{"git", "remote", "STR", "REPO"},
		},
		{
			name:   "www URL",
			tokens: []string{"wget", "www.example.com/file.tar.gz"},
			want:   []string{"wget", "REPO"},
		},
		{
			name:   "ssh URL",
			tokens: []string{"scp", "ssh://user@host/path"},
			want:   []string{"scp", "REPO"},
		},
		{
			name:   "SCP remote path",
			tokens: []string{"scp", "file.txt", "user@host:/remote/path/"},
			want:   []string{"scp", "STR", "REPO"},
		},
		{
			name:   "curl URL",
			tokens: []string{"curl", "https://api.example.com/data"},
			want:   []string{"curl", "REPO"},
		},
		{
			name:   "s3 URL",
			tokens: []string{"aws", "s3", "ls", "s3://my-bucket/"},
			want:   []string{"aws", "s3", "STR", "REPO"},
		},
		{
			name:   "ftp URL",
			tokens: []string{"wget", "ftp://example.com/file"},
			want:   []string{"wget", "REPO"},
		},
		{
			name:   "git:// protocol",
			tokens: []string{"git", "clone", "git://example.com/repo.git"},
			want:   []string{"git", "clone", "REPO"},
		},

		// --- HASH classification ---
		{
			name:   "short git hash",
			tokens: []string{"git", "checkout", "6ff23d4"},
			want:   []string{"git", "checkout", "HASH"},
		},
		{
			name:   "full git hash",
			tokens: []string{"git", "cherry-pick", "6ff23d4e1fa57632e122e77577c736fbd3da9b28"},
			want:   []string{"git", "cherry-pick", "HASH"},
		},
		{
			name:   "6-char hash",
			tokens: []string{"echo", "abcdef"},
			want:   []string{"echo", "HASH"},
		},
		{
			name:   "mixed case hash",
			tokens: []string{"git", "log", "AbCdEf0"},
			want:   []string{"git", "log", "HASH"},
		},
		{
			name:   "non-hex word not a hash",
			tokens: []string{"echo", "hello"},
			want:   []string{"echo", "STR"},
		},
		{
			name:   "too long for hash (>40 chars)",
			tokens: []string{"echo", "6ff23d4e1fa57632e122e77577c736fbd3da9b28abcd12345"},
			want:   []string{"echo", "STR"},
		},

		// --- NUM classification ---
		{
			name:   "integer",
			tokens: []string{"kill", "1234"},
			want:   []string{"kill", "NUM"},
		},
		{
			name:   "negative integer",
			tokens: []string{"seq", "-5", "10"},
			want:   []string{"seq", "FLAG", "NUM"},
		},
		{
			name:   "float",
			tokens: []string{"brightnessctl", "set", "0.75"},
			want:   []string{"brightnessctl", "STR", "NUM"},
		},
		{
			name:   "decimal",
			tokens: []string{"sleep", "2.5"},
			want:   []string{"sleep", "NUM"},
		},
		{
			name:   "pid",
			tokens: []string{"kill", "-9", "54321"},
			want:   []string{"kill", "FLAG", "NUM"},
		},
		{
			name:   "6-digit pure number is NUM not HASH",
			tokens: []string{"kill", "123456"},
			want:   []string{"kill", "NUM"},
		},
		{
			name:   "7-digit pure number is NUM not HASH",
			tokens: []string{"echo", "1234567"},
			want:   []string{"echo", "NUM"},
		},
		{
			name:   "hex with 0x prefix is STR",
			tokens: []string{"echo", "0x1234"},
			want:   []string{"echo", "STR"},
		},
		{
			name:   "long SHA-256 is STR not HASH",
			tokens: []string{"echo", "6ff23d4e1fa57632e122e77577c736fbd3da9b28abcd12345abcd12345abcd"},
			want:   []string{"echo", "STR"},
		},
		{
			name:   "multiple nums collapsed",
			tokens: []string{"seq", "1", "100"},
			want:   []string{"seq", "NUM"},
		},
		{
			name:   "leading zeros",
			tokens: []string{"chmod", "0755"},
			want:   []string{"chmod", "NUM"},
		},
		{
			name:   "scientific notation not a num",
			tokens: []string{"echo", "1e10"},
			want:   []string{"echo", "STR"},
		},

		// --- STR fallback ---
		{
			name:   "plain args become STR",
			tokens: []string{"echo", "hello", "world"},
			want:   []string{"echo", "STR"},
		},
		{
			name:   "docker image name not STR",
			tokens: []string{"docker", "pull", "ubuntu:22.04"},
			want:   []string{"docker", "pull", "STR"},
		},

		// --- KWARGS (-- separator) ---
		{
			name:   "after dash-dash",
			tokens: []string{"cargo", "run", "--", "--release", "--verbose"},
			want:   []string{"cargo", "run", "KWARGS"},
		},
		{
			name:   "npm run with --",
			tokens: []string{"npm", "run", "test", "--", "--coverage"},
			want:   []string{"npm", "run", "STR", "KWARGS"},
		},
		{
			name:   "go test with --",
			tokens: []string{"go", "test", "--", "-count=1", "./..."},
			want:   []string{"go", "test", "KWARGS"},
		},
		{
			name:   "ssh with -- for command",
			tokens: []string{"ssh", "host", "--", "ls", "-la"},
			want:   []string{"ssh", "STR", "KWARGS"},
		},
		{
			name:   "kubectl with --",
			tokens: []string{"kubectl", "exec", "-it", "pod", "--", "/bin/bash"},
			want:   []string{"kubectl", "exec", "FLAG", "STR", "KWARGS"},
		},

		// --- Known parent commands (subcommand kept) ---
		{
			name:    "git push (args collapse to STR)",
			tokens:  []string{"git", "push", "origin", "main"},
			parents: []string{"git"},
			want:    []string{"git", "push", "STR"},
		},
		{
			name:    "docker build",
			tokens:  []string{"docker", "build", "-t", "myimage", "."},
			parents: []string{"docker"},
			want:    []string{"docker", "build", "FLAG", "STR", "PATH"},
		},
		{
			name:    "kubectl get pods",
			tokens:  []string{"kubectl", "get", "pods", "-n", "default"},
			parents: []string{"kubectl"},
			want:    []string{"kubectl", "get", "STR", "FLAG", "STR"},
		},
		{
			name:    "npm install express",
			tokens:  []string{"npm", "install", "express"},
			parents: []string{"npm"},
			want:    []string{"npm", "install", "STR"},
		},
		{
			name:    "apt install vim",
			tokens:  []string{"apt", "install", "vim"},
			parents: []string{"apt"},
			want:    []string{"apt", "install", "STR"},
		},
		{
			name:    "systemctl restart nginx",
			tokens:  []string{"systemctl", "restart", "nginx"},
			parents: []string{"systemctl"},
			want:    []string{"systemctl", "restart", "STR"},
		},
		{
			name:    "cargo check",
			tokens:  []string{"cargo", "check", "--all-features"},
			parents: []string{"cargo"},
			want:    []string{"cargo", "check", "FLAG"},
		},
		{
			name:    "brew install package",
			tokens:  []string{"brew", "install", "ripgrep"},
			parents: []string{"brew"},
			want:    []string{"brew", "install", "STR"},
		},
		{
			name:    "terraform apply",
			tokens:  []string{"terraform", "apply", "-auto-approve"},
			parents: []string{"terraform"},
			want:    []string{"terraform", "apply", "FLAG"},
		},
		{
			name:    "gcloud compute instances list",
			tokens:  []string{"gcloud", "compute", "instances", "list"},
			parents: []string{"gcloud"},
			want:    []string{"gcloud", "compute", "STR"},
		},
		{
			name:    "docker compose up",
			tokens:  []string{"docker", "compose", "up", "-d"},
			parents: []string{"docker"},
			want:    []string{"docker", "compose", "STR", "FLAG"},
		},
		{
			name:    "gh pr create",
			tokens:  []string{"gh", "pr", "create", "--title", "feat"},
			parents: []string{"gh"},
			want:    []string{"gh", "pr", "STR", "FLAG", "STR"},
		},
		{
			name:    "go test",
			tokens:  []string{"go", "test", "-v", "./..."},
			parents: []string{"go"},
			want:    []string{"go", "test", "FLAG", "PATH"},
		},

		// --- Commands not in parent list (subcommand NOT preserved) ---
		{
			name:   "mkdir with flags",
			tokens: []string{"mkdir", "-p", "foo/bar"},
			want:   []string{"mkdir", "FLAG", "PATH"},
		},
		{
			name:   "cp without parent",
			tokens: []string{"cp", "-r", "src", "/dst"},
			want:   []string{"cp", "FLAG", "STR", "PATH"},
		},
		{
			name:   "mv without parent",
			tokens: []string{"mv", "file.txt", "/tmp/"},
			want:   []string{"mv", "STR", "PATH"},
		},

		// --- Collapse behavior ---
		{
			name:   "flags collapse consecutively",
			tokens: []string{"cmd", "-v", "--verbose", "--debug"},
			want:   []string{"cmd", "FLAG"},
		},
		{
			name:   "STR args collapse consecutively",
			tokens: []string{"grep", "-r", "pattern", "-l", "file"},
			want:   []string{"grep", "FLAG", "STR", "FLAG", "STR"},
		},
		{
			name:   "mix: flag path flag path",
			tokens: []string{"cmd", "-i", "input.txt", "-o", "output.txt"},
			want:   []string{"cmd", "FLAG", "STR", "FLAG", "STR"},
		},

		// --- Multi-tool scenarios ---
		{
			name:    "git checkout -b feature-branch",
			tokens:  []string{"git", "checkout", "-b", "feature-branch"},
			parents: []string{"git"},
			want:    []string{"git", "checkout", "FLAG", "STR"},
		},
		{
			name:    "git log --oneline -n 10",
			tokens:  []string{"git", "log", "--oneline", "-n", "10"},
			parents: []string{"git"},
			want:    []string{"git", "log", "FLAG", "NUM"},
		},
		{
			name:    "docker run --rm -it ubuntu bash",
			tokens:  []string{"docker", "run", "--rm", "-it", "ubuntu", "bash"},
			parents: []string{"docker"},
			want:    []string{"docker", "run", "FLAG", "STR"},
		},
		{
			name:    "kubectl apply -f deployment.yaml",
			tokens:  []string{"kubectl", "apply", "-f", "deployment.yaml"},
			parents: []string{"kubectl"},
			want:    []string{"kubectl", "apply", "FLAG", "STR"},
		},
		{
			name:    "kubectl describe pod nginx-abc123 -n prod",
			tokens:  []string{"kubectl", "describe", "pod", "nginx-abc123", "-n", "prod"},
			parents: []string{"kubectl"},
			want:    []string{"kubectl", "describe", "STR", "FLAG", "STR"},
		},
		{
			name:    "curl -X POST https://api.example.com -d data",
			tokens:  []string{"curl", "-X", "POST", "https://api.example.com", "-d", "data"},
			parents: []string{"curl"},
			want:    []string{"curl", "FLAG", "STR", "REPO", "FLAG", "STR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parents := tt.parents
			if parents == nil {
				parents = DefaultParents
			}
			got := classifyTokens(tt.tokens, parents)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("classifyTokens(%v, parents) = %v, want %v", tt.tokens, got, tt.want)
			}
		})
	}
}

func TestClassifyTokensEmpty(t *testing.T) {
	got := classifyTokens(nil, DefaultParents)
	if got != nil {
		t.Errorf("classifyTokens(nil) = %v, want nil", got)
	}
	got = classifyTokens([]string{}, DefaultParents)
	if got != nil {
		t.Errorf("classifyTokens([]) = %v, want nil", got)
	}
}
