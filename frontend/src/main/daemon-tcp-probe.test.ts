import { createServer, type Server } from "node:net";
import { afterEach, describe, expect, it } from "vitest";
import { probeDaemonTcp } from "./daemon-tcp-probe";

let server: Server | undefined;
afterEach(() => {
	server?.close();
	server = undefined;
});

async function listen(): Promise<number> {
	server = createServer((s) => s.destroy());
	await new Promise<void>((resolve) => server!.listen(0, "127.0.0.1", resolve));
	return (server.address() as { port: number }).port;
}

describe("probeDaemonTcp", () => {
	it("is true while something listens on the port", async () => {
		expect(await probeDaemonTcp(await listen())).toBe(true);
	});
	it("is false once nothing listens (loopback TCP off)", async () => {
		const port = await listen();
		await new Promise<void>((resolve) => server!.close(() => resolve()));
		server = undefined;
		expect(await probeDaemonTcp(port)).toBe(false);
	});
	it("is false with no daemon port", async () => {
		expect(await probeDaemonTcp(undefined)).toBe(false);
	});
});
