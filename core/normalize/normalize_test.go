package normalize

import (
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		parents []string
		want    string
	}{
		// --- Examples from README/AGENTS.md ---
		{
			name: "mkdir foo",
			raw:  "mkdir foo",
			want: "mkdir STR",
		},
		{
			name: "mkdir foo bar baz",
			raw:  "mkdir foo bar baz",
			want: "mkdir STR",
		},
		{
			name: "git commit -m init",
			raw:  `git commit -m "init"`,
			want: "git commit FLAG STR",
		},
		{
			name: "cargo build -- --target x86_64",
			raw:  "cargo build -- --target x86_64",
			want: "cargo build KWARGS",
		},

		// --- Full pipeline integration tests ---
		{
			name: "git clone repo",
			raw:  "git clone https://github.com/user/project.git",
			want: "git clone REPO",
		},
		{
			name: "cd into project",
			raw:  "cd project",
			want: "cd STR",
		},
		{
			name: "cargo build with flags",
			raw:  "cargo build --release --target x86_64-unknown-linux-gnu",
			want: "cargo build FLAG STR",
		},
		{
			name: "cargo test",
			raw:  "cargo test -- --nocapture --test-threads 4",
			want: "cargo test KWARGS",
		},

		// --- Docker workflows ---
		{
			name: "docker build",
			raw:  "docker build -t myapp:latest .",
			want: "docker build FLAG STR PATH",
		},
		{
			name: "docker run",
			raw:  "docker run --rm -it -p 8080:8080 myapp",
			want: "docker run FLAG STR",
		},
		{
			name: "docker compose up",
			raw:  "docker compose up -d --build",
			want: "docker compose STR FLAG",
		},
		{
			name: "docker logs",
			raw:  "docker logs -f --tail 100 container-name",
			want: "docker logs FLAG NUM STR",
		},

		// --- Git workflows ---
		{
			name: "git add",
			raw:  "git add .",
			want: "git add PATH",
		},
		{
			name: "git status",
			raw:  "git status",
			want: "git status",
		},
		{
			name: "git push",
			raw:  "git push origin main",
			want: "git push STR",
		},
		{
			name: "git pull",
			raw:  "git pull --rebase origin main",
			want: "git pull FLAG STR",
		},
		{
			name: "git stash",
			raw:  "git stash push -m wip",
			want: "git stash STR FLAG STR",
		},

		// --- Systemctl workflows ---
		{
			name: "systemctl start",
			raw:  "systemctl start nginx.service",
			want: "systemctl start STR",
		},
		{
			name: "systemctl status",
			raw:  "systemctl status sshd",
			want: "systemctl status STR",
		},

		// --- Package manager workflows ---
		{
			name: "apt update",
			raw:  "sudo apt update",
			want: "apt update",
		},
		{
			name: "apt install",
			raw:  "sudo apt install -y vim tmux htop",
			want: "apt install FLAG STR",
		},
		{
			name: "npm init",
			raw:  "npm init -y",
			want: "npm init FLAG",
		},
		{
			name: "npm test",
			raw:  "npm run test -- --coverage",
			want: "npm run STR KWARGS",
		},
		{
			name: "pip install",
			raw:  "pip install --upgrade pip setuptools",
			want: "pip install FLAG STR",
		},
		{
			name: "brew search",
			raw:  "brew search python",
			want: "brew search STR",
		},

		// --- Sudo + known parent combos ---
		{
			name: "sudo git push",
			raw:  "sudo git push origin main",
			want: "git push STR",
		},
		{
			name: "sudo systemctl restart",
			raw:  "sudo systemctl restart nginx",
			want: "systemctl restart STR",
		},
		{
			name: "sudo docker run",
			raw:  "sudo docker run -d --name web nginx",
			want: "docker run FLAG STR",
		},
		{
			name: "sudo npm install -g",
			raw:  "sudo npm install -g typescript",
			want: "npm install FLAG STR",
		},

		// --- Kubectl workflows ---
		{
			name: "kubectl get",
			raw:  "kubectl get pods -n default -o wide",
			want: "kubectl get STR FLAG STR FLAG STR",
		},
		{
			name: "kubectl exec",
			raw:  "kubectl exec -it pod-name -- /bin/bash",
			want: "kubectl exec FLAG STR KWARGS",
		},
		{
			name: "kubectl logs",
			raw:  "kubectl logs -f deployment/app -n production --tail 50",
			want: "kubectl logs FLAG PATH FLAG STR FLAG NUM",
		},

		// --- File operations ---
		{
			name: "ls",
			raw:  "ls -la /var/log",
			want: "ls FLAG PATH",
		},
		{
			name: "cp",
			raw:  "cp -r src/ /backup/dst/",
			want: "cp FLAG PATH",
		},
		{
			name: "rm",
			raw:  "rm -rf ./node_modules /tmp/cache",
			want: "rm FLAG PATH",
		},
		{
			name: "find",
			raw:  "find . -name '*.go' -type f",
			want: "find PATH FLAG STR FLAG STR",
		},
		{
			name: "grep",
			raw:  `grep -rn "TODO" ./src/`,
			want: "grep FLAG STR PATH",
		},

		// --- Python / Node workflows ---
		{
			name: "python script",
			raw:  "python train.py --epochs 100 --batch-size 32",
			want: "python STR FLAG NUM FLAG NUM",
		},
		{
			name: "node server",
			raw:  "node server.js --port 3000",
			want: "node STR FLAG NUM",
		},
		{
			name: "pipenv install",
			raw:  "pipenv install --dev pytest black",
			want: "pipenv install FLAG STR",
		},
		{
			name: "go run",
			raw:  "go run ./cmd/server --config config.yaml",
			want: "go run PATH FLAG STR",
		},

		// --- SSH / remote workflows ---
		{
			name: "ssh to host",
			raw:  "ssh user@prod-server",
			want: "ssh STR",
		},
		{
			name: "scp file",
			raw:  "scp -r ./build user@host:/var/www/",
			want: "scp FLAG PATH REPO",
		},
		{
			name: "rsync",
			raw:  "rsync -avz --progress ./src/ user@host:/dst/",
			want: "rsync FLAG PATH REPO",
		},

		// --- Nested wrappers + known parents ---
		{
			name: "sudo time git push",
			raw:  "sudo time git push origin main",
			want: "git push STR",
		},
		{
			name: "sudo env DEBUG=1 npm test",
			raw:  "sudo env DEBUG=1 npm test",
			want: "npm test",
		},

		// --- Media tools ---
		{
			name: "ffmpeg convert",
			raw:  "ffmpeg -i input.mp4 -c:v libx264 output.mp4",
			want: "ffmpeg FLAG STR FLAG STR",
		},

		// --- Cloud tools ---
		{
			name: "aws s3 cp",
			raw:  "aws s3 cp ./file.txt s3://mybucket/",
			want: "aws s3 STR PATH REPO",
		},
		{
			name: "terraform plan",
			raw:  "terraform plan -out=tfplan -var-file=prod.tfvars",
			want: "terraform plan FLAG",
		},

		// --- Pipes (|) — each segment normalized independently ---
		{
			name: "pipe separates segments",
			raw:  "git log | grep fix",
			want: "git log | grep STR",
		},
		{
			name: "multi-pipe",
			raw:  "npm test | tap-json | grep fail",
			want: "npm test | tap-json | grep STR",
		},
		{
			name: "pipe with flags",
			raw:  "grep -r TODO ./src | xargs rm",
			want: "grep FLAG STR PATH | xargs STR",
		},

		// --- Redirects — non-operator symbols collapse normally ---
		{
			name: "redirect stdout",
			raw:  "echo hello > /dev/null",
			want: "echo STR PATH",
		},
		{
			name: "redirect stderr",
			raw:  "cargo build 2>&1",
			want: "cargo build STR",
		},
		{
			name: "redirect append",
			raw:  "date >> log.txt",
			want: "date STR",
		},
		{
			name: "redirect with pipe",
			raw:  "make 2>&1 | tee build.log",
			want: "make STR | tee STR",
		},

		// --- Heredocs — multi-line input is joined as one token ---
		{
			name: "heredoc",
			raw:  "cat <<EOF\nhello\nEOF",
			want: "cat STR",
		},
		{
			name: "heredoc with pipe",
			raw:  "cat <<'PY' | python\nimport os\nPY",
			want: "cat STR | python STR",
		},

		// --- Bare env prefix unwrapping ---
		{
			name: "bare env assignment",
			raw:  "FOO=bar make",
			want: "make",
		},
		{
			name: "multiple env assignments",
			raw:  "FOO=bar BAZ=qux make",
			want: "make",
		},
		{
			name: "env assignment with no command",
			raw:  "FOO=bar",
			want: "FOO=bar",
		},

		// --- Compound operators ---
		{
			name: "and-operator",
			raw:  "make && make install",
			want: "make && make STR",
		},
		{
			name: "or-operator pipe",
			raw:  "make || echo fail",
			want: "make || echo STR",
		},

		// --- Edge cases ---
		{
			name: "empty string",
			raw:  "",
			want: "",
		},
		{
			name: "whitespace only",
			raw:  "   ",
			want: "",
		},
		{
			name: "single command",
			raw:  "pwd",
			want: "pwd",
		},
		{
			name: "command with = in arg",
			raw:  "kubectl get pods --field-selector=status.phase=Running",
			want: "kubectl get STR FLAG",
		},
		{
			name: "6-digit pid is NUM",
			raw:  "kill 123456",
			want: "kill NUM",
		},
		{
			name: "sudo dash unwraps to inner command",
			raw:  "sudo - ls -la",
			want: "ls FLAG",
		},
		{
			name: "nice dash unwraps to inner command",
			raw:  "nice - make",
			want: "make",
		},
		{
			name: "flock stdin lock file",
			raw:  "flock - cat file",
			want: "cat STR",
		},
		{
			name: "node script after parent removal",
			raw:  `node --eval "console.log('hi')"`,
			want: "node FLAG STR",
		},
		{
			name: "node inspect script",
			raw:  "node --inspect server.js",
			want: "node FLAG STR",
		},
		{
			name: "npx package (not a parent)",
			raw:  "npx create-react-app my-app",
			want: "npx STR",
		},
		{
			name: "bunx package (not a parent)",
			raw:  "bunx eslint src/",
			want: "bunx STR PATH",
		},
		{
			name: "cmake source dir (not a parent)",
			raw:  "cmake ..",
			want: "cmake PATH",
		},
		{
			name: "cmake build dir (not a parent)",
			raw:  "cmake -S . -B build",
			want: "cmake FLAG PATH FLAG STR",
		},
		{
			name: "short flag",
			raw:  "mycli -A",
			want: "mycli FLAG",
		},
		{
			name: "long flag no value",
			raw:  "mycli --allow",
			want: "mycli FLAG",
		},
		{
			name: "long flag with equals value",
			raw:  `mycli --allow-only="Derek"`,
			want: "mycli FLAG",
		},
		{
			name: "mixed flags and positional args",
			raw:  "mycli -A --allow --allow-only=val arg1 arg2",
			want: "mycli FLAG STR",
		},

		// --- Go specific workflows ---
		{
			name: "go mod tidy",
			raw:  "go mod tidy",
			want: "go mod STR",
		},
		{
			name: "go build",
			raw:  "go build -o bin/hunch ./cmd/hunch",
			want: "go build FLAG PATH",
		},
		{
			name: "go vet",
			raw:  "go vet ./...",
			want: "go vet PATH",
		},
		{
			name: "golangci-lint run",
			raw:  "golangci-lint run ./...",
			want: "golangci-lint STR PATH",
		},

		// --- Rust/Cargo workflows ---
		{
			name: "cargo fmt",
			raw:  "cargo fmt -- --check",
			want: "cargo fmt KWARGS",
		},
		{
			name: "cargo clippy",
			raw:  "cargo clippy -- -D warnings",
			want: "cargo clippy KWARGS",
		},

		// --- tmux sessions ---
		{
			name: "tmux new session",
			raw:  "tmux new -s dev",
			want: "tmux new FLAG STR",
		},
		{
			name: "tmux attach",
			raw:  "tmux attach -t dev",
			want: "tmux attach FLAG STR",
		},

		// --- jq / yq ---
		{
			name: "jq filter",
			raw:  "jq '.items[] | select(.enabled)' data.json",
			want: "jq PATH STR",
		},
		{
			name: "jq with -r",
			raw:  "jq -r '.name' file.json",
			want: "jq FLAG PATH STR",
		},

		// --- Journalctl ---
		{
			name: "journalctl logs",
			raw:  "journalctl -u nginx -n 50 --no-pager",
			want: "journalctl FLAG STR FLAG NUM FLAG",
		},
		{
			name: "journalctl follow",
			raw:  "sudo journalctl -f -u sshd",
			want: "journalctl FLAG STR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parents := tt.parents
			if parents == nil {
				parents = DefaultParents
			}
			got := Normalize(tt.raw, parents)
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNormalizeCustomParents(t *testing.T) {
	customParents := []string{"git", "mycli"}

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "git with custom parents",
			raw:  "git push origin main",
			want: "git push STR",
		},
		{
			name: "mycli with custom parents",
			raw:  "mycli deploy -e production",
			want: "mycli deploy FLAG STR",
		},
		{
			name: "docker not in custom parents (subcommand NOT kept)",
			raw:  "docker run -it ubuntu",
			want: "docker STR FLAG STR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.raw, customParents)
			if got != tt.want {
				t.Errorf("Normalize(%q, customParents) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNormalizeNilParents(t *testing.T) {
	got := Normalize("git push origin main", nil)
	want := "git push STR"
	if got != want {
		t.Errorf("Normalize with nil parents = %q, want %q", got, want)
	}
}

func TestNormalizeWorkflows(t *testing.T) {
	tests := []struct {
		name     string
		workflow []string
		want     []string
	}{
		{
			name: "GitHub clone to PR workflow",
			workflow: []string{
				"git clone https://github.com/user/repo.git",
				"cd repo",
				"git checkout -b feature-branch",
				"vim src/main.go",
				"git add src/main.go",
				"git commit -m 'feat: add feature'",
				"git push origin feature-branch",
				"gh pr create --title 'Add feature'",
			},
			want: []string{
				"git clone REPO",
				"cd STR",
				"git checkout FLAG STR",
				"vim PATH",
				"git add PATH",
				"git commit FLAG STR",
				"git push STR",
				"gh pr STR FLAG STR",
			},
		},
		{
			name: "Docker dev workflow",
			workflow: []string{
				"vim Dockerfile",
				"docker build -t myapp:latest .",
				"docker run --rm -p 8080:8080 myapp",
				"docker logs -f myapp",
			},
			want: []string{
				"vim STR",
				"docker build FLAG STR PATH",
				"docker run FLAG STR",
				"docker logs FLAG STR",
			},
		},
		{
			name: "Kubernetes deploy workflow",
			workflow: []string{
				"kubectl apply -f deployment.yaml",
				"kubectl get pods -n production",
				"kubectl logs -f deployment/app",
				"kubectl describe pod app-7d4f8b-abcde",
				"kubectl rollout status deployment/app",
			},
			want: []string{
				"kubectl apply FLAG STR",
				"kubectl get STR FLAG STR",
				"kubectl logs FLAG PATH",
				"kubectl describe STR",
				"kubectl rollout STR PATH",
			},
		},
		{
			name: "Terraform workflow",
			workflow: []string{
				"terraform init",
				"terraform plan -out=tfplan",
				"terraform apply tfplan",
				"terraform destroy -auto-approve",
			},
			want: []string{
				"terraform init",
				"terraform plan FLAG",
				"terraform apply STR",
				"terraform destroy FLAG",
			},
		},
		{
			name: "System administration workflow",
			workflow: []string{
				"sudo systemctl status nginx",
				"tail -f /var/log/nginx/access.log",
				"sudo journalctl -u nginx -f",
				"sudo systemctl restart nginx",
				"curl -I https://example.com",
			},
			want: []string{
				"systemctl status STR",
				"tail FLAG PATH",
				"journalctl FLAG STR FLAG",
				"systemctl restart STR",
				"curl FLAG REPO",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, cmd := range tt.workflow {
				got := Normalize(cmd, nil)
				want := tt.want[i]
				if got != want {
					t.Errorf("workflow step %d: Normalize(%q) = %q, want %q", i, cmd, got, want)
				}
			}
		})
	}
}
