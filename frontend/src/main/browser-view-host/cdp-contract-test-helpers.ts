/**
 * The CDP-direct agent browser command contract (network capture, console,
 * errors, viewport), run against both backends: the in-app browser's Electron
 * debugger and a Chrome mode session (B849). Each backend supplies a harness;
 * the assertions are the same, so the two hosts cannot drift apart. The page
 * commands (back/forward/reload/eval/upload, B857) are in
 * page-cdp-contract-test-helpers.ts.
 */
import { describe, expect, it, vi } from "vitest";

export type NativeMessages = (action: string) => Record<string, unknown> | undefined;

export type CdpContractHarness = {
	execute(action: string, args?: Record<string, unknown>): Promise<unknown>;
	/** A CDP event from the page's own debugger session. */
	emit(method: string, params: Record<string, unknown>): void;
	/** Methods sent to the page's debugger, with their params. */
	commands(): { method: string; params?: Record<string, unknown> }[];
	/** Answers a CDP method sent to the page's debugger. */
	respond(method: string, answer: (params: Record<string, unknown>) => unknown): void;
};

export function describeCdpCommandContract(backend: string, make: (native?: NativeMessages) => CdpContractHarness | Promise<CdpContractHarness>): void {
	const methods = (harness: CdpContractHarness) => harness.commands().map((command) => command.method);

	describe(`CDP-direct agent browser commands (${backend})`, () => {
		it("network capture is opt-in and exposes only sanitized request metadata", async () => {
			const harness = await make();
			harness.emit("Network.requestWillBeSent", { requestId: "before-start", request: { method: "GET", url: "https://example.test/unobserved" } });
			expect(await harness.execute("network-list")).toMatchObject({ active: false, requestCount: 0, requests: [] });

			expect(await harness.execute("network-start", { durationSeconds: 30 })).toMatchObject({
				active: true,
				metadataOnly: true,
				untrustedExternalContent: true,
				requestCount: 0,
				maxEntries: 200,
			});
			expect(methods(harness)).toContain("Network.enable");
			expect(await harness.execute("network-start", { durationSeconds: 30 })).toMatchObject({ active: true, alreadyActive: true });

			harness.emit("Network.requestWillBeSent", {
				requestId: "request-1",
				timestamp: 12,
				wallTime: 1_750_000_000,
				type: "XHR",
				request: {
					method: "POST",
					url: "https://user:password@api.example.test/items?token=secret&page=2#private",
					postData: "must-not-be-stored",
					headers: { Authorization: "Bearer very-secret", Cookie: "session=very-secret", "Content-Type": "application/json" },
				},
			});
			harness.emit("Network.responseReceived", {
				requestId: "request-1",
				response: { status: 401, statusText: "Unauthorized", headers: { "Set-Cookie": "session=server-secret", "Content-Type": "application/json" } },
			});
			harness.emit("Network.loadingFinished", { requestId: "request-1", timestamp: 12.125 });

			const result = (await harness.execute("network-list")) as { requestCount: number; requests: Array<Record<string, unknown>> };
			expect(result.requestCount).toBe(1);
			expect(result.requests[0]).toMatchObject({
				id: "n1",
				method: "POST",
				resourceType: "xhr",
				status: 401,
				durationMs: 125,
				requestHeaders: { "content-type": "application/json" },
				responseHeaders: { "content-type": "application/json" },
			});
			const text = JSON.stringify(result);
			for (const secret of ["very-secret", "server-secret", "must-not-be-stored", "password"]) expect(text).not.toContain(secret);
			expect(result.requests[0]?.url).toContain("token=%5Bredacted%5D");
			expect(await harness.execute("network-status")).toMatchObject({ active: true, requestCount: 1 });

			expect(await harness.execute("network-clear")).toMatchObject({ active: true, requestCount: 0 });
			harness.emit("Network.requestWillBeSent", { requestId: "request-2", request: { method: "GET", url: "https://api.example.test/next" } });
			expect(await harness.execute("network-stop")).toMatchObject({ active: false, stopReason: "stopped", requestCount: 1 });
			harness.emit("Network.requestWillBeSent", { requestId: "after-stop", request: { method: "GET", url: "https://api.example.test/late" } });
			expect(await harness.execute("network-list")).toMatchObject({ active: false, requestCount: 1 });
			// The debugger attachment is shared with the native runtime: stopping never disables the domain.
			expect(methods(harness)).not.toContain("Network.disable");
		});

		it("network capture validates its duration and expires on its own", async () => {
			vi.useFakeTimers();
			try {
				const harness = await make();
				await expect(harness.execute("network-start", { durationSeconds: 0 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
				await expect(harness.execute("network-start", { durationSeconds: 301 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
				expect(await harness.execute("network-status")).toMatchObject({ active: false });
				expect(methods(harness)).not.toContain("Network.enable");
				await harness.execute("network-start", { durationSeconds: 1 });
				await vi.advanceTimersByTimeAsync(1_000);
				expect(await harness.execute("network-status")).toMatchObject({ active: false, stopReason: "expired" });
			} finally {
				vi.useRealTimers();
			}
		});

		it("viewport emulates and resets device metrics and rejects invalid sizes first", async () => {
			const harness = await make();
			await expect(harness.execute("viewport", {})).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
			await expect(harness.execute("viewport", { width: "x", height: 10 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
			await expect(harness.execute("viewport", { width: 10, height: 10, deviceScaleFactor: -1 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
			expect(methods(harness)).not.toContain("Emulation.setDeviceMetricsOverride");

			const set = await harness.execute("viewport", { width: 390.4, height: 20000, deviceScaleFactor: 12, mobile: true });
			expect(set).toMatchObject({ width: 390, height: 10000, deviceScaleFactor: 8, mobile: true, target: expect.anything() });
			expect(harness.commands()).toContainEqual({ method: "Emulation.setDeviceMetricsOverride", params: { width: 390, height: 10000, deviceScaleFactor: 8, mobile: true } });
			expect(await harness.execute("viewport", { reset: true })).toMatchObject({ reset: true });
			expect(methods(harness).at(-1)).toBe("Emulation.clearDeviceMetricsOverride");
		});

		it("console and errors return the native page messages, normalized and marked untrusted", async () => {
			const harness = await make((action) =>
				action === "console"
					? { messages: [{ type: "warning", text: "see https://user:pw@example.test/?token=abc" }, "plain line"] }
					: action === "errors"
						? { messages: [{ message: "Uncaught TypeError: boom" }, "Failed to load resource"] }
						: undefined,
			);
			const consoleResult = (await harness.execute("console")) as { messages: { level: string; message: string }[]; untrustedExternalContent: boolean };
			expect(consoleResult.untrustedExternalContent).toBe(true);
			expect(consoleResult.messages.map((message) => message.level)).toEqual(["warning", "log"]);
			expect(consoleResult.messages[0]?.message).not.toContain("pw@");
			expect(consoleResult.messages[0]?.message).not.toContain("token=abc");
			const errors = (await harness.execute("errors")) as { messages: { level: string; message: string }[] };
			expect(errors.messages.map((message) => message.level)).toEqual(["error", "error"]);
			expect(errors.messages[0]?.message).toContain("Uncaught TypeError: boom");
		});
	});
}
