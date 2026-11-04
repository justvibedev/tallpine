import {
	base64ToBytes as sharedBase64ToBytes,
	closeFrame,
	dataFrame,
	openFrame,
	pingFrame,
	resizeFrame,
	subscribeFrame,
} from "@tallpine/shared/mux/frames";
import { isPairingRevoked, PAIRING_REMOVED_MESSAGE } from "./revokedPairing";
import { authHeaders, muxUrl, type ServerConfig } from "./config";
import { space } from "./tokens";

// Mirrors Tallpine's mux-protocol.ts (the bits we use).
export type SessionPatch = {
	id: string;
	status: string;
	activity: string | null;
	lastActivityAt: string;
};

export type MuxStatus = "connecting" | "open" | "closed" | "error";

type Handlers = {
	onStatus?: (s: MuxStatus, detail?: string) => void;
	onTerminalData?: (id: string, bytes: Uint8Array) => void;
	onTerminalOpened?: (id: string) => void;
	onTerminalExited?: (id: string, code: number) => void;
	onTerminalError?: (id: string, message: string) => void;
	// The daemon pushes the shared PTY's authoritative grid (the size the PTY is
	// actually using, driven by the largest/primary client) so this follower can
	// render that exact grid scaled to fit, instead of its own fitted size.
	onTerminalResize?: (id: string, cols: number, rows: number) => void;
	onSessions?: (sessions: SessionPatch[]) => void;
};

// Encode a JS string (already UTF-8 decoded by the server) back to UTF-8 bytes
// for xterm. Prefer the native TextEncoder; fall back to a manual encoder if a
// runtime ever lacks it, so the terminal never hard-crashes on a missing global.
const nativeEncoder = typeof TextEncoder !== "undefined" ? new TextEncoder() : null;

function utf8Encode(str: string): Uint8Array {
	if (nativeEncoder) return nativeEncoder.encode(str);
	const out: number[] = [];
	for (let i = 0; i < str.length; i++) {
		let c = str.charCodeAt(i);
		if (c < 0x80) {
			out.push(c);
		} else if (c < 0x800) {
			out.push(0xc0 | (c >> 6), 0x80 | (c & 0x3f));
		} else if (c >= 0xd800 && c <= 0xdbff && i + 1 < str.length) {
			const c2 = str.charCodeAt(++i);
			c = 0x10000 + ((c & 0x3ff) << 10) + (c2 & 0x3ff);
			out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 0x3f), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
		} else {
			out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
		}
	}
	return new Uint8Array(out);
}

// The Go daemon carries terminal payloads as base64 (Go base64.StdEncoding);
// the encoder and the frame shapes are shared with the desktop.
export { bytesToBase64 } from "@tallpine/shared/mux/frames";

/**
 * Decodes one base64 payload from the daemon.
 *
 * Returns empty instead of throwing: `handle` is called outside the JSON.parse
 * guard in ws.onmessage, so a throw here would escape the socket callback. An
 * unreadable frame is dropped, which is what that guard already does one level up.
 */
export function base64ToBytes(b64: string): Uint8Array {
	try {
		return sharedBase64ToBytes(b64);
	} catch {
		return new Uint8Array(0);
	}
}

/**
 * Thin client over Tallpine's mux WebSocket. One socket multiplexes session-status
 * snapshots and per-session terminal I/O. Auto-reconnects with backoff.
 */
export class MuxClient {
	private ws: WebSocket | null = null;
	private cfg: ServerConfig;
	private handlers: Handlers;
	private closedByUser = false;
	private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
	private pingTimer: ReturnType<typeof setInterval> | null = null;
	private backoff = 1000;
	// Terminals we want open, so we can re-open them after a reconnect. Maps the
	// session id -> its projectId so the re-open carries projectId too (the server
	// may need it to locate the right session across projects).
	private openTerminals = new Map<string, string | undefined>();
	private subscribed = false;

	constructor(cfg: ServerConfig, handlers: Handlers) {
		this.cfg = cfg;
		this.handlers = handlers;
	}

	connect() {
		this.closedByUser = false;
		this.open();
	}

	private open() {
		// A rejected pairing stays rejected; reconnecting would only resend it.
		if (isPairingRevoked(this.cfg.password)) {
			this.handlers.onStatus?.("error", PAIRING_REMOVED_MESSAGE);
			return;
		}
		this.handlers.onStatus?.("connecting");
		let ws: WebSocket;
		try {
			// The Go daemon's CORS guard 403s any non-loopback Origin before the WS
			// upgrade. React Native's WebSocket (iOS SocketRocket) auto-sets Origin to
			// the socket URL - e.g. the LAN/proxy address a phone points at - so the
			// upgrade is rejected even though the REST API (fetch sends no Origin) works.
			// Pin a loopback Origin so the upgrade passes. RN honors this third `options`
			// arg; web browsers ignore it (and set Origin to the page), which is fine.
			const WS = WebSocket as unknown as {
				new (url: string, protocols?: string | string[], options?: { headers?: Record<string, string> }): WebSocket;
			};
			ws = new WS(muxUrl(this.cfg), undefined, { headers: { Origin: "http://localhost", ...authHeaders(this.cfg) } });
		} catch (e) {
			this.handlers.onStatus?.("error", String(e));
			this.scheduleReconnect();
			return;
		}
		this.ws = ws;

		ws.onopen = () => {
			this.backoff = 1000;
			this.handlers.onStatus?.("open");
			if (this.subscribed) this.sendRaw(subscribeFrame(["sessions", "notifications"]));
			// Re-open any terminals that were active before a reconnect (with projectId).
			for (const [id, projectId] of this.openTerminals) {
				this.sendRaw(openFrame(id, { projectId, role: "secondary" }));
			}
			this.pingTimer = setInterval(() => {
				this.sendRaw(pingFrame());
			}, 20000);
		};

		ws.onmessage = (ev) => {
			let msg: unknown;
			try {
				msg = JSON.parse(typeof ev.data === "string" ? ev.data : "");
			} catch {
				return;
			}
			this.handle(msg);
		};

		ws.onerror = () => {
			this.handlers.onStatus?.("error");
		};

		ws.onclose = () => {
			this.clearPing();
			if (this.closedByUser) {
				this.handlers.onStatus?.("closed");
				return;
			}
			this.handlers.onStatus?.("closed");
			this.scheduleReconnect();
		};
	}

	private handle(raw: unknown) {
		if (!raw || typeof raw !== "object") return;
		const msg = raw as {
			ch?: string;
			type?: string;
			sessions?: SessionPatch[];
			id?: string;
			data?: string;
			code?: number;
			cols?: number;
			rows?: number;
			// The daemon reports terminal errors in `error`; older servers used `message`.
			error?: string;
			message?: string;
		};
		if (msg.ch === "sessions" && msg.type === "snapshot") {
			this.handlers.onSessions?.(msg.sessions ?? []);
		} else if (msg.ch === "terminal") {
			const id = msg.id ?? "";
			switch (msg.type) {
				case "data":
					// PTY output arrives base64-encoded; decode to raw bytes for xterm.
					this.handlers.onTerminalData?.(id, base64ToBytes(String(msg.data ?? "")));
					break;
				case "opened":
					this.handlers.onTerminalOpened?.(id);
					break;
				case "exited":
					this.handlers.onTerminalExited?.(id, msg.code ?? 0);
					break;
				case "error":
					this.handlers.onTerminalError?.(id, msg.error ?? msg.message ?? "terminal error");
					break;
				case "resize":
					// Authoritative grid from the daemon (see onTerminalResize).
					if (typeof msg.cols === "number" && typeof msg.rows === "number" && msg.cols > 0 && msg.rows > 0) {
						this.handlers.onTerminalResize?.(id, msg.cols, msg.rows);
					}
					break;
			}
		}
	}

	private scheduleReconnect() {
		if (this.closedByUser) return;
		this.clearReconnect();
		this.reconnectTimer = setTimeout(() => this.open(), this.backoff);
		this.backoff = Math.min(this.backoff * 2, 15000);
	}

	private clearReconnect() {
		if (this.reconnectTimer) {
			clearTimeout(this.reconnectTimer);
			this.reconnectTimer = null;
		}
	}

	private clearPing() {
		if (this.pingTimer) {
			clearInterval(this.pingTimer);
			this.pingTimer = null;
		}
	}

	private sendRaw(frame: string) {
		if (this.ws && this.ws.readyState === WebSocket.OPEN) this.ws.send(frame);
	}

	subscribeSessions() {
		this.subscribed = true;
		this.sendRaw(subscribeFrame(["sessions", "notifications"]));
	}

	openTerminal(id: string, projectId?: string) {
		this.openTerminals.set(id, projectId);
		// The phone is always a follower: it announces role "secondary" so a
		// co-attached desktop drives the shared PTY grid (and the phone renders that
		// grid scaled). When the phone is the only client the daemon falls back to
		// its size, so "secondary" is safe even solo.
		this.sendRaw(openFrame(id, { projectId, role: "secondary" }));
	}

	sendInput(id: string, data: string, projectId?: string) {
		// Keystrokes must be base64-encoded for the daemon (raw bytes over JSON).
		this.sendRaw(dataFrame(id, utf8Encode(data), { projectId }));
	}

	resize(id: string, cols: number, rows: number, projectId?: string) {
		this.sendRaw(resizeFrame(id, cols, rows, { projectId }));
	}

	closeTerminal(id: string, projectId?: string) {
		this.openTerminals.delete(id);
		this.sendRaw(closeFrame(id, { projectId }));
	}

	disconnect() {
		this.closedByUser = true;
		this.clearReconnect();
		this.clearPing();
		try {
			this.ws?.close();
		} catch {
			/* ignore */
		}
		this.ws = null;
	}
}
