//go:build windows

package releaseinstall

import "errors"

func startDetached(_, _ []string, _ string) (int, error) {
	return 0, errors.New("release install is macOS only")
}
