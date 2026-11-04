import { DEFAULT_CONFIG, type ServerConfig } from "./config";
import type { Endpoint } from "./endpoints";
import { mergeEndpoints } from "./mergeEndpoints";
import type { Host } from "./hosts";
import { isDeviceToken } from "./pairExchange";
import type { RaceOutcome } from "./race";
import { isPairingRevoked } from "./revokedPairing";

/**
 * Adapts a won endpoint into the shape the API layer already speaks.
 *
 * Everything downstream — api.ts, mux.ts, the terminal — is built on
 * ServerConfig, so the race hands back one of those rather than threading a new
 * type through every call site.
 */
export function configForEndpoint(endpoint: Endpoint, token: string, hostId = ""): ServerConfig {
	return {
		...DEFAULT_CONFIG,
		host: endpoint.host,
		httpPort: String(endpoint.port),
		secure: endpoint.secure,
		...(endpoint.pin ? { pin: endpoint.pin } : {}),
		password: token,
		// Carried so per-machine state — the chat event cursor above all — keys on
		// the machine rather than whichever address won the race.
		...(hostId ? { hostId } : {}),
		// Carried so the poll can speed up on paths where the event stream
		// cannot deliver. See pollInterval.ts.
		endpointKind: endpoint.kind,
	};
}

export type ConnectResult =
	| { ok: true; config: ServerConfig; endpoint: Endpoint; hostId: string }
	| { ok: false; reason: "unknown-host" | "no-candidates" | "none-reachable" | "incompatible" | "https-required" | "pairing-removed" };

export type ConnectDeps = {
	findHost: (id: string) => Promise<Host | null>;
	race: (host: Host) => Promise<RaceOutcome>;
	/** Asks the daemon what it advertises now, over the endpoint that just won. */
	refreshEndpoints: (config: ServerConfig) => Promise<Endpoint[]>;
	saveEndpoints: (hostId: string, endpoints: Endpoint[]) => Promise<void>;
	/** Records the identity a machine reported that had none stored. */
	adoptIdentity: (oldId: string, hostId: string) => Promise<void>;
	touch: (hostId: string) => Promise<void>;
	/** Trades a stored legacy password for a device token over the endpoint that
	 * just won. Returns the new token, or null to keep using the old one. */
	upgradeToken?: (endpoint: Endpoint, legacyToken: string) => Promise<string | null>;
	/** Persists the upgraded token only while storage still holds `expectedCurrent`
	 * (the legacy value the upgrade started from). False means it did not. */
	saveToken?: (hostId: string, token: string, expectedCurrent: string) => Promise<boolean | void>;
};

/**
 * Upgrades in flight, per machine. reloadConfig runs concurrently (resume, the
 * upgrade check, the retry timer, the end of pairing), and each run used to
 * POST its own exchange: the daemon replaces every token of an install on each
 * one, so the second exchange revoked the first, and whichever response was
 * saved last could be the revoked one.
 */
const upgradesInFlight = new Map<string, Promise<string | null>>();

/** Save attempts before giving up on persisting an upgraded token. */
const SAVE_ATTEMPTS = 2;

async function upgradeAndSave(hostKey: string, endpoint: Endpoint, legacy: string, deps: ConnectDeps): Promise<string | null> {
	const issued = await deps.upgradeToken!(endpoint, legacy).catch(() => null);
	if (!issued) return null;
	for (let attempt = 0; attempt < SAVE_ATTEMPTS; attempt++) {
		try {
			// void (older fakes) counts as saved; only an explicit false is a refusal.
			if ((await deps.saveToken!(hostKey, issued, legacy)) === false) return null;
			return issued;
		} catch {
			/* retry once */
		}
	}
	// Not persisted: using the new token now would leave storage on the legacy
	// password, so every launch would upgrade again and revoke the last token.
	// Stay on the legacy credential, which the daemon still honours.
	return null;
}

/**
 * Connects to a paired machine: race its endpoints, then refresh what we know
 * about it from whichever one won.
 *
 * The refresh is what makes the pairing self-healing. A tunnel hostname that
 * rotated, a LAN address that changed with the lease, a machine that gained
 * Tailscale — all of it is picked up on the next successful connect without the
 * user re-pairing.
 */
export async function connectHost(id: string, deps: ConnectDeps): Promise<ConnectResult> {
	const host = await deps.findHost(id);
	if (!host) return { ok: false, reason: "unknown-host" };
	// The machine already rejected this pairing. Probing, refreshing endpoints
	// or upgrading its token would only send the dead credential again.
	if (host.pairingRemoved || isPairingRevoked(host.token)) return { ok: false, reason: "pairing-removed" };

	const outcome = await deps.race(host);
	if (!outcome.ok) return { ok: false, reason: outcome.reason };

	const hostKeyForConfig = host.id === "" ? outcome.hostId : host.id;
	let token = host.token;
	// A phone paired before device tokens upgrades itself in place on its first
	// connect; the old password stops working once the daemon's grace window ends.
	if (token && !isDeviceToken(token) && deps.upgradeToken && deps.saveToken) {
		let flight = upgradesInFlight.get(hostKeyForConfig);
		if (!flight) {
			flight = upgradeAndSave(hostKeyForConfig, outcome.endpoint, token, deps).finally(() => upgradesInFlight.delete(hostKeyForConfig));
			upgradesInFlight.set(hostKeyForConfig, flight);
		}
		const upgraded = await flight;
		if (upgraded) token = upgraded;
	}
	const config = configForEndpoint(outcome.endpoint, token, hostKeyForConfig);

	// A machine migrated from the single-server config has no id until it
	// connects once. This is where it learns one, and it is verified from here on.
	if (host.id === "" && outcome.hostId !== "") {
		await deps.adoptIdentity(host.id, outcome.hostId);
	}

	const hostKey = host.id === "" ? outcome.hostId : host.id;

	try {
		const advertised = await deps.refreshEndpoints(config);
		// The daemon cannot advertise a client-side NAT or proxy address that just worked.
		const refreshed = advertised.some((e) => e.kind === outcome.endpoint.kind) &&
			!advertised.some((e) => e.kind === outcome.endpoint.kind && e.host === outcome.endpoint.host && e.port === outcome.endpoint.port && e.secure === outcome.endpoint.secure)
			? [...advertised, outcome.endpoint] : advertised;
		// Merged, not replaced: a kind the daemon omits — a tunnel mid-restart,
		// above all — is unknown rather than gone. See mergeEndpoints.
		await deps.saveEndpoints(
			hostKey,
			mergeEndpoints(host.endpoints, refreshed),
		);
	} catch {
		// A failed refresh must not cost us a working connection: the endpoints
		// already stored are what got us here and remain good enough.
	}
	await deps.touch(hostKey);

	return { ok: true, config, endpoint: outcome.endpoint, hostId: hostKey };
}
