import { describe, expect, it, vi } from "vitest";
import { guardIpcMain, isTrustedSender, originOf } from "./ipc-guard";

const shell = { id: 1 } as never;
const other = { id: 2 } as never;
const options = { getShellWebContents: () => shell, allowedOrigins: ["app://renderer", "http://localhost:5173"] };
const ev = (sender: unknown, url?: string) => ({ sender, senderFrame: url === undefined ? null : { url } }) as never;

function fakeIpc() {
	const handlers = new Map<string, (...a: unknown[]) => unknown>();
	return {
		handlers,
		handle: (c: string, f: never) => void handlers.set(c, f),
		on: (c: string, f: never) => void handlers.set(`on:${c}`, f),
		once: vi.fn(),
		off: vi.fn(),
		removeListener: vi.fn(),
		removeHandler: vi.fn(),
	};
}

describe("ipc guard", () => {
	it("parses custom-scheme origins", () => {
		expect(originOf("app://renderer/x/y")).toBe("app://renderer");
		expect(originOf("garbage")).toBeNull();
	});

	it("allows the shell on the app origin and the dev server", () => {
		expect(isTrustedSender(ev(shell, "app://renderer/"), "handle", "daemon:stop", options)).toBe(true);
		expect(isTrustedSender(ev(shell, "http://localhost:5173/x"), "handle", "daemon:stop", options)).toBe(true);
	});

	it("blocks other webContents, foreign frames, and missing frames", () => {
		expect(isTrustedSender(ev(other, "app://renderer/"), "handle", "daemon:stop", options)).toBe(false);
		expect(isTrustedSender(ev(shell, "https://evil.example/"), "handle", "daemon:stop", options)).toBe(false);
		expect(isTrustedSender(ev(shell), "handle", "daemon:stop", options)).toBe(false);
		expect(isTrustedSender(ev(shell, "app://renderer/"), "handle", "daemon:stop", { ...options, getShellWebContents: () => null })).toBe(false);
	});

	it("lets browser-view webContents reach only the exact page-side (method, channel) pairs", () => {
		const page = ev(other, "https://page.example/");
		expect(isTrustedSender(page, "handle", "browser:annotation:submit", options)).toBe(true);
		expect(isTrustedSender(page, "handle", "browser:annotation:capture", options)).toBe(true);
		expect(isTrustedSender(page, "on", "browser:annotation:cancel", options)).toBe(true);
		expect(isTrustedSender(page, "on", "browser:annotation:state", options)).toBe(true);
		expect(isTrustedSender(page, "on", "browser:annotation:discard", options)).toBe(true);
		expect(isTrustedSender(page, "handle", "browser:ensure", options)).toBe(false);
		// Shell-side annotation channels, incl. the shell-side discard `handle`, are not exempt.
		for (const channel of ["setMode", "complete", "action", "discard"]) {
			expect(isTrustedSender(page, "handle", `browser:annotation:${channel}`, options)).toBe(false);
		}
		expect(isTrustedSender(page, "on", "browser:annotation:setMode", options)).toBe(false);
		expect(isTrustedSender(page, "on", "browser:annotation:future", options)).toBe(false);
	});


	it("wrapped handle rejects untrusted callers without running the handler", async () => {
		const raw = fakeIpc();
		const guarded = guardIpcMain(raw as never, options);
		const fn = vi.fn(() => "ok");
		guarded.handle("clipboard:readText", fn);
		const registered = raw.handlers.get("clipboard:readText")!;
		await expect(async () => registered(ev(other, "app://renderer/"))).rejects.toThrow("Untrusted");
		expect(fn).not.toHaveBeenCalled();
		expect(registered(ev(shell, "app://renderer/"))).toBe("ok");
	});

	it("wrapped on drops untrusted events silently and off removes the wrapper", () => {
		const raw = fakeIpc();
		const guarded = guardIpcMain(raw as never, options);
		const fn = vi.fn();
		guarded.on("shell:focus", fn);
		raw.handlers.get("on:shell:focus")!(ev(other, "app://renderer/"));
		expect(fn).not.toHaveBeenCalled();
		raw.handlers.get("on:shell:focus")!(ev(shell, "app://renderer/"));
		expect(fn).toHaveBeenCalledTimes(1);
		guarded.off("shell:focus", fn);
		expect(raw.off).toHaveBeenCalledWith("shell:focus", raw.handlers.get("on:shell:focus"));
	});
});
