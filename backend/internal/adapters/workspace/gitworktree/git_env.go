package gitworktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type gitEnvKey struct{}

// withGitEnv makes runCommand add env to the git process it starts. The value
// stays inside this adapter's command path; it is never logged.
func withGitEnv(ctx context.Context, env []string) context.Context {
	if len(env) == 0 {
		return ctx
	}
	return context.WithValue(ctx, gitEnvKey{}, env)
}

func gitEnvFrom(ctx context.Context) []string {
	env, _ := ctx.Value(gitEnvKey{}).([]string)
	return env
}

// gitEnvFingerprint identifies a credential without containing it; "" for none.
func gitEnvFingerprint(env []string) string {
	if len(env) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(env, "\x00")))
	return hex.EncodeToString(sum[:])
}
