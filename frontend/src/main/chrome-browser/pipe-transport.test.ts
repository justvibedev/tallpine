import { PassThrough } from "node:stream";
import { describe, expect, it } from "vitest";
import { createPipeConnection } from "./pipe-transport";

function pipes(maxMessageBytes?: number) {
	const fromChrome = new PassThrough();
	const toChrome = new PassThrough();
	const written: string[] = [];
	toChrome.on("data", (chunk: Buffer) => written.push(chunk.toString()));
	return { fromChrome, written, connection: createPipeConnection(fromChrome, toChrome, { maxMessageBytes }) };
}

const flush = () => new Promise((resolve) => setImmediate(resolve));

describe("createPipeConnection", () => {
	it("writes each command as JSON ended by NUL, with the target session", async () => {
		const { written, connection } = pipes();
		void connection.send("Page.navigate", { url: "https://example.com" }, "S1");
		await flush();
		expect(written.join("")).toBe(`${JSON.stringify({ id: 1, method: "Page.navigate", params: { url: "https://example.com" }, sessionId: "S1" })}\0`);
	});

	it("splits responses and events on NUL, across and within chunks", async () => {
		const { fromChrome, connection } = pipes();
		const events: unknown[] = [];
		connection.on((event) => events.push(event));
		const first = connection.send("Browser.getVersion");
		const second = connection.send("Target.getTargets");
		const frames = `${JSON.stringify({ id: 2, result: { targetInfos: [] } })}\0${JSON.stringify({ method: "Target.targetCreated", params: { a: 1 }, sessionId: "S" })}\0${JSON.stringify({ id: 1, result: { product: "Chrome" } })}\0`;
		const cut = 7;
		fromChrome.write(frames.slice(0, cut));
		await flush();
		fromChrome.write(frames.slice(cut));
		await expect(first).resolves.toEqual({ product: "Chrome" });
		await expect(second).resolves.toEqual({ targetInfos: [] });
		expect(events).toEqual([{ method: "Target.targetCreated", params: { a: 1 }, sessionId: "S" }]);
	});

	it("rejects a CDP error response with its message", async () => {
		const { fromChrome, connection } = pipes();
		const request = connection.send("Nope.method");
		fromChrome.write(`${JSON.stringify({ id: 1, error: { message: "'Nope.method' wasn't found" } })}\0`);
		await expect(request).rejects.toMatchObject({ code: "BROWSER_CHROME_CDP_ERROR", message: "'Nope.method' wasn't found" });
	});

	it("closes on a frame over the size limit and fails pending commands BROWSER_CHROME_CLOSED", async () => {
		const { fromChrome, connection } = pipes(64);
		let closed = false;
		connection.onClose(() => {
			closed = true;
		});
		const pending = connection.send("Browser.getVersion");
		fromChrome.write("x".repeat(40));
		await flush();
		expect(connection.closed).toBe(false);
		fromChrome.write("y".repeat(40));
		await expect(pending).rejects.toMatchObject({ code: "BROWSER_CHROME_CLOSED" });
		expect(closed).toBe(true);
		await expect(connection.send("Browser.getVersion")).rejects.toMatchObject({ code: "BROWSER_CHROME_CLOSED" });
	});

	it("closes when Chrome's end of the pipe closes", async () => {
		const { fromChrome, connection } = pipes();
		const pending = connection.send("Browser.getVersion");
		fromChrome.destroy();
		await expect(pending).rejects.toMatchObject({ code: "BROWSER_CHROME_CLOSED" });
		expect(connection.closed).toBe(true);
	});
});
