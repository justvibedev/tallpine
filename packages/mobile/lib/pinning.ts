import type { Endpoint } from "./endpoints";

/**
 * Certificate pinning for the Mac's gateway.
 *
 * React Native's fetch and WebSocket cannot pin a certificate on their own, so
 * a small native module (modules/tallpine-pinning) replaces the iOS and Android
 * network stacks' trust decision for the hosts registered here: the connection
 * is accepted only when the SHA-256 of the server's public key (SPKI) equals the
 * pin from the pairing code. Hosts with no pin keep normal CA validation.
 */
type PinningModule = {
	setPins(entries: string[]): void;
	probe(host: string, port: number): Promise<{ pin: string; trusted: boolean }>;
};

/**
 * Looked up on each call rather than imported at load, so the pure helpers here
 * stay loadable without the native runtime (unit tests). `globalThis.expo.modules`
 * is only populated once expo-modules-core has installed its registry, so the
 * first lookup goes through it; the global is the fast path afterwards.
 */
function nativeModule(): PinningModule | undefined {
	const viaGlobal = (globalThis as { expo?: { modules?: { TallpinePinning?: PinningModule } } }).expo?.modules?.TallpinePinning;
	if (viaGlobal) return viaGlobal;
	try {
		// eslint-disable-next-line @typescript-eslint/no-require-imports
		const core = require("expo-modules-core") as { requireOptionalNativeModule: <T>(name: string) => T | null };
		return core.requireOptionalNativeModule<PinningModule>("TallpinePinning") ?? undefined;
	} catch {
		return undefined;
	}
}

const PIN_RE = /^[0-9a-f]{64}$/;

export function isValidPin(pin: unknown): pin is string {
	return typeof pin === "string" && PIN_RE.test(pin);
}

/** An endpoint carrying a pin is https by definition: the pin is useless over http. */
export function normalizeEndpoint(e: Endpoint): Endpoint {
	if (!isValidPin(e.pin)) {
		const { pin: _drop, ...rest } = e;
		return rest;
	}
	return { ...e, secure: true };
}

/**
 * The endpoints the phone may use: pinned ones (made secure by
 * normalizeEndpoint) and ones served with a trusted certificate. Plain-http
 * endpoints from an older desktop are dropped; the phone never uses them.
 */
export function secureEndpoints(endpoints: readonly Endpoint[]): Endpoint[] {
	return endpoints.map(normalizeEndpoint).filter((e) => e.secure);
}

/** `host:port=pin` for every pinned endpoint, lowercased, deduplicated. */
export function pinEntries(endpoints: readonly Endpoint[]): string[] {
	const out = new Set<string>();
	for (const e of endpoints) {
		if (isValidPin(e.pin)) out.add(`${e.host.toLowerCase()}:${e.port}=${e.pin}`);
	}
	return [...out];
}

/** What the native layer currently holds, by host:port. */
let registered = new Map<string, string>();

function sendPins(pairs: Array<[string, string]>): boolean {
	if (pairs.length === 0) return true;
	const native = nativeModule();
	if (!native) {
		console.warn("[pinning] native module missing: pinned hosts will fail certificate validation");
		return false;
	}
	native.setPins(pairs.map(([key, pin]) => `${key}=${pin}`));
	for (const [key, pin] of pairs) registered.set(key, pin);
	return true;
}

function changedPairs(endpoints: readonly Endpoint[]): Array<[string, string]> {
	return pinEntries(endpoints)
		.map((entry) => entry.split("=") as [string, string])
		.filter(([key, pin]) => registered.get(key) !== pin);
}

/**
 * Tells the native layer which keys to accept for which host:port. Additive:
 * pairing a second machine must not unpin the first. Synchronous, so it has
 * landed before the request that follows it. For pins that come from a saved
 * machine or one we are already talking to; an unconfirmed pairing offer goes
 * through stageOfferPins instead.
 */
export function registerPins(endpoints: readonly Endpoint[]): void {
	sendPins(changedPairs(endpoints));
}

/**
 * Registers the pins of a scanned or linked offer for the pairing attempt, and
 * returns a function that puts back whatever those host:ports held before. An
 * offer is untrusted input: if the attempt fails or is abandoned it must not
 * leave a saved machine's pin replaced.
 */
export function stageOfferPins(endpoints: readonly Endpoint[]): () => void {
	const pairs = changedPairs(endpoints);
	const before = pairs.flatMap(([key]) => (registered.has(key) ? [[key, registered.get(key)!] as [string, string]] : []));
	sendPins(pairs);
	return () => { sendPins(before); };
}

/** True when this host:port has a pin registered (used to route its requests through the pinned stack). */
export function hasPin(host: string, port: number): boolean {
	return registered.has(`${host.toLowerCase()}:${port}`);
}

/** Test seam. */
export function resetPinRegistrationForTest(): void {
	registered = new Map();
}

export type TlsProbe = { pin: string; trusted: boolean };

/**
 * What a host presents on first contact: its key's pin, and whether the system
 * CA store already trusts it (Tailscale Serve, a reverse proxy). Used by manual
 * entry, which has no pairing code to carry a pin; a self-signed gateway key is
 * pinned after the user confirms its fingerprint.
 */
export async function probeTls(host: string, port: number): Promise<TlsProbe> {
	const native = nativeModule();
	if (!native) throw new Error("secure connections are not available in this build");
	return native.probe(host, port);
}

/** `ab12 cd34 ef56 7890`: the first 16 hex digits, grouped for reading aloud. */
export function shortFingerprint(pin: string): string {
	return pin.slice(0, 16).replace(/(.{4})(?=.)/g, "$1 ");
}
