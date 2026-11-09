import { describe, expect, it, vi } from "vitest";
import { DAEMON_TCP_OFF_ERROR } from "../shared/daemon-url";
import { isAllowedAppExternalURL, openGatedExternalURL } from "./external-open";

describe("isAllowedAppExternalURL", () => {
	it("allows web and mail handoff URLs from the app renderer", () => {
		expect(isAllowedAppExternalURL("https://github.com/justvibedev/tallpine/issues/new")).toBe(true);
		expect(isAllowedAppExternalURL("http://localhost:5173/help")).toBe(true);
		expect(isAllowedAppExternalURL("mailto:user@example.com?subject=Tallpine%20feedback")).toBe(true);
	});

	it("blocks local, privileged, and script schemes", () => {
		expect(isAllowedAppExternalURL("file:///Users/alice/private.txt")).toBe(false);
		expect(isAllowedAppExternalURL("app://renderer/index.html")).toBe(false);
		expect(isAllowedAppExternalURL("javascript:alert(1)")).toBe(false);
	});

	const gate = (open: boolean) => ({ port: () => 3101, tcpOpen: vi.fn().mockResolvedValue(open) });

	it("opens allowed URLs through the native shell opener", async () => {
		const openExternal = vi.fn().mockResolvedValue(undefined);

		await openGatedExternalURL("mailto:user@example.com?subject=Tallpine%20feedback", { openExternal }, gate(true));

		expect(openExternal).toHaveBeenCalledWith("mailto:user@example.com?subject=Tallpine%20feedback");
	});

	it("rejects unsupported URLs before reaching the shell opener", async () => {
		const openExternal = vi.fn().mockResolvedValue(undefined);

		await expect(openGatedExternalURL("file:///Users/alice/private.txt", { openExternal }, gate(true))).rejects.toThrow(
			"Unsupported external URL",
		);
		expect(openExternal).not.toHaveBeenCalled();
	});

	it("does not open daemon URLs when loopback TCP is off", async () => {
		const openExternal = vi.fn().mockResolvedValue(undefined);

		for (const url of ["http://127.0.0.1:3101/x", "http://localhost:3101/", "http://abc.localhost:3101/p", "http://[::1]:3101/"]) {
			await expect(openGatedExternalURL(url, { openExternal }, gate(false))).rejects.toThrow(DAEMON_TCP_OFF_ERROR);
		}
		expect(openExternal).not.toHaveBeenCalled();
	});

	it("opens daemon URLs when TCP is on, and other URLs even when it is off", async () => {
		const openExternal = vi.fn().mockResolvedValue(undefined);

		await openGatedExternalURL("http://127.0.0.1:3101/x", { openExternal }, gate(true));
		await openGatedExternalURL("http://127.0.0.1:5173/x", { openExternal }, gate(false));
		await openGatedExternalURL("https://example.com/", { openExternal }, gate(false));
		expect(openExternal).toHaveBeenCalledTimes(3);
	});
});
