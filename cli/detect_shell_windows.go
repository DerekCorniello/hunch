//go:build windows

package cli

// detectShellFallback is a best-effort fallback when $SHELL is not set.
// On Windows, os.Getppid() always returns 0 (no /proc filesystem),
// so we cannot reliably detect the parent shell. Return empty to let
// the user specify their shell explicitly via `hunch init <shell>`.
func detectShellFallback() string {
	return ""
}
