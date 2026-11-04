// Credentials a machine has rejected with a 401. Free of React Native imports
// so the request layer and its tests can use it directly.
//
// A rejected credential stays rejected: the desktop revoked the pairing, the
// legacy password expired, or the token was never valid there. Every further
// request with it is wasted, and before the daemon learned to tell a revoked
// token from a guess, a phone polling with one locked its own address out and
// the fresh pairing scan that would fix it was refused. So once a machine
// answers 401, nothing is sent with that credential again until the user
// re-pairs, which stores a new one.

export const PAIRING_REMOVED_TITLE = "Pairing removed";
export const PAIRING_REMOVED_MESSAGE = "Scan the code on that computer again.";

const revoked = new Set<string>();
const listeners = new Set<(token: string) => void>();

export function isPairingRevoked(token: string | undefined): boolean {
	return !!token && revoked.has(token);
}

/** Records a rejected credential. Listeners hear about each one once, so the
 * store can persist the machine's state without reacting to every request. */
export function markPairingRevoked(token: string | undefined): void {
	if (!token || revoked.has(token)) return;
	revoked.add(token);
	for (const listener of listeners) listener(token);
}

/** Seeds a credential already known to be rejected (read from storage at
 * launch) without notifying: it is persisted already. */
export function rememberPairingRevoked(token: string | undefined): void {
	if (token) revoked.add(token);
}

/** A credential the user just saved again (re-pair, or a corrected legacy
 * password) gets one more chance. */
export function clearPairingRevoked(token: string | undefined): void {
	if (token) revoked.delete(token);
}

export function onPairingRevoked(listener: (token: string) => void): () => void {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

/** Test seam. */
export function resetRevokedPairings(): void {
	revoked.clear();
	listeners.clear();
}
