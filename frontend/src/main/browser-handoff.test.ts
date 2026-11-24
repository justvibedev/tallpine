import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BrowserHandoffs, createAppBrowserHandoffs } from "./browser-handoff";
import { compileUrlGlob, type BrowserHandoffChange } from "../shared/browser-handoff";

const args = { reason: "Enter the 2FA code", timeoutMs: 60_000 };

function setup(url = "https://login.example/2fa") {
	const changes: BrowserHandoffChange[] = [];
	const handoffs = new BrowserHandoffs((change) => changes.push(change), () => 1_000);
	const page = { current: url as string | undefined, url: () => page.current };
	return { changes, handoffs, page };
}

describe("BrowserHandoffs", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it("shows the banner and returns when the person clicks Continue", async () => {
		const { changes, handoffs, page } = setup();
		const run = handoffs.run("s1", { ...args, reason: "  Enter the 2FA code " }, page);
		const state = handoffs.state("s1");
		expect(state).toMatchObject({ sessionId: "s1", reason: "Enter the 2FA code", deadline: 61_000 });
		expect(changes).toEqual([{ sessionId: "s1", state }]);
		expect(handoffs.continue("s1", "other-id")).toBe(false);
		expect(handoffs.continue("s2", state!.id)).toBe(false);
		page.current = "https://app.example/home";
		expect(handoffs.continue("s1", state!.id)).toBe(true);
		await expect(run).resolves.toEqual({ outcome: "continued", url: "https://app.example/home" });
		expect(handoffs.state("s1")).toBeNull();
		expect(changes.at(-1)).toEqual({ sessionId: "s1", state: null });
	});

	it("a Google sign-in page ends the handoff when the person is done in plain Chrome, and releases it however it ends (B854)", async () => {
		const { changes, handoffs } = setup();
		let finish = () => {};
		const release = vi.fn(async () => undefined);
		const page = { url: () => undefined, signIn: true, ended: new Promise<void>((resolve) => { finish = resolve; }), release };
		const run = handoffs.run("s1", args, page);
		expect(handoffs.state("s1")).toMatchObject({ signIn: true });
		expect(changes[0].state).toMatchObject({ signIn: true });
		finish();
		await expect(run).resolves.toEqual({ outcome: "continued", url: "" });
		expect(release).toHaveBeenCalledTimes(1);
		expect(handoffs.state("s1")).toBeNull();

		const again = { url: () => undefined, signIn: true, ended: new Promise<void>(() => {}), release: vi.fn(async () => undefined) };
		const second = handoffs.run("s1", args, again);
		expect(handoffs.continue("s1", handoffs.state("s1")!.id)).toBe(true);
		await second;
		expect(again.release).toHaveBeenCalledTimes(1);
	});

	it("releases the page when the handoff is refused before it starts (B854)", async () => {
		const { handoffs } = setup();
		const page = () => ({ url: () => undefined, signIn: true, release: vi.fn(async () => undefined) });
		const bad = page();
		await expect(handoffs.run("s1", { reason: "x", timeoutMs: 1 }, bad)).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		expect(bad.release).toHaveBeenCalledTimes(1);
		const canceled = page();
		const abort = new AbortController();
		abort.abort();
		await expect(handoffs.run("s1", args, canceled, abort.signal)).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(canceled.release).toHaveBeenCalledTimes(1);
		const first = handoffs.run("s1", args, { url: () => undefined });
		const second = page();
		await expect(handoffs.run("s1", args, second)).rejects.toMatchObject({ code: "BROWSER_HANDOFF_ACTIVE" });
		expect(second.release).toHaveBeenCalledTimes(1);
		expect(() => handoffs.check("s1", args)).toThrow(/already waiting/);
		handoffs.continue("s1", handoffs.state("s1")!.id);
		await first;
		expect(handoffs.check("s1", args)).toMatchObject({ reason: "Enter the 2FA code", timeoutMs: 60_000 });
	});

	it("returns once the page navigates to a URL matching --until-url", async () => {
		const { handoffs, page } = setup("https://app.example/login");
		let settled = false;
		const run = handoffs.run("s1", { ...args, untilUrl: "https://app.example/*" }, page).finally(() => { settled = true; });
		// The start page already matches but has not moved: keep waiting.
		await vi.advanceTimersByTimeAsync(2_000);
		expect(settled).toBe(false);
		page.current = "https://login.example/2fa";
		await vi.advanceTimersByTimeAsync(600);
		expect(settled).toBe(false);
		page.current = "https://app.example/dashboard";
		await vi.advanceTimersByTimeAsync(600);
		await expect(run).resolves.toEqual({ outcome: "url-matched", url: "https://app.example/dashboard" });
	});

	it("fails with BROWSER_HANDOFF_TIMEOUT and clears the banner", async () => {
		const { changes, handoffs, page } = setup();
		const run = handoffs.run("s1", { ...args, timeoutMs: 10_000 }, page);
		const outcome = expect(run).rejects.toMatchObject({ code: "BROWSER_HANDOFF_TIMEOUT" });
		await vi.advanceTimersByTimeAsync(10_000);
		await outcome;
		expect(handoffs.state("s1")).toBeNull();
		expect(changes.at(-1)).toEqual({ sessionId: "s1", state: null });
	});

	it("ends when the daemon cancels the command", async () => {
		const { handoffs, page } = setup();
		const controller = new AbortController();
		const run = handoffs.run("s1", args, page, controller.signal);
		controller.abort();
		await expect(run).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(handoffs.state("s1")).toBeNull();
	});

	it("runs one handoff per session and leaves the first one's banner up", async () => {
		const { handoffs, page } = setup();
		const first = handoffs.run("s1", args, page);
		const id = handoffs.state("s1")!.id;
		await expect(handoffs.run("s1", args, page)).rejects.toMatchObject({ code: "BROWSER_HANDOFF_ACTIVE" });
		expect(handoffs.state("s1")?.id).toBe(id);
		const other = handoffs.run("s2", args, page);
		handoffs.continue("s1", id);
		handoffs.continue("s2", handoffs.state("s2")!.id);
		await expect(first).resolves.toMatchObject({ outcome: "continued" });
		await expect(other).resolves.toMatchObject({ outcome: "continued" });
	});

	it("rejects invalid arguments without showing a banner", async () => {
		const { changes, handoffs, page } = setup();
		for (const bad of [{ timeoutMs: 60_000 }, { reason: " ", timeoutMs: 60_000 }, { reason: "x" }, { reason: "x", timeoutMs: 9_999 }, { reason: "x", timeoutMs: 1_800_001 }, { reason: "x", timeoutMs: 10_000.5 }]) {
			await expect(handoffs.run("s1", bad, page)).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		}
		expect(changes).toEqual([]);
	});
});

describe("createAppBrowserHandoffs", () => {
	it("answers only the shell's state and Continue requests", async () => {
		const handlers = new Map<string, (event: { sender: unknown }, ...values: unknown[]) => unknown>();
		const ipcMain = { handle: vi.fn((channel: string, handler: never) => { handlers.set(channel, handler); }), removeHandler: vi.fn() };
		const shell = { send: vi.fn(), isDestroyed: () => false };
		const app = createAppBrowserHandoffs({ ipcMain: ipcMain as never, shellWebContents: shell as never });
		const run = app.handoffs.run("s1", args, { url: () => "https://a.example/" });
		const state = handlers.get("browser:handoffState")!;
		const cont = handlers.get("browser:handoffContinue")!;
		expect(shell.send).toHaveBeenCalledWith("browser:handoffChanged", { sessionId: "s1", state: app.handoffs.state("s1") });
		expect(state({ sender: {} }, "s1")).toBeNull();
		const current = state({ sender: shell }, "s1") as { id: string };
		expect(current.id).toBeTruthy();
		expect(cont({ sender: {} }, "s1", current.id)).toBe(false);
		expect(cont({ sender: shell }, "s1", current.id)).toBe(true);
		await expect(run).resolves.toMatchObject({ outcome: "continued" });
		app.dispose();
		expect(ipcMain.removeHandler).toHaveBeenCalledWith("browser:handoffState");
		expect(ipcMain.removeHandler).toHaveBeenCalledWith("browser:handoffContinue");
	});
});

describe("closing the window", () => {
	it("ends every running handoff so the agent's command returns", async () => {
		const ipcMain = { handle: vi.fn(), removeHandler: vi.fn() };
		let destroyed = false;
		const shell = { send: vi.fn(), isDestroyed: () => destroyed };
		const app = createAppBrowserHandoffs({ ipcMain: ipcMain as never, shellWebContents: shell as never });
		const page = { url: () => "https://a.example/" };
		const first = app.handoffs.run("s1", args, page);
		const second = app.handoffs.run("s2", args, page);
		destroyed = true;
		app.dispose();
		await expect(first).rejects.toMatchObject({ code: "BROWSER_TARGET_UNAVAILABLE" });
		await expect(second).rejects.toMatchObject({ code: "BROWSER_TARGET_UNAVAILABLE" });
		expect(app.handoffs.state("s1")).toBeNull();
		expect(app.handoffs.state("s2")).toBeNull();
	});
});

describe("compileUrlGlob", () => {
	it("matches the whole URL with * and ? as the only wildcards", () => {
		const glob = compileUrlGlob("https://app.example/dash*");
		expect(glob("https://app.example/dashboard?x=1")).toBe(true);
		expect(glob("https://app.example/home")).toBe(false);
		expect(glob("http://evil.test/?https://app.example/dash")).toBe(false);
		expect(compileUrlGlob("https://a.example/?")("https://a.example/x")).toBe(true);
		expect(compileUrlGlob("https://a.example/?")("https://a.example/xy")).toBe(false);
		// Regex metacharacters are literal.
		expect(compileUrlGlob("https://a.example/(x)+.html")("https://a.example/(x)+.html")).toBe(true);
		expect(compileUrlGlob("https://a.example/(x)+.html")("https://a.example/xx.html")).toBe(false);
	});
});
