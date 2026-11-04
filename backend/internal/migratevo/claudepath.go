package migratevo

import (
	"strings"
	"unicode/utf16"
)

// maxClaudeDirName is the length above which Claude Code appends a hash to the
// escaped name. We cannot reproduce that suffix, so such paths are reported as
// misses instead of guessed.
const maxClaudeDirName = 200

// ClaudeProjectDirName escapes a session cwd the way Claude Code names its
// ~/.claude/projects/<dir>: every UTF-16 code unit outside [A-Za-z0-9]
// becomes "-". ok is false when the name is too long to reproduce.
func ClaudeProjectDirName(cwd string) (name string, ok bool) {
	var b strings.Builder
	for _, u := range utf16.Encode([]rune(cwd)) {
		if u < 0x80 && (u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9') {
			b.WriteByte(byte(u))
		} else {
			b.WriteByte('-')
		}
	}
	return b.String(), b.Len() <= maxClaudeDirName
}
