package normalize

import "testing"

// Realistic command samples that approximate the kinds of inputs
// the daemon will see in practice. Keep this list in sync with
// representative workflows as the codebase grows.
var benchCommands = []string{
	"git status",
	"git diff --stat",
	"git add .",
	"git commit -m \"fix: handle empty input\"",
	"git push origin main",
	"git pull --rebase",
	"git checkout -b feature/foo",
	"git stash push -m wip",
	"git stash pop",
	"git log --oneline -n 20",
	"docker build -t myapp:latest .",
	"docker run --rm -p 8080:8080 myapp",
	"docker compose up -d --build",
	"docker logs -f --tail 100 myapp",
	"docker exec -it myapp /bin/bash",
	"kubectl get pods -n production -o wide",
	"kubectl logs -f deployment/app -n production",
	"kubectl describe pod app-7d4f8b-abcde",
	"kubectl exec -it app-pod -- /bin/bash",
	"kubectl rollout status deployment/app",
	"sudo apt update",
	"sudo apt install -y vim tmux htop",
	"cargo build --release",
	"cargo test -- --nocapture",
	"cargo clippy -- -D warnings",
	"npm install",
	"npm test -- --coverage",
	"npm run build",
	"go test ./...",
	"go build -o bin/hunch ./cmd/hunch",
	"ssh user@prod-server",
	"scp -r ./build user@host:/var/www/",
	"terraform plan -out=tfplan -var-file=prod.tfvars",
	"terraform apply tfplan",
	"ffmpeg -i input.mp4 -c:v libx264 output.mp4",
	"journalctl -u nginx -n 50 --no-pager",
	"tmux new -s dev",
	"tmux attach -t dev",
	"sudo time nice -n 19 make -j8 build",
}

func BenchmarkNormalize(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, cmd := range benchCommands {
			_ = Normalize(cmd, nil)
		}
	}
}

func BenchmarkTokenize(b *testing.B) {
	b.ReportAllocs()
	cmds := make([]string, len(benchCommands))
	copy(cmds, benchCommands)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, cmd := range cmds {
			_ = tokenize(cmd)
		}
	}
}

func BenchmarkClassifyTokens(b *testing.B) {
	b.ReportAllocs()
	tokens := tokenize("kubectl get pods -n production -o wide --field-selector=status.phase=Running")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyTokens(tokens, DefaultParents)
	}
}
