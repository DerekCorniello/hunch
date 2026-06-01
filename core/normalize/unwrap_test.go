package normalize

import (
	"reflect"
	"testing"
)

func TestUnwrapPrefixTokens(t *testing.T) {
	tests := []struct {
		name   string
		tokens []string
		want   []string
	}{
		// --- Simple wrappers ---
		{name: "sudo simple", tokens: []string{"sudo", "apt", "update"}, want: []string{"apt", "update"}},
		{name: "time simple", tokens: []string{"time", "make", "build"}, want: []string{"make", "build"}},
		{name: "nohup simple", tokens: []string{"nohup", "python", "script.py"}, want: []string{"python", "script.py"}},
		{name: "exec simple", tokens: []string{"exec", "bash"}, want: []string{"bash"}},
		{name: "doas simple", tokens: []string{"doas", "pacman", "-Syu"}, want: []string{"pacman", "-Syu"}},
		{name: "fakeroot simple", tokens: []string{"fakeroot", "dpkg-deb", "--build"}, want: []string{"dpkg-deb", "--build"}},
		{name: "stdbuf simple", tokens: []string{"stdbuf", "-oL", "tail", "-f", "log"}, want: []string{"tail", "-f", "log"}},
		{name: "pkexec simple", tokens: []string{"pkexec", "gedit", "/etc/fstab"}, want: []string{"gedit", "/etc/fstab"}},

		// --- sudo with flags ---
		{name: "sudo -u", tokens: []string{"sudo", "-u", "root", "apt", "install", "vim"}, want: []string{"apt", "install", "vim"}},
		{name: "sudo --user", tokens: []string{"sudo", "--user", "postgres", "psql"}, want: []string{"psql"}},
		{name: "sudo -E -H", tokens: []string{"sudo", "-E", "-H", "pip", "install", "requests"}, want: []string{"pip", "install", "requests"}},
		{name: "sudo -u nobody -g nogroup", tokens: []string{"sudo", "-u", "nobody", "-g", "nogroup", "id"}, want: []string{"id"}},
		{name: "sudo -p prompt", tokens: []string{"sudo", "-p", "Password:", "whoami"}, want: []string{"whoami"}},
		{name: "doas -u", tokens: []string{"doas", "-u", "www", "nginx", "-t"}, want: []string{"nginx", "-t"}},
		{name: "sudo with env assignment", tokens: []string{"sudo", "EDITOR=vim", "visudo"}, want: []string{"visudo"}},

		// --- env ---
		{name: "env one var", tokens: []string{"env", "FOO=bar", "command"}, want: []string{"command"}},
		{name: "env multiple vars", tokens: []string{"env", "FOO=bar", "BAZ=qux", "command", "arg"}, want: []string{"command", "arg"}},
		{name: "env with --", tokens: []string{"env", "FOO=bar", "--", "command", "-flag"}, want: []string{"command", "-flag"}},
		{name: "env no assignments", tokens: []string{"env", "command"}, want: []string{"command"}},

		// --- nice / ionice ---
		{name: "nice default", tokens: []string{"nice", "make"}, want: []string{"make"}},
		{name: "nice with adjustment", tokens: []string{"nice", "-n", "10", "make"}, want: []string{"make"}},
		{name: "ionice with class", tokens: []string{"ionice", "-c", "3", "dd", "if=/dev/zero", "of=test"}, want: []string{"dd", "if=/dev/zero", "of=test"}},

		// --- chroot ---
		{name: "chroot simple", tokens: []string{"chroot", "/mnt/newroot", "/bin/bash"}, want: []string{"/bin/bash"}},
		{name: "chroot with userspec", tokens: []string{"chroot", "--userspec", "1000:1000", "/path", "cmd"}, want: []string{"cmd"}},

		// --- taskset / numactl / prlimit / unshare ---
		{name: "taskset CPU affinity", tokens: []string{"taskset", "-c", "0-3", "stress-ng", "--cpu", "4"}, want: []string{"stress-ng", "--cpu", "4"}},
		{name: "numactl node bind", tokens: []string{"numactl", "--membind", "1", "--cpunodebind", "0", "program"}, want: []string{"program"}},
		{name: "prlimit nproc", tokens: []string{"prlimit", "--nproc=100", "cmd"}, want: []string{"cmd"}},
		{name: "unshare net namespace", tokens: []string{"unshare", "-n", "--mount-proc", "bash"}, want: []string{"bash"}},
		{name: "stdbuf with option", tokens: []string{"stdbuf", "-i0", "-o0", "-e0", "cmd"}, want: []string{"cmd"}},

		// --- flock ---
		{name: "flock with -c", tokens: []string{"flock", "/var/lock/mylock", "-c", "echo", "hello"}, want: []string{"echo", "hello"}},
		{name: "flock with --command", tokens: []string{"flock", "-n", "/tmp/lock", "--command", "ls"}, want: []string{"ls"}},
		{name: "flock shorthand", tokens: []string{"flock", "/tmp/lock", "cat", "file"}, want: []string{"cat", "file"}},

		// --- systemd-run ---
		{name: "systemd-run basic", tokens: []string{"systemd-run", "ls", "/tmp"}, want: []string{"ls", "/tmp"}},
		{name: "systemd-run with unit name", tokens: []string{"systemd-run", "--unit", "mybackup", "rsync", "-av", "/src", "/dst"}, want: []string{"rsync", "-av", "/src", "/dst"}},
		{name: "systemd-run with --", tokens: []string{"systemd-run", "--user", "--", "cmd", "arg"}, want: []string{"cmd", "arg"}},

		// --- watch ---
		{name: "watch basic", tokens: []string{"watch", "ls", "-la"}, want: []string{"ls", "-la"}},
		{name: "watch with interval", tokens: []string{"watch", "-n", "5", "df", "-h"}, want: []string{"df", "-h"}},
		{name: "watch with --interval", tokens: []string{"watch", "--interval", "2", "date"}, want: []string{"date"}},

		// --- time with flags ---
		{name: "time -p", tokens: []string{"time", "-p", "ls"}, want: []string{"ls"}},

		// --- Nested wrappers ---
		{name: "sudo time", tokens: []string{"sudo", "time", "make", "build"}, want: []string{"make", "build"}},
		{name: "sudo env", tokens: []string{"sudo", "env", "PATH=/usr/bin", "make"}, want: []string{"make"}},
		{name: "sudo nohup", tokens: []string{"sudo", "-u", "app", "nohup", "python", "server.py"}, want: []string{"python", "server.py"}},
		{name: "time nice", tokens: []string{"time", "nice", "-n", "19", "tar", "-czf", "backup.tar.gz", "."}, want: []string{"tar", "-czf", "backup.tar.gz", "."}},
		{name: "sudo ionice nice", tokens: []string{"sudo", "ionice", "-c", "2", "-n", "0", "nice", "-n", "-20", "cmd"}, want: []string{"cmd"}},
		{name: "sudo flock", tokens: []string{"sudo", "flock", "/var/lock/x", "-c", "apt", "update"}, want: []string{"apt", "update"}},
		{name: "triple nested", tokens: []string{"sudo", "time", "nice", "make", "test"}, want: []string{"make", "test"}},
		{name: "sudo -u root env FOO=bar -- nice make", tokens: []string{"sudo", "-u", "root", "env", "FOO=bar", "--", "nice", "make"}, want: []string{"make"}},

		// --- No wrapper (pass-through) ---
		{name: "no wrapper - ls", tokens: []string{"ls", "-la"}, want: []string{"ls", "-la"}},
		{name: "no wrapper - git", tokens: []string{"git", "commit", "-m", "msg"}, want: []string{"git", "commit", "-m", "msg"}},
		{name: "no wrapper - single token", tokens: []string{"pwd"}, want: []string{"pwd"}},
		{name: "empty tokens", tokens: nil, want: nil},

		// --- Edge cases ---
		{name: "sudo with no command", tokens: []string{"sudo"}, want: []string{"sudo"}},
		{name: "env with only assignments", tokens: []string{"env", "FOO=bar", "BAZ=qux"}, want: []string{"env", "FOO=bar", "BAZ=qux"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unwrapPrefixTokens(tt.tokens)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("unwrapPrefixTokens(%v) = %v, want %v", tt.tokens, got, tt.want)
			}
		})
	}
}

func TestIsWrapper(t *testing.T) {
	wraps := []string{"sudo", "time", "nohup", "nice", "ionice", "env", "exec",
		"flock", "watch", "chroot", "setsid", "doas", "fakeroot", "stdbuf",
		"unshare", "prlimit", "taskset", "numactl", "systemd-run", "pkexec"}

	for _, w := range wraps {
		if !isWrapper(w) {
			t.Errorf("isWrapper(%q) = false, want true", w)
		}
	}

	nonWraps := []string{"git", "ls", "cat", "make", "python", "docker", "echo", "cd", "foo"}
	for _, nw := range nonWraps {
		if isWrapper(nw) {
			t.Errorf("isWrapper(%q) = true, want false", nw)
		}
	}
}
