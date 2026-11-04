// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { shouldShutdownDaemonForUpdate, shutdownDaemonForUpdate } from "./daemon-update-shutdown";

describe("shouldShutdownDaemonForUpdate", () => {
	const base = { updateRestart: true, appOwned: false, ready: true, port: 3101 };
	it("stops a keep-alive or attached daemon on an update restart", () => {
		expect(shouldShutdownDaemonForUpdate(base)).toBe(true);
	});
	it("leaves the app-owned daemon to the normal quit, and ordinary quits alone", () => {
		expect(shouldShutdownDaemonForUpdate({ ...base, appOwned: true })).toBe(false);
		expect(shouldShutdownDaemonForUpdate({ ...base, updateRestart: false })).toBe(false);
		expect(shouldShutdownDaemonForUpdate({ ...base, ready: false })).toBe(false);
	});
});

describe("shutdownDaemonForUpdate", () => {
	it("sends a bare /shutdown with the token and waits until the daemon is gone", async () => {
		let up = 2;
		const fetchImpl = vi.fn(async (url: string, init?: RequestInit) => {
			if (url.endsWith("/healthz")) {
				if (up-- > 0) return new Response("ok");
				throw new TypeError("fetch failed");
			}
			expect(init?.method).toBe("POST");
			expect(init?.body).toBeUndefined();
			expect((init?.headers as Record<string, string>)["X-VO-Token"] ?? Object.values(init?.headers ?? {})[0]).toBe("tok");
			return new Response("", { status: 200 });
		});
		expect(await shutdownDaemonForUpdate(3101, "tok", fetchImpl as never, 5000, 1)).toBe(true);
		expect(fetchImpl.mock.calls[0]![0]).toBe("http://127.0.0.1:3101/shutdown");
	});

	it("reports a refused shutdown", async () => {
		const fetchImpl = vi.fn(async () => new Response("", { status: 401 }));
		expect(await shutdownDaemonForUpdate(3101, "tok", fetchImpl as never)).toBe(false);
	});
});
