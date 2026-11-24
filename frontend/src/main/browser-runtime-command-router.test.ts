import { describe, expect, it, vi } from "vitest";
import { BrowserHandoffs } from "./browser-handoff";
import { routeBrowserRuntimeCommand } from "./browser-runtime-command-router";
import { setupChromeHost } from "./chrome-browser/chrome-test-helpers";

function hosts() {
	return {
		viewHost: { execute: vi.fn(async () => ({ from: "electron" })) },
		chromeHost: { execute: vi.fn(async () => ({ from: "chrome" })), destroySession: vi.fn(async () => undefined) },
	};
}

const signal = new AbortController().signal;

describe("routeBrowserRuntimeCommand", () => {
	it("keeps commands without a backend, or electron, on the in-app browser unchanged", async () => {
		for (const backend of [undefined, "electron", ""]) {
			const h = hosts();
			const args = { ref: "e1" };
			await expect(routeBrowserRuntimeCommand({ sessionId: "s1", action: "click", args, backend }, signal, h)).resolves.toEqual({ from: "electron" });
			expect(h.viewHost.execute).toHaveBeenCalledWith("s1", "click", args, signal);
			expect(h.chromeHost.execute).not.toHaveBeenCalled();
		}
	});

	it("sends backend=chrome commands to the Chrome host only", async () => {
		const h = hosts();
		await expect(routeBrowserRuntimeCommand({ sessionId: "s1", action: "snapshot", args: {}, backend: "chrome" }, signal, h)).resolves.toEqual({ from: "chrome" });
		expect(h.chromeHost.execute).toHaveBeenCalledWith("s1", "snapshot", {}, signal);
		expect(h.viewHost.execute).not.toHaveBeenCalled();
	});

	it("fails a chrome command when no Chrome host is open, never falling back to Electron", async () => {
		const h = hosts();
		await expect(routeBrowserRuntimeCommand({ sessionId: "s1", action: "snapshot", backend: "chrome" }, signal, { ...h, chromeHost: null }))
			.rejects.toMatchObject({ code: "BROWSER_TARGET_UNAVAILABLE" });
		expect(h.viewHost.execute).not.toHaveBeenCalled();
	});

	it("tears a session down in both browsers", async () => {
		const h = hosts();
		h.chromeHost.destroySession.mockRejectedValueOnce(new Error("Chrome gone"));
		await routeBrowserRuntimeCommand({ sessionId: "s1", action: "__destroy-session" }, signal, h);
		expect(h.chromeHost.destroySession).toHaveBeenCalledWith("s1");
		expect(h.viewHost.execute).toHaveBeenCalledWith("s1", "__destroy-session", undefined, signal);
	});

	it("runs handoff on the session's backend page through the handoff registry", async () => {
		for (const backend of ["electron", "chrome"] as const) {
			const page = { url: () => "https://a.example/" };
			const view = { execute: vi.fn(), handoffPage: vi.fn(async () => page) };
			const chrome = { execute: vi.fn(), destroySession: vi.fn(), handoffPage: vi.fn(async () => page) };
			const handoffs = { check: vi.fn(), run: vi.fn(async () => ({ outcome: "continued" as const, url: "https://a.example/" })) };
			const args = { reason: "Enter the code", timeoutMs: 60_000 };
			await expect(routeBrowserRuntimeCommand({ sessionId: "s1", action: "handoff", args, backend }, signal, { viewHost: view, chromeHost: chrome, handoffs }))
				.resolves.toEqual({ outcome: "continued", url: "https://a.example/" });
			const [used, unused] = backend === "chrome" ? [chrome, view] : [view, chrome];
			expect(used.handoffPage).toHaveBeenCalledWith("s1", signal);
			expect(unused.handoffPage).not.toHaveBeenCalled();
			expect(handoffs.run).toHaveBeenCalledWith("s1", args, page, signal);
			expect(view.execute).not.toHaveBeenCalled();
			expect(chrome.execute).not.toHaveBeenCalled();
		}
	});

	it("a handoff refused on a Google sign-in page never opens plain Chrome, nor leaves the agent's Chrome away (B854)", async () => {
		const { host, process, connection } = setupChromeHost();
		await host.execute("s1", "open", { url: "example.com" });
		connection().emit({
			method: "Target.targetInfoChanged",
			params: { targetInfo: { targetId: "T1", type: "page", url: "https://accounts.google.com/v3/signin/identifier?continue=https%3A%2F%2Fa.example%2F", title: "Sign in" } },
		});
		const handoffs = new BrowserHandoffs(() => undefined);
		const hosts = { viewHost: null, chromeHost: host, handoffs };
		const route = (args: Record<string, unknown>, abort = signal) =>
			routeBrowserRuntimeCommand({ sessionId: "s1", action: "handoff", args, backend: "chrome" }, abort, hosts);
		await expect(route({ reason: "Sign in to Google", timeoutMs: 5_000 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		await expect(route({ timeoutMs: 60_000 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		const aborted = new AbortController();
		aborted.abort();
		await expect(route({ reason: "Sign in to Google", timeoutMs: 60_000 }, aborted.signal)).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(process.signIn).not.toHaveBeenCalled();
		expect(host.state("s1")).toMatchObject({ running: true, signingIn: false });

		const running = route({ reason: "Sign in to Google", timeoutMs: 60_000 });
		await vi.waitFor(() => expect(handoffs.state("s1")).not.toBeNull());
		expect(process.signIn).toHaveBeenCalledTimes(1);
		await expect(route({ reason: "Again", timeoutMs: 60_000 })).rejects.toMatchObject({ code: "BROWSER_HANDOFF_ACTIVE" });
		// The refused second handoff leaves the person's sign-in alone.
		expect(process.finishSignIn).not.toHaveBeenCalled();
		await host.finishSignIn("s1");
		await expect(running).resolves.toMatchObject({ outcome: "continued" });
	});

	it("fails handoff when no window holds the handoff registry", async () => {
		const view = { execute: vi.fn(), handoffPage: vi.fn() };
		await expect(routeBrowserRuntimeCommand({ sessionId: "s1", action: "handoff", args: {} }, signal, { viewHost: view, chromeHost: null, handoffs: null }))
			.rejects.toMatchObject({ code: "BROWSER_TARGET_UNAVAILABLE" });
		expect(view.handoffPage).not.toHaveBeenCalled();
	});
});
