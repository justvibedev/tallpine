import type { ServerConfig } from "./config";
import { normalizeServerHost, type Endpoint } from "./endpoints";
import type { Host } from "./hosts";
import { isDeviceToken, type ExchangeResult } from "./pairExchange";

export type AdoptManualDeps = {
	/** The machine's reported host id, or "" when it does not report one. */
	identity: (cfg: ServerConfig) => Promise<string>;
	saveHost: (host: Host) => Promise<void>;
	setActiveHost: (id: string) => Promise<void>;
};

export type PersistManualHostEditDeps = {
	/** Public identity probe. This must run before ping presents the saved credential. */
	probe: (endpoint: Endpoint) => Promise<{ hostId: string }>;
	ping: (cfg: ServerConfig) => Promise<unknown>;
	saveHost: (host: Host) => Promise<void>;
	renameHost: (id: string, name: string) => Promise<void>;
};

/** A typed address answered, but it was not the paired machine being edited. */
export class DifferentMachineError extends Error {
	constructor() {
		super("That address belongs to another computer. This pairing was not changed.");
		this.name = "DifferentMachineError";
	}
}

/**
 * Turns a hand-entered address into a paired machine.
 *
 * ManualConnectSheet used to write only the legacy ServerConfig. That worked
 * when there was one server; with a host list it does not, because resolution
 * reconnects the *active machine* on the next launch and migration skips once
 * any host exists. The manual connection was therefore replaced by whatever was
 * there before, seconds after the user made it.
 *
 * Storing it and selecting it is the whole fix: the address the user typed is a
 * machine like any other, and the one they just chose to talk to.
 */
export async function adoptManualConnection(
	cfg: ServerConfig,
	deps: AdoptManualDeps,
	name: string,
): Promise<string> {
	const machineName = name.trim();
	if (!machineName) throw new Error("Name is required");
	const hostId = await deps.identity(cfg);
	const hostname = normalizeServerHost(cfg.host);
	await deps.saveHost({
		id: hostId,
		name: machineName,
		platform: "",
		endpoints: [manualEndpoint(cfg)],
		token: cfg.password,
		lastConnected: Date.now(),
	});
	// Even without an id: the selection is by list position, and leaving it
	// unset would resolve back to the previous machine on the next launch.
	await deps.setActiveHost(hostId);
	return hostId;
}

/** Replace one saved address and its password without dropping other discovered addresses. */
export function editedManualHost(host: Host, cfg: ServerConfig, name: string, endpointIndex: number): Host {
	const endpoint = manualEndpoint(cfg);
	const previous = host.endpoints[endpointIndex];
	if (previous) endpoint.kind = previous.kind;
	const endpoints = [...host.endpoints];
	endpoints[endpointIndex] = endpoint;
	return { ...host, name: name.trim(), token: cfg.password, endpoints };
}

/**
 * Persists the edit form shared by the connection sheet and the inline
 * Environments editor.
 *
 * Name-only edits stay local. A route or credential change first performs the
 * unauthenticated identity probe, then authenticates, and only then writes the
 * new pairing. That ordering prevents a saved bearer from being presented to a
 * different machine at a mistyped address.
 */
export async function persistManualHostEdit(
	host: Host,
	cfg: ServerConfig,
	name: string,
	endpointIndex: number,
	deps: PersistManualHostEditDeps,
): Promise<Host> {
	const machineName = name.trim();
	if (!machineName) throw new Error("Name is required");
	const target = { ...cfg, host: normalizeServerHost(cfg.host), httpPort: cfg.httpPort.trim() };
	if (!target.host) throw new Error("Host is required");
	if (!/^\d+$/.test(target.httpPort) || Number(target.httpPort) < 1 || Number(target.httpPort) > 65535) {
		throw new Error("API port must be between 1 and 65535");
	}

	const edited = editedManualHost(host, target, machineName, endpointIndex);
	const before = host.endpoints[endpointIndex];
	const after = edited.endpoints[endpointIndex];
	const connectionChanged = !before || before.host !== after.host || before.port !== after.port ||
		before.secure !== after.secure || before.pin !== after.pin || host.token !== target.password;
	if (connectionChanged) {
		const { hostId } = await deps.probe(after);
		if (host.id && hostId !== host.id) throw new DifferentMachineError();
		await deps.ping({ ...target, hostId: host.id || hostId });
		await deps.saveHost(edited);
	} else if (edited.name !== host.name) {
		await deps.renameHost(host.id, edited.name);
	}
	return edited;
}

/**
 * A typed address is one endpoint, not a list — the daemon's own list replaces
 * this on the first successful connect. TLS by hand means the tailnet path;
 * everything else was plain LAN, which mirrors how migration reads the old
 * single-server config.
 */
function manualEndpoint(cfg: ServerConfig): Endpoint {
	const secure = cfg.secure === true;
	return {
		kind: secure ? "tailscale" : "lan",
		host: normalizeServerHost(cfg.host),
		port: Number(cfg.httpPort) || 3011,
		secure,
		...(cfg.pin ? { pin: cfg.pin } : {}),
	};
}

/**
 * Turns what the user typed into the credential the daemon accepts.
 *
 * The desktop shows a single-use pairing code, not a bearer: it is redeemed
 * once at POST /pair/exchange for a per-device token, and sent as a bearer on
 * any other route it is refused with 401. Manual connect used to send it as the
 * bearer, so the correct code always failed as "That password was rotated".
 *
 * Falls back to the typed value only when the exchange is unavailable (no
 * answer, or 404/405 from a daemon without the route). A refusal is thrown as
 * is: pinging with the same value would fail auth a second time, and the daemon
 * locks a source out after five failures, so one wrong code must cost one.
 */
export async function redeemManualCredential(
	cfg: ServerConfig,
	exchange: (endpoint: Endpoint, code: string) => Promise<ExchangeResult>,
): Promise<ServerConfig> {
	const typed = cfg.password.trim();
	if (!typed || isDeviceToken(typed)) return cfg;
	const endpoint: Endpoint = { kind: "lan", host: cfg.host, port: Number(cfg.httpPort), secure: Boolean(cfg.secure), ...(cfg.pin ? { pin: cfg.pin } : {}) };
	const result = await exchange(endpoint, typed);
	if (result.ok) return { ...cfg, password: result.token };
	if (result.status === undefined || result.status === 404 || result.status === 405) return cfg;
	throw new ExchangeRefusedError(result.status);
}

/** The daemon answered the pairing exchange with a refusal (401, 429, ...). */
export class ExchangeRefusedError extends Error {
	constructor(readonly status: number) {
		super(`pairing exchange refused (${status})`);
		this.name = "ExchangeRefusedError";
	}
}
