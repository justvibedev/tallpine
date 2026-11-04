import { configForEndpoint } from "./connect";
import type { ServerConfig } from "./config";
import type { Endpoint } from "./endpoints";
import type { Host } from "./hosts";
import { machineAppearance, type MachineAppearance } from "./machineAppearance";
import { isLegacyPairingCode, parsePairingCode } from "./pairingCode";
import { registerPins, stageOfferPins } from "./pinning";
import type { ExchangeResult } from "./pairExchange";
import type { PairStepId, PairStepState } from "./pairProgress";
import type { RaceOutcome } from "./race";

export type PairResult =
	| { ok: true; config: ServerConfig; host: Host }
	| {
			ok: false;
			reason: "not-tallpine-qr" | "outdated-desktop" | "no-candidates" | "none-reachable" | "incompatible" | "https-required" | "wrong-machine" | "cancelled" | "name-required";
		}
	| {
			ok: false;
			reason: "verify-failed";
			/** The endpoint that won the race: verification reached it, so it is
			 * the address to name, not whichever came first in the code. */
			endpoint: Endpoint;
			/** What the daemon answered, or undefined when it never did. Kept so a
			 * rejected password is not reported as an unreachable machine. */
			status?: number;
			/** The daemon's envelope code, so a used pairing code is not reported
			 * as a rotated password. */
			code?: string;
		};

export type PairDeps = {
	/** Probes the code's endpoints and returns the best that answers. */
	race: (endpoints: Endpoint[], expectedHostId: string) => Promise<RaceOutcome>;
	/** Trades the code's token for a per-device token. Absent in tests of the
	 * old flow; a daemon that predates the route (404/405) falls back to using
	 * the code's token as the bearer, as older desktops expect. */
	exchange?: (endpoint: Endpoint, code: string) => Promise<ExchangeResult>;
	/** Proves the winning endpoint actually serves us, with our credential. */
	verify: (config: ServerConfig) => Promise<void>;
	/** New pairings are not persisted until the user confirms a readable name.
	 * The icon is offered alongside it, starting from the machine's default. */
	chooseName: (suggested: string, appearance: MachineAppearance, hostId: string) => Promise<MachineNameChoice | null>;
	saveHost: (host: Host) => Promise<void>;
	setActiveHost: (id: string) => Promise<void>;
	/** Reports each step as it starts and ends, for the on-screen progress. */
	onStep?: (step: PairStepId, state: PairStepState, note?: string) => void;
};

function exchangeFailureNote(status: number | undefined, code: string | undefined): string {
	if (status === undefined) return "no answer from the computer";
	if (code === "PAIRING_CODE_USED") return "the code was already used";
	if (status === 401) return "the code was not accepted";
	return `the computer answered ${status}`;
}

export type MachineNameChoice = { name: string; appearance: MachineAppearance };

/**
 * Tokens already issued for a pairing code in this app session, keyed by the
 * code. A code is single-use and a successful exchange consumes it, so if the
 * step after it fails (verify over a flaky path) a retry must reuse the token
 * rather than redeem the dead code again: that earns a 401, a lockout strike,
 * and a "rotated" message for a pairing that is fine.
 */
const issuedTokens = new Map<string, string>();

/** Test hook: forget remembered tokens. */
export function resetIssuedTokens(): void {
	issuedTokens.clear();
}

export type PairOptions = {
	/** Existing identity and name when refreshing one saved pairing. */
	repairHost?: Pick<Host, "id" | "name" | "appearance">;
};

/**
 * Turns a scanned or pasted pairing code into a stored machine.
 *
 * Lives here rather than in the pair screen because `packages/mobile` has no
 * screen tests: logic left in a component is unreachable by the suite.
 *
 * Order matters. The endpoints are raced first, so what gets stored is a
 * machine we have actually reached, and verification runs before anything is
 * written — a pairing saved without being proven leaves the app looking
 * connected to something it has never spoken to.
 */
export async function pairFromCode(raw: string, deps: PairDeps, options: PairOptions = {}): Promise<PairResult> {
	const offer = parsePairingCode(raw);
	// Distinguished from unrecognised input so the reason does not depend on
	// which caller reached here: a v1 code is a real Tallpine code from a desktop too
	// old to pair with, and saying so is more useful than "not a pairing code".
	if (!offer) {
		return { ok: false, reason: isLegacyPairingCode(raw) ? "outdated-desktop" : "not-tallpine-qr" };
	}
	if (options.repairHost && offer.hostId !== options.repairHost.id) {
		return { ok: false, reason: "wrong-machine" };
	}

	// The offer is untrusted until the pairing succeeds: its pins are staged for
	// this attempt only and the previous ones come back if it does not.
	const restorePins = stageOfferPins(offer.endpoints);
	let result: PairResult;
	try {
		result = await pairOffer(offer, deps, options);
	} catch (error) {
		restorePins();
		throw error;
	}
	if (result.ok) registerPins(offer.endpoints);
	else restorePins();
	return result;
}

async function pairOffer(offer: NonNullable<ReturnType<typeof parsePairingCode>>, deps: PairDeps, options: PairOptions): Promise<PairResult> {
	const step = deps.onStep ?? (() => {});
	step("find", "active", `${offer.endpoints.length} ${offer.endpoints.length === 1 ? "address" : "addresses"} to try`);
	const outcome = await deps.race(offer.endpoints, offer.hostId);
	if (!outcome.ok) {
		step("find", "failed", outcome.reason === "incompatible" ? "this computer needs a newer app" : "nothing answered");
		return { ok: false, reason: outcome.reason };
	}
	step("find", "done", `at ${outcome.endpoint.host}:${outcome.endpoint.port}`);
	step("security", "active");

	let token = offer.token;
	const issued = offer.token ? issuedTokens.get(offer.token) : undefined;
	if (issued) {
		token = issued;
		step("security", "done", "accepted earlier");
	} else if (deps.exchange) {
		const exchanged = await deps.exchange(outcome.endpoint, offer.token);
		if (exchanged.ok) {
			token = exchanged.token;
			if (offer.token) issuedTokens.set(offer.token, token);
			step("security", "done");
		} else if (exchanged.status !== 404 && exchanged.status !== 405) {
			step("security", "failed", exchangeFailureNote(exchanged.status, exchanged.code));
			// The code was refused (wrong, used or expired) or never answered.
			return {
				ok: false,
				reason: "verify-failed",
				endpoint: outcome.endpoint,
				...(exchanged.status === undefined ? {} : { status: exchanged.status }),
				...(exchanged.code ? { code: exchanged.code } : {}),
			};
		} else {
			// An older desktop without the exchange route takes the code itself.
			step("security", "done");
		}
	} else {
		step("security", "done");
	}


	const config = configForEndpoint(outcome.endpoint, token, offer.hostId || outcome.hostId);
	step("paired", "active");
	try {
		await deps.verify(config);
	} catch (error) {
		const status = typeof error === "object" && error !== null && typeof (error as { status?: unknown }).status === "number"
			? (error as { status: number }).status
			: undefined;
		const code = typeof error === "object" && error !== null && typeof (error as { code?: unknown }).code === "string"
			? (error as { code: string }).code
			: undefined;
		step("paired", "failed", status === undefined ? "no answer from the computer" : `the computer answered ${status}`);
		return { ok: false, reason: "verify-failed", endpoint: outcome.endpoint, ...(status === undefined ? {} : { status }), ...(code ? { code } : {}) };
	}

	const hostId = offer.hostId || outcome.hostId;
	let name = options.repairHost?.name.trim() ?? "";
	let appearance = options.repairHost?.appearance;
	if (!name) {
		const suggested = offer.name || outcome.endpoint.host;
		const chosen = await deps.chooseName(suggested, appearance ?? machineAppearance({ id: hostId, name: suggested }), hostId);
		if (chosen === null) {
			step("paired", "failed", "cancelled");
			return { ok: false, reason: "cancelled" };
		}
		name = chosen.name.trim();
		if (!name) return { ok: false, reason: "name-required" };
		appearance = chosen.appearance;
	}

	const host: Host = {
		// A v1 code carries no identity, but we have just spoken to the machine
		// and it told us who it is, so adopt that now rather than leaving it
		// unverified until the next connect.
		id: hostId,
		name,
		...(appearance ? { appearance } : {}),
		platform: offer.platform,
		endpoints: offer.endpoints,
		token,
		lastConnected: Date.now(),
	};
	await deps.saveHost(host);
	await deps.setActiveHost(host.id);
	step("paired", "done", `as "${name}"`);
	if (offer.token) issuedTokens.delete(offer.token);

	return { ok: true, config, host };
}
