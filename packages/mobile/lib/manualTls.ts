import type { ServerConfig } from "./config";
import { registerPins, shortFingerprint, type TlsProbe } from "./pinning";

export class ManualTlsDeclinedError extends Error {
	constructor() {
		super("You did not confirm the security code, so nothing was connected.");
		this.name = "ManualTlsDeclinedError";
	}
}

export type ManualTlsDeps = {
	probe: (host: string, port: number) => Promise<TlsProbe>;
	/** Shows the fingerprint; resolves true when the user says it matches the desktop. */
	confirm: (fingerprint: string) => Promise<boolean>;
	register?: typeof registerPins;
};

/**
 * Makes a hand-typed address https. The phone never speaks plain http to the
 * Mac, but typing an address gives it no pin to hold the Mac to, so it asks the
 * host what it presents:
 * - a certificate the system already trusts (Tailscale Serve, a reverse proxy)
 *   is used as is, with normal CA validation;
 * - the gateway's own self-signed key is pinned, but only after the user
 *   confirms that its fingerprint matches the one Tallpine shows on the Mac.
 *   That check is what stops a device on the same Wi-Fi from answering first.
 * A host already carrying a pin keeps it.
 */
export async function establishManualTls(
	cfg: ServerConfig,
	deps: ManualTlsDeps,
	/** The endpoint being edited, when there is one. */
	current?: { host: string; port: number },
): Promise<ServerConfig> {
	// A pin belongs to one host:port: keep it only while editing that same
	// address, otherwise the new address goes through the probe and confirmation.
	const sameAddress = current && current.host.toLowerCase() === cfg.host.toLowerCase() && String(current.port) === cfg.httpPort;
	if (cfg.pin && sameAddress) return { ...cfg, secure: true };
	if (cfg.pin) cfg = { ...cfg, pin: undefined };
	const port = Number(cfg.httpPort);
	const seen = await deps.probe(cfg.host, port);
	if (seen.trusted) return { ...cfg, secure: true, pin: undefined };
	if (!(await deps.confirm(shortFingerprint(seen.pin)))) throw new ManualTlsDeclinedError();
	(deps.register ?? registerPins)([{ kind: "lan", host: cfg.host, port, secure: true, pin: seen.pin }]);
	return { ...cfg, secure: true, pin: seen.pin };
}
