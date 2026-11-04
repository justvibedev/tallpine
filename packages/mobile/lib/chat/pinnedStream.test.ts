import { describe, expect, it } from "vitest";
import { MAX_BUFFERED_CHARS, pinnedEventFetch, type XhrFactory } from "./pinnedStream";

type Fake = ReturnType<XhrFactory> & { feed(text: string, done?: boolean): void; respond(status: number): void; onerror: (() => void) | null };

function fakeXhr(headers: Record<string, string> = {}): Fake {
	const x = {
		readyState: 1, status: 0, statusText: "", responseText: "",
		onreadystatechange: null as (() => void) | null, onprogress: null as (() => void) | null, onload: null as (() => void) | null, onerror: null as (() => void) | null, onabort: null as (() => void) | null, ontimeout: null as (() => void) | null,
		sent: [] as string[],
		open() {}, setRequestHeader(n: string, v: string) { this.sent.push(`${n}:${v}`); }, send() {},
		abort() { this.onabort?.(); },
		getResponseHeader: (n: string) => headers[n] ?? null,
		respond(status: number) { this.status = status; this.readyState = 2; this.onreadystatechange?.(); },
		feed(text: string, done = false) { this.responseText += text; this.readyState = done ? 4 : 3; this.onprogress?.(); if (done) this.onload?.(); },
	};
	return x as unknown as Fake;
}

describe("pinnedEventFetch", () => {
	it("streams incremental text and exposes headers", async () => {
		const x = fakeXhr({ "X-AO-Event-After": "7" });
		const p = pinnedEventFetch("https://h/e", { headers: { A: "b" }, signal: new AbortController().signal }, () => x);
		x.respond(200);
		const res = await p;
		expect(res.ok).toBe(true);
		expect(res.headers.get("X-AO-Event-After")).toBe("7");
		const r = res.body.getReader();
		const first = r.read();
		x.feed("id: 1\n\n");
		expect(new TextDecoder().decode((await first).value)).toBe("id: 1\n\n");
		const second = r.read();
		x.feed("id: 2\n\n", true);
		expect(new TextDecoder().decode((await second).value)).toBe("id: 2\n\n");
		expect((await r.read()).done).toBe(true);
	});

	it("rejects when the connection fails before headers", async () => {
		const x = fakeXhr();
		const p = pinnedEventFetch("https://h/e", { headers: {}, signal: new AbortController().signal }, () => x);
		(x as Fake).onerror?.();
		await expect(p).rejects.toThrow(/live updates/);
	});

	it("reports non-2xx as not ok and aborts via the signal", async () => {
		const x = fakeXhr();
		const ac = new AbortController();
		const p = pinnedEventFetch("https://h/e", { headers: {}, signal: ac.signal }, () => x);
		x.respond(401);
		expect((await p).ok).toBe(false);
		ac.abort();
	});

	it("ends the stream and aborts once the XHR has buffered too much", async () => {
		const x = fakeXhr();
		let aborted = false;
		const abort = x.abort.bind(x);
		x.abort = () => { aborted = true; abort(); };
		const p = pinnedEventFetch("https://h/e", { headers: {}, signal: new AbortController().signal }, () => x);
		x.respond(200);
		const r = (await p).body.getReader();
		x.feed("x".repeat(MAX_BUFFERED_CHARS + 1));
		const big = await r.read();
		expect(big.done).toBe(false);
		expect(aborted).toBe(true);
		expect((await r.read()).done).toBe(true);
	});
});
