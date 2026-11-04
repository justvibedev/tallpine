package mobilebridge

import "context"

// LegacyCredential marks a LAN request authenticated with the pre-token
// connection password rather than a per-device token.
const LegacyCredential = "legacy-password"

type credentialKey struct{}

// WithCredential records which credential authenticated a LAN request: a
// device token's hash, or LegacyCredential.
func WithCredential(ctx context.Context, hash string) context.Context {
	return context.WithValue(ctx, credentialKey{}, hash)
}

// CredentialFrom returns the credential WithCredential recorded. ok is false
// for requests that did not come through the LAN listener (loopback).
func CredentialFrom(ctx context.Context) (hash string, ok bool) {
	hash, ok = ctx.Value(credentialKey{}).(string)
	return hash, ok && hash != ""
}

// TokenIDPrefix is a short, non-secret label for a token hash in logs.
func TokenIDPrefix(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
