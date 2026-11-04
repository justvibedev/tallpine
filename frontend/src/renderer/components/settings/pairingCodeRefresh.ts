/**
 * The desktop's pairing code is single-use and expires after ten minutes, so a
 * displayed QR goes stale when a phone pairs or time passes. While Connect
 * Mobile is on, keep re-reading the status at this slower rate so the code on
 * screen is replaced soon after the daemon mints a fresh one.
 */
export const PAIRING_CODE_REFRESH_MS = 5_000;

/** Combines the readiness poll interval with the pairing-code refresh. */
export function withPairingCodeRefresh(readinessInterval: number | false, enabled: boolean | undefined): number | false {
	if (readinessInterval !== false) return readinessInterval;
	return enabled ? PAIRING_CODE_REFRESH_MS : false;
}
