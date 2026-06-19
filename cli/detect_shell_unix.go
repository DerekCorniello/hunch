//go:build !windows

package cli

func detectShellFallback() string {
	return ""
}
