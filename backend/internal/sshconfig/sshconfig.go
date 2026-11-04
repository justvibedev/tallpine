// Package sshconfig resolves ~/.ssh/config Host aliases; the one `ssh -G` reader.
package sshconfig

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/process"
)

// HostName returns the lower-cased HostName `ssh -G -- alias` reports, "" when
// none is printed, and an error when ssh fails or ctx expires.
func HostName(ctx context.Context, alias string) (string, error) {
	out, err := process.CommandContext(ctx, "ssh", "-G", "--", alias).Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if name, value, ok := strings.Cut(strings.TrimSpace(line), " "); ok && strings.EqualFold(name, "hostname") {
			return strings.ToLower(strings.TrimSpace(value)), nil
		}
	}
	return "", nil
}
