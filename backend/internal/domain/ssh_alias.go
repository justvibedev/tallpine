package domain

import (
	"context"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/sshconfig"
)

// ResolveSSHHostAlias maps an ~/.ssh/config Host alias (git@github-work:o/r) to
// its real hostname. It returns "" when the alias cannot be resolved. Tests
// replace it.
var ResolveSSHHostAlias = resolveSSHHostAliasViaSSH

// sshAliasCache holds successful lookups only; a failed or timed-out ssh -G is
// retried on the next call.
var sshAliasCache sync.Map

// sshHostName is the lookup; tests stub it.
var sshHostName = sshconfig.HostName

func resolveSSHHostAliasViaSSH(host string) string {
	if v, ok := sshAliasCache.Load(host); ok {
		s, _ := v.(string)
		return s
	}
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t/") {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resolved, err := sshHostName(ctx, host)
	if err != nil {
		return ""
	}
	sshAliasCache.Store(host, resolved)
	return resolved
}
