//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
)

func detectShellFallback() string {
	ppid := os.Getppid()
	exe, err := os.Readlink("/proc/" + itoa(ppid) + "/exe")
	if err != nil {
		return ""
	}
	base := strings.ToLower(filepath.Base(exe))
	base = strings.TrimSuffix(base, ".exe")
	switch base {
	case "pwsh", "powershell":
		return "powershell"
	case "cmd":
		return ""
	default:
		return ""
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
