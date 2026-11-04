package reviewgateway

import "testing"

func TestValidatePRURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://github.com/o/r/pull/7":             true,
		"https://github.com/O/R.x/pull/7":           true,
		"http://github.com/o/r/pull/7":              false,
		"https://www.github.com/o/r/pull/7":         false,
		"https://github.com/o/r/pull/7?x=1":         false,
		"https://github.com/o/r/pull/7#c":           false,
		"https://github.com/o/r/pull/7/files":       false,
		"https://gitlab.com/g/r/-/merge_requests/7": false,
		"https://ghe.io/o/r/pull/7":                 false,
		"https://t.ghe.io/o/r/pull/7":               false,
		"https://github.com/o/../pull/7":            false,
	} {
		if err := validatePRURL(raw); (err == nil) != ok {
			t.Errorf("validatePRURL(%q) err=%v, want ok=%v", raw, err, ok)
		}
	}
}
