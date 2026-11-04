/**
 * Whether a freshly resolved config should wipe the auth error left by the
 * pairing it replaces.
 *
 * A revoked pairing leaves error=401 / connection="closed" in the store. A
 * successful re-pair resolves a new, configured config but nothing cleared that
 * state until the first poll landed, so every surface in between said "Pairing
 * removed" / "That password was rotated" for a phone that had just paired.
 * Only auth failures are cleared: other errors describe the network, which a
 * resolved config says nothing about.
 */
export function shouldClearAuthError(resolvedConfigured: boolean, configChanged: boolean, errorStatus: number | null): boolean {
	// An unchanged config (a periodic upgrade race re-resolving the same
	// credential) must keep the error: polling would not restart, so clearing it
	// would strand the app on "connecting" with no re-pair prompt.
	return resolvedConfigured && configChanged && (errorStatus === 401 || errorStatus === 403);
}
