package normalize

import "strings"

// tokenize splits a raw command string into tokens, respecting shell quotes.
// Shell operators (|, &&, ||, ;) are emitted as standalone tokens even without
// surrounding whitespace. Returns unquoted token values.
func tokenize(raw string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case escaped:
			current.WriteByte(ch)
			escaped = false
		case ch == '\\' && inDouble:
			escaped = true
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			if !inSingle && !inDouble {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				continue
			}
			current.WriteByte(ch)
		case ch == '|' && !inSingle && !inDouble:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			if i+1 < len(raw) && raw[i+1] == '|' {
				tokens = append(tokens, "||")
				i++
			} else {
				tokens = append(tokens, "|")
			}
		case ch == '&' && !inSingle && !inDouble:
			// Only emit && as a compound operator, not bare & (avoid
			// splitting redirects like 2>&1).
			if i+1 < len(raw) && raw[i+1] == '&' {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				tokens = append(tokens, "&&")
				i++
			} else {
				current.WriteByte(ch)
			}
		case ch == ';' && !inSingle && !inDouble:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, ";")
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
