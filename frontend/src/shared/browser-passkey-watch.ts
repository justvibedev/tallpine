/**
 * Watches a Browser panel page's WebAuthn calls so the panel can say why a
 * passkey sign-in seems stuck.
 *
 * Electron ships no WebAuthn UI: no Touch ID platform authenticator unless the
 * app is provisioned for it, no hybrid (phone / QR) sheet, and no PIN prompt.
 * A passkey `navigator.credentials.get()` therefore waits silently until the
 * page's timeout. This watch reports whether such a request is in flight.
 *
 * `installPasskeyRequestWatch` runs in the page's main world through
 * `contextBridge.executeInMainWorld`, which serializes it with toString: it
 * must not reference anything outside its own body.
 */
export const BROWSER_WEBAUTHN_STATE_CHANNEL = "browser:webauthn:state";

export function installPasskeyRequestWatch(report: (pending: boolean) => void): void {
	const container = typeof navigator === "undefined" ? undefined : navigator.credentials;
	if (!container) return;
	const proto = Object.getPrototypeOf(container) as Record<string, unknown> | null;
	if (!proto) return;
	let pending = 0;
	const settle = (): void => {
		pending -= 1;
		if (pending === 0) report(false);
	};
	for (const name of ["get", "create"]) {
		const descriptor = Object.getOwnPropertyDescriptor(proto, name);
		const original = descriptor?.value;
		if (!descriptor || typeof original !== "function") continue;
		// A Proxy keeps the native toString and name, so the page sees the same
		// function shape it would in Chrome.
		const watched = new Proxy(original, {
			apply(target, thisArg, args: unknown[]) {
				const result = Reflect.apply(target, thisArg, args) as unknown;
				const options = args[0] as { publicKey?: unknown; mediation?: unknown } | null | undefined;
				// Conditional mediation is the passive autofill request many sign-in
				// pages leave pending for their whole life; it is not a stuck prompt.
				if (!options || typeof options !== "object" || !options.publicKey || options.mediation === "conditional") return result;
				pending += 1;
				if (pending === 1) report(true);
				Promise.resolve(result).then(settle, settle);
				return result;
			},
		});
		Object.defineProperty(proto, name, { ...descriptor, value: watched });
	}
}
