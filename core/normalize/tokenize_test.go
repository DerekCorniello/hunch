package normalize

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple",
			input: "ls -la /home",
			want:  []string{"ls", "-la", "/home"},
		},
		{
			name:  "no args",
			input: "pwd",
			want:  []string{"pwd"},
		},
		{
			name:  "double quoted string",
			input: `echo "hello world"`,
			want:  []string{"echo", "hello world"},
		},
		{
			name:  "single quoted string",
			input: `echo 'hello world'`,
			want:  []string{"echo", "hello world"},
		},
		{
			name:  "mixed quotes",
			input: `git commit -m "fix: update 'foo' bar"`,
			want:  []string{"git", "commit", "-m", "fix: update 'foo' bar"},
		},
		{
			name:  "escaped double quote",
			input: `echo "he said \"hello\""`,
			want:  []string{"echo", `he said "hello"`},
		},
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  nil,
		},
		{
			name:  "multiple spaces",
			input: "ls   -la    /home",
			want:  []string{"ls", "-la", "/home"},
		},
		{
			name:  "tabs",
			input: "ls\t-la\t/home",
			want:  []string{"ls", "-la", "/home"},
		},
		{
			name:  "command with equals",
			input: `cargo build --release --target=x86_64`,
			want:  []string{"cargo", "build", "--release", "--target=x86_64"},
		},
		{
			name:  "env assignments",
			input: `VAR=val command arg`,
			want:  []string{"VAR=val", "command", "arg"},
		},
		{
			name:  "path with tilde",
			input: `ls ~/projects`,
			want:  []string{"ls", "~/projects"},
		},
		{
			name:  "git remote URL",
			input: `git clone https://github.com/user/repo.git`,
			want:  []string{"git", "clone", "https://github.com/user/repo.git"},
		},
		{
			name:  "git ssh remote",
			input: `git push git@github.com:user/repo.git main`,
			want:  []string{"git", "push", "git@github.com:user/repo.git", "main"},
		},
		{
			name:  "backslash escape space outside quotes",
			input: `echo foo\ bar`,
			want:  []string{"echo", "foo bar"},
		},
		{
			name:  "backslash escape inside single quotes is literal",
			input: `echo 'foo\ bar'`,
			want:  []string{"echo", "foo\\ bar"},
		},
		{
			name:  "backslash at end of input outside quotes",
			input: `echo foo\`,
			want:  []string{"echo", "foo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenize(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenize(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
