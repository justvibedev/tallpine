package contextbudget

import (
	"path"
	"strings"
)

// Shell-command parsing for the doc-routing finder: which files a command
// read and what it searched for.

var (
	readers   = map[string]bool{"cat": true, "head": true, "tail": true, "sed": true, "bat": true, "less": true, "more": true, "nl": true}
	searchers = map[string]bool{"rg": true, "grep": true, "egrep": true, "ag": true, "ack": true}
	// valueFlags take the next word as their value, which is not the pattern.
	valueFlags = map[string]bool{"-g": true, "--glob": true, "-t": true, "--type": true, "-A": true, "-B": true, "-C": true, "-m": true, "--max-count": true, "-j": true, "--include": true, "--exclude": true}
)

func parseSegment(seg shellSeg, projectPath string) (reads, searches []string) {
	words := seg.words
	if len(words) == 0 {
		return nil, nil
	}
	cmd := path.Base(words[0])
	args := words[1:]
	switch {
	case readers[cmd]:
		for _, a := range args {
			if p, ok := repoDoc(a, projectPath); ok {
				reads = append(reads, p)
			}
		}
	case searchers[cmd]:
		if seg.piped {
			return nil, nil // a filter over another command's output
		}
		if q := repoSearch(cmd, args, projectPath); q != "" {
			searches = []string{q}
		}
	}
	return reads, searches
}

// repoSearch is the pattern of a grep-like command that searches repo files:
// not stdin, and not a log, tmp file or other path outside the repo. "" when
// the command is no such search.
func repoSearch(cmd string, args []string, projectPath string) string {
	pattern, patternSet, recursive := "", false, false
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "<"):
			return "" // input redirect: stdin, not repo files
		case (a == "-e" || a == "--regexp") && !patternSet:
			if i+1 >= len(args) {
				return ""
			}
			pattern, patternSet = args[i+1], true
			i++
		case valueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
			if !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rR") || a == "--recursive" {
				recursive = true
			}
		case !patternSet:
			pattern, patternSet = a, true
		default:
			paths = append(paths, a)
		}
	}
	if !patternSet {
		return ""
	}
	if len(paths) == 0 {
		// rg, ag and ack search the working directory; grep without -r reads stdin.
		if cmd == "grep" || cmd == "egrep" {
			if !recursive {
				return ""
			}
		}
		return searchKey(pattern)
	}
	for _, p := range paths {
		if repoPath(p, projectPath) {
			return searchKey(pattern)
		}
	}
	return ""
}

// repoPath reports whether p names a place inside the repo that holds source or
// docs: not a log, tmp or output file, and not outside the project or one of
// its worktrees.
func repoPath(p, projectPath string) bool {
	if p == "" || strings.HasPrefix(p, "~") || strings.HasPrefix(p, "$") {
		return false
	}
	rel, ok := repoRelative(p, projectPath)
	if !ok {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		switch strings.ToLower(part) {
		case "tmp", "temp", "log", "logs", "var", ".tallpine":
			return false
		}
	}
	switch strings.ToLower(path.Ext(rel)) {
	case ".log", ".out", ".err", ".tmp":
		return false
	}
	return true
}

// searchKey is the comparable form of a search pattern; "" when it is too
// short to say anything.
func searchKey(q string) string {
	q = strings.TrimSpace(q)
	if len(q) < minSearchChars {
		return ""
	}
	return q
}

// shellSeg is one simple command; piped is set when it reads another command's output.
type shellSeg struct {
	words []string
	piped bool
}

// shellSegments splits a command line into the words of each simple command,
// honoring quotes; pipes, &&, || and ; start a new segment.
func shellSegments(line string) [][]string {
	pipeline := shellPipeline(line)
	out := make([][]string, 0, len(pipeline))
	for _, s := range pipeline {
		out = append(out, s.words)
	}
	return out
}

func shellPipeline(line string) []shellSeg {
	var segs []shellSeg
	var cur []string
	var word strings.Builder
	var quote, prev rune
	inWord, pipeNext, curPiped := false, false, false
	flush := func() {
		if inWord {
			cur = append(cur, word.String())
			word.Reset()
			inWord = false
		}
	}
	endSeg := func() {
		flush()
		if len(cur) > 0 {
			segs = append(segs, shellSeg{words: cur, piped: curPiped})
			cur = nil
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		case r == '|' || r == ';' || r == '&':
			endSeg()
			switch {
			case r == '|' && prev == '|':
				pipeNext = false // "||" is no pipe
			case r == '|':
				pipeNext = true
			case r == '&' && prev == '|':
			default:
				pipeNext = false
			}
			curPiped = pipeNext
		default:
			word.WriteRune(r)
			inWord = true
		}
		prev = r
	}
	endSeg()
	return segs
}

var nameFlags = map[string]bool{"-name": true, "-iname": true, "-path": true, "-ipath": true, "-g": true, "--glob": true, "-e": true, "--regexp": true}

// segmentTargets returns the words of a search command that say what it looks
// for: the pattern, and name or glob arguments. Paths and other options are
// left out.
func segmentTargets(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	cmd := path.Base(words[0])
	if !searchers[cmd] && cmd != "find" && cmd != "fd" {
		return nil
	}
	var out []string
	patternSeen := cmd == "find" // find has no positional pattern
	for i := 1; i < len(words); i++ {
		a := words[i]
		switch {
		case nameFlags[a]:
			if i+1 < len(words) {
				out = append(out, words[i+1])
				patternSeen = patternSeen || a == "-e" || a == "--regexp"
				i++
			}
		case valueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		case !patternSeen:
			out, patternSeen = append(out, a), true
		}
	}
	return out
}
