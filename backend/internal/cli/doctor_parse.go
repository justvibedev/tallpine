package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	ansiRE       = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	gitVersionRE = regexp.MustCompile(`(?i)\bgit version\s+(\d+(?:\.\d+){1,3})`)
)

func parseGitVersion(out string) (string, error) {
	clean := ansiRE.ReplaceAllString(out, "")
	m := gitVersionRE.FindStringSubmatch(clean)
	if len(m) < 2 {
		return "", fmt.Errorf("parse git version from %q", strings.TrimSpace(clean))
	}
	return m[1], nil
}

func firstOutputLine(out []byte) string {
	clean := strings.TrimSpace(ansiRE.ReplaceAllString(string(out), ""))
	if clean == "" {
		return ""
	}
	line := strings.SplitN(clean, "\n", 2)[0]
	return strings.TrimSpace(line)
}

func compareDottedVersion(a, b string) (int, error) {
	ap, err := dottedVersionParts(a)
	if err != nil {
		return 0, err
	}
	bp, err := dottedVersionParts(b)
	if err != nil {
		return 0, err
	}
	maxLen := len(ap)
	if len(bp) > maxLen {
		maxLen = len(bp)
	}
	for i := 0; i < maxLen; i++ {
		var av, bv int
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		switch {
		case av < bv:
			return -1, nil
		case av > bv:
			return 1, nil
		}
	}
	return 0, nil
}

func dottedVersionParts(s string) ([]int, error) {
	raw := strings.Split(s, ".")
	parts := make([]int, 0, len(raw))
	for _, part := range raw {
		if part == "" {
			return nil, fmt.Errorf("empty version segment in %q", s)
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("parse version segment %q in %q: %w", part, s, err)
		}
		parts = append(parts, n)
	}
	return parts, nil
}
