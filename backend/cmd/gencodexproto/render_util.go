package main

import (
	"fmt"
	"strings"
)

func doc(b *strings.Builder, name, description string) {
	if description == "" {
		return
	}
	fmt.Fprintf(b, "// %s: %s\n", name, oneLine(description))
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.TrimSpace(s)
	const limit = 110
	if len(s) > limit {
		s = s[:limit] + "…"
	}
	return s
}

// exportName turns a JSON or schema name into an exported Go identifier.
func exportName(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		alpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		digit := r >= '0' && r <= '9'
		switch {
		// Anything that is not alphanumeric is a word boundary. The schema uses
		// `_`, `-`, `/`, `.` and, for JSON Schema's own keywords, `$`.
		case !alpha && !digit:
			upper = true
		case digit:
			b.WriteRune(r)
			upper = false
		case upper:
			b.WriteString(strings.ToUpper(string(r)))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	// A Go identifier cannot begin with a digit.
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "N" + out
	}
	// Initialisms, so the generated names read like Go rather than like a
	// transliteration.
	for _, pair := range [][2]string{
		{"Id", "ID"}, {"Url", "URL"}, {"Uri", "URI"}, {"Api", "API"},
		{"Json", "JSON"}, {"Http", "HTTP"}, {"Mcp", "MCP"}, {"Sdp", "SDP"},
		{"Cwd", "Cwd"}, {"Ts", "Ts"},
	} {
		if strings.HasSuffix(out, pair[0]) {
			out = strings.TrimSuffix(out, pair[0]) + pair[1]
		}
	}
	return out
}
