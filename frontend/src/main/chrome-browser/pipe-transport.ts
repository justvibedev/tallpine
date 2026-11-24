/**
 * CDP over Chrome's --remote-debugging-pipe (B849 a2): Chrome reads commands
 * on fd 3 and writes responses and events on fd 4, each one JSON message ended
 * by a NUL byte. No TCP port is ever opened.
 */
import type { Readable, Writable } from "node:stream";
import { browserError } from "../browser-view-host/args";

/** One CDP event, from the browser or from a flattened target session. */
export type ChromeEvent = { method: string; params: Record<string, unknown>; sessionId?: string };

export type ChromeConnection = {
	send(method: string, params?: Record<string, unknown>, sessionId?: string): Promise<Record<string, unknown>>;
	/** Subscribes to every CDP event; returns the unsubscribe function. */
	on(listener: (event: ChromeEvent) => void): () => void;
	/** Runs once when the connection closes (Chrome exited or close() was called). */
	onClose(listener: () => void): () => void;
	readonly closed: boolean;
	close(): void;
};

/** Chrome's own CDP messages (screenshots included) stay well below this. */
export const MAX_CHROME_MESSAGE_BYTES = 256 << 20;

export function chromeClosedError(): Error & { code: string } {
	return browserError("BROWSER_CHROME_CLOSED", "Google Chrome closed while the command ran; run it again to reopen Chrome");
}

type Pending = { resolve: (value: Record<string, unknown>) => void; reject: (error: Error) => void };

/**
 * Wraps Chrome's pipe pair. input is Chrome's fd 4 (Chrome writes), output its
 * fd 3 (Chrome reads). A frame longer than maxMessageBytes without its NUL
 * closes the connection instead of buffering without bound.
 */
export function createPipeConnection(
	input: Readable,
	output: Writable,
	options: { maxMessageBytes?: number } = {},
): ChromeConnection {
	const maxMessageBytes = options.maxMessageBytes ?? MAX_CHROME_MESSAGE_BYTES;
	const pending = new Map<number, Pending>();
	const listeners = new Set<(event: ChromeEvent) => void>();
	const closeListeners = new Set<() => void>();
	let buffered: Buffer[] = [];
	let bufferedBytes = 0;
	let nextId = 1;
	let closed = false;

	const close = () => {
		if (closed) return;
		closed = true;
		input.off("data", onData);
		input.off("close", close);
		input.off("error", close);
		output.off("error", close);
		buffered = [];
		for (const request of pending.values()) request.reject(chromeClosedError());
		pending.clear();
		for (const listener of [...closeListeners]) listener();
		closeListeners.clear();
		listeners.clear();
	};

	const dispatch = (frame: Buffer) => {
		let message: Record<string, unknown>;
		try {
			message = JSON.parse(frame.toString("utf8")) as Record<string, unknown>;
		} catch {
			return;
		}
		if (typeof message.id === "number") {
			const request = pending.get(message.id);
			if (!request) return;
			pending.delete(message.id);
			const error = message.error as { message?: unknown } | undefined;
			if (error) {
				request.reject(browserError("BROWSER_CHROME_CDP_ERROR", typeof error.message === "string" ? error.message : "Chrome refused the command"));
			} else {
				request.resolve((message.result as Record<string, unknown> | undefined) ?? {});
			}
			return;
		}
		if (typeof message.method !== "string") return;
		const event: ChromeEvent = {
			method: message.method,
			params: (message.params as Record<string, unknown> | undefined) ?? {},
			...(typeof message.sessionId === "string" ? { sessionId: message.sessionId } : {}),
		};
		for (const listener of [...listeners]) listener(event);
	};

	const onData = (chunk: Buffer | string) => {
		let data = typeof chunk === "string" ? Buffer.from(chunk) : chunk;
		for (let end = data.indexOf(0); end !== -1 && !closed; end = data.indexOf(0)) {
			const frame = Buffer.concat([...buffered, data.subarray(0, end)]);
			buffered = [];
			bufferedBytes = 0;
			data = data.subarray(end + 1);
			if (frame.length > maxMessageBytes) {
				close();
				return;
			}
			dispatch(frame);
		}
		if (closed || data.length === 0) return;
		bufferedBytes += data.length;
		if (bufferedBytes > maxMessageBytes) {
			close();
			return;
		}
		buffered.push(data);
	};

	input.on("data", onData);
	input.on("close", close);
	input.on("error", close);
	output.on("error", close);

	return {
		get closed() {
			return closed;
		},
		send(method, params = {}, sessionId) {
			if (closed) return Promise.reject(chromeClosedError());
			const id = nextId++;
			return new Promise((resolve, reject) => {
				pending.set(id, { resolve, reject });
				const frame = JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) });
				output.write(`${frame}\0`, (error) => {
					if (!error) return;
					pending.delete(id);
					reject(chromeClosedError());
				});
			});
		},
		on(listener) {
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
		onClose(listener) {
			if (closed) {
				listener();
				return () => undefined;
			}
			closeListeners.add(listener);
			return () => closeListeners.delete(listener);
		},
		close,
	};
}
