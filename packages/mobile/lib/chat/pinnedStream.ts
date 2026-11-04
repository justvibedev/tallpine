// expo/fetch runs on its own native session, which never consults the pinning
// handlers, so a pinned (self-signed) gateway would fail CA validation there.
// React Native's XHR goes through the pinned handler on both platforms and
// reports body growth incrementally, which is all the SSE reader needs.

type XhrLike = {
	open(method: string, url: string): void;
	setRequestHeader(name: string, value: string): void;
	send(): void;
	abort(): void;
	getResponseHeader(name: string): string | null;
	readonly status: number;
	readonly statusText: string;
	readonly responseText: string;
	readonly readyState: number;
	onreadystatechange: (() => void) | null;
	onprogress: (() => void) | null;
	onload: (() => void) | null;
	onerror: (() => void) | null;
	onabort: (() => void) | null;
	ontimeout: (() => void) | null;
};

export type XhrFactory = () => XhrLike;

export type StreamResponse = {
	ok: boolean;
	status: number;
	statusText: string;
	headers: { get(name: string): string | null };
	body: { getReader(): { read(): Promise<{ done: boolean; value?: Uint8Array }>; cancel(): Promise<void>; releaseLock(): void } };
	json(): Promise<unknown>;
};

/**
 * RN's XHR keeps every chunk in responseText, so a stream that stays open grows
 * without bound. Past this much, end the stream: the caller reconnects from its
 * cursor, which starts a fresh request with an empty buffer.
 */
export const MAX_BUFFERED_CHARS = 2_000_000;

export function pinnedEventFetch(
	url: string,
	init: { headers: Record<string, string>; signal: AbortSignal },
	create: XhrFactory = () => new XMLHttpRequest() as unknown as XhrLike,
): Promise<StreamResponse> {
	return new Promise((resolve, reject) => {
		const xhr = create();
		const encoder = new TextEncoder();
		let consumed = 0;
		let finished = false;
		let failure: Error | null = null;
		let wake: (() => void) | null = null;
		let opened = false;
		const notify = () => { const w = wake; wake = null; w?.(); };
		const fail = (message: string) => {
			failure = new Error(message);
			if (!opened) reject(failure);
			notify();
		};
		xhr.open("GET", url);
		for (const [name, value] of Object.entries(init.headers)) xhr.setRequestHeader(name, value);
		const reader = {
			async read() {
				for (;;) {
					if (failure) throw failure;
					const text = xhr.responseText;
					if (text.length > consumed) {
						const chunk = text.slice(consumed);
						consumed = text.length;
						if (consumed > MAX_BUFFERED_CHARS) { finished = true; xhr.abort(); }

						return { done: false, value: encoder.encode(chunk) };
					}
					if (finished) return { done: true as const };
					await new Promise<void>((r) => { wake = r; });
				}
			},
			async cancel() { finished = true; xhr.abort(); },
			releaseLock() {},
		};
		const open = () => {
			if (opened || xhr.readyState < 2) return;
			opened = true;
			resolve({
				ok: xhr.status >= 200 && xhr.status < 300,
				status: xhr.status,
				statusText: xhr.statusText,
				headers: { get: (name) => xhr.getResponseHeader(name) },
				body: { getReader: () => reader },
				json: async () => JSON.parse(xhr.responseText),
			});
		};
		xhr.onreadystatechange = open;
		xhr.onprogress = () => { open(); notify(); };
		xhr.onload = () => { open(); finished = true; notify(); };
		xhr.onerror = () => fail("Couldn't open live updates from your computer.");
		xhr.ontimeout = xhr.onerror;
		xhr.onabort = () => { finished = true; notify(); };
		const onAbort = () => xhr.abort();
		if (init.signal.aborted) { reject(new DOMException("Aborted", "AbortError")); return; }
		init.signal.addEventListener("abort", onAbort, { once: true });
		xhr.send();
	});
}
