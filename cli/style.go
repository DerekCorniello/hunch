package cli

import (
	"os"
	"strings"
)

// colorize is true only when stdout is a real terminal (or a PTY like a
// recorder), so piped output stays plain and tests see no escape codes. Set
// NO_COLOR to force it off explicitly.
var colorize = stdoutIsTerminal() && os.Getenv("NO_COLOR") == ""

func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// style wraps s in the given ANSI SGR code; a no-op when output is piped.
func style(code, s string) string {
	if !colorize {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Catppuccin Mocha palette (256-color approximations).
func bold(s string) string   { return style("1", s) }
func red(s string) string    { return style("38;5;203", s) } // #f38ba8
func green(s string) string  { return style("38;5;150", s) } // #a6e3a1
func yellow(s string) string { return style("38;5;229", s) } // #f9e2af
func teal(s string) string   { return style("38;5;122", s) } // #94e2d5
func purple(s string) string { return style("38;5;183", s) } // #cba6f7

// colorJSON paints field names and values of an indented JSON blob so `hunch
// stats` reads well on a terminal. It walks line by line: the first quoted
// token is a field name if a ':' follows its closing quote, otherwise it is a
// bare string value. A no-op when output would be piped.
func colorJSON(b []byte) string {
	if !colorize {
		return string(b)
	}
	lines := strings.Split(string(b), "\n")
	var sb strings.Builder
	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		open := strings.IndexByte(line, '"')
		if open < 0 {
			sb.WriteString(line)
			continue
		}
		close := strings.IndexByte(line[open+1:], '"')
		if close < 0 {
			sb.WriteString(line)
			continue
		}
		end := open + 1 + close
		token := line[open : end+1]
		painted := teal(token)
		if strings.HasPrefix(strings.TrimSpace(line[end+1:]), ":") {
			painted = purple(token)
		}
		sb.WriteString(line[:open])
		sb.WriteString(painted)
		sb.WriteString(line[end+1:])
	}
	return sb.String()
}
