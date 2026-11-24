import { EventEmitter } from "node:events";
import path from "node:path";
import { PassThrough } from "node:stream";
import { lstatSync, mkdirSync, mkdtempSync, rmSync, symlinkSync } from "node:fs";
import os from "node:os";
import type { ChildProcess, SpawnOptions } from "node:child_process";
import { describe, expect, it, vi } from "vitest";
import { CHROME_SIGN_IN_URL, ChromeProcessManager, chromeUserDataDir, isTallpineChromeCommand } from "./chrome-process";

type Spawned = { file: string; args: string[]; options: SpawnOptions; child: FakeChrome };

class FakeChrome extends EventEmitter {
	exitCode: number | null = null;
	signalCode: NodeJS.Signals | null = null;
	readonly toChrome = new PassThrough();
	readonly fromChrome = new PassThrough();
	readonly stdio: unknown[] = [null, null, null, this.toChrome, this.fromChrome];
	readonly methods: string[] = [];
	readonly pid = 4000 + Math.floor(Math.random() * 1000);

	constructor() {
		super();
		let buffered = "";
		this.toChrome.on("data", (chunk: Buffer) => {
			buffered += chunk.toString();
			for (let end = buffered.indexOf("\0"); end !== -1; end = buffered.indexOf("\0")) {
				const message = JSON.parse(buffered.slice(0, end)) as { id: number; method: string };
				buffered = buffered.slice(end + 1);
				this.methods.push(message.method);
				this.fromChrome.write(`${JSON.stringify({ id: message.id, result: {} })}\0`);
				if (message.method === "Browser.close") setImmediate(() => this.exit(0));
			}
		});
	}

	readonly signals: NodeJS.Signals[] = [];

	kill(signal: NodeJS.Signals = "SIGTERM"): boolean {
		this.signals.push(signal);
		setImmediate(() => this.exit(null, signal));
		return true;
	}

	exit(code: number | null, signal: NodeJS.Signals | null = null): void {
		if (this.exitCode !== null || this.signalCode !== null) return;
		this.exitCode = code;
		this.signalCode = signal;
		this.fromChrome.destroy();
		this.emit("exit", code, signal);
	}
}

function manager(overrides: Partial<ConstructorParameters<typeof ChromeProcessManager>[0]> = {}) {
	const dataDir = mkdtempSync(path.join(process.env.TMPDIR ?? os.tmpdir(), "chrome-pm-"));
	const spawned: Spawned[] = [];
	const pm = new ChromeProcessManager({
		dataDir,
		platform: "darwin",
		env: { HOME: "/Users/p", PATH: "/usr/bin", LANG: "en_US.UTF-8", TALLPINE_DATA_DIR: "/secret", TALLPINE_BROWSER_RUNTIME_TOKEN: "tok", GITHUB_TOKEN: "ghp", ANTHROPIC_API_KEY: "sk" },
		discover: () => ({ path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", channel: "stable" }),
		lockOwner: async () => null,
		keychainReachable: async () => true,
		quitProcess: async () => false,
		spawn: (file, args, options) => {
			const child = new FakeChrome();
			spawned.push({ file, args, options, child });
			setImmediate(() => child.emit("spawn"));
			return child as unknown as ChildProcess;
		},
		...overrides,
	});
	return { pm, spawned, dataDir };
}

describe("ChromeProcessManager", () => {
	it("launches once for concurrent callers (single-flight)", async () => {
		const { pm, spawned } = manager();
		const [a, b] = await Promise.all([pm.ensure(), pm.ensure()]);
		expect(a).toBe(b);
		expect(spawned).toHaveLength(1);
		expect(await pm.ensure()).toBe(a);
		expect(spawned).toHaveLength(1);
		expect(spawned[0].child.methods).toEqual(["Browser.getVersion"]);
	});

	it("uses the shared profile over a pipe, never a debugging port", async () => {
		const { pm, spawned, dataDir } = manager();
		await pm.ensure();
		const { args, options } = spawned[0];
		expect(args).toContain(`--user-data-dir=${chromeUserDataDir(dataDir)}`);
		expect(chromeUserDataDir(dataDir)).toBe(path.join(dataDir, "browser", "chrome", "default"));
		expect(args).toContain("--remote-debugging-pipe");
		expect(args.some((arg) => arg.startsWith("--remote-debugging-port"))).toBe(false);
		expect(options.stdio).toEqual(["ignore", "ignore", "ignore", "pipe", "pipe"]);
	});

	it("spawns Chrome with a scrubbed environment", async () => {
		const { pm, spawned } = manager();
		await pm.ensure();
		expect(spawned[0].options.env).toEqual({ HOME: "/Users/p", PATH: "/usr/bin", LANG: "en_US.UTF-8" });
	});

	it("relaunches on the next ensure after Chrome exits, and tells listeners", async () => {
		const { pm, spawned } = manager();
		const exits = vi.fn();
		pm.onChange(exits);
		const first = await pm.ensure();
		spawned[0].child.exit(0);
		await vi.waitFor(() => expect(exits).toHaveBeenCalledTimes(1));
		expect(first.closed).toBe(true);
		expect(pm.running).toBe(false);
		const second = await pm.ensure();
		expect(second).not.toBe(first);
		expect(spawned).toHaveLength(2);
	});

	it("closes Chrome with Browser.close", async () => {
		const { pm, spawned } = manager();
		await pm.ensure();
		await pm.close();
		expect(spawned[0].child.methods).toContain("Browser.close");
		expect(spawned[0].child.exitCode).toBe(0);
	});

	it("fails BROWSER_CHROME_NOT_FOUND with the install link when Chrome is missing", async () => {
		const { pm, spawned } = manager({ discover: () => null });
		await expect(pm.ensure()).rejects.toMatchObject({ code: "BROWSER_CHROME_NOT_FOUND", message: expect.stringContaining("https://www.google.com/chrome/") });
		expect(spawned).toHaveLength(0);
	});

	it("ends a leftover Chrome only when its args name our exact profile (a9)", async () => {
		const signals: [number, string][] = [];
		let alive = true;
		const base = manager();
		const dir = chromeUserDataDir(base.dataDir);
		const { pm, spawned } = manager({
			dataDir: base.dataDir,
			lockOwner: async () => ({ host: "box", pid: 4242 }),
			hostname: "box",
			processAlive: () => alive,
			processCommand: async () => `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=${dir} --remote-debugging-pipe`,
			signal: (pid, sig) => {
				signals.push([pid, sig]);
				alive = false;
			},
		});
		await pm.ensure();
		expect(signals).toEqual([[4242, "SIGTERM"]]);
		expect(spawned).toHaveLength(1);
	});

	it("asks the person to quit another Chrome holding the profile (a9)", async () => {
		const base = manager();
		const dir = chromeUserDataDir(base.dataDir);
		const signal = vi.fn();
		const { pm, spawned } = manager({
			dataDir: base.dataDir,
			lockOwner: async () => ({ host: "box", pid: 4242 }),
			hostname: "box",
			processAlive: () => true,
			processCommand: async () => `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=${dir}-other`,
			signal,
		});
		await expect(pm.ensure()).rejects.toMatchObject({ code: "BROWSER_CHROME_PROFILE_IN_USE" });
		expect(signal).not.toHaveBeenCalled();
		expect(spawned).toHaveLength(0);
	});

	it("keeps the lock of a live holder whose command line cannot be read (a9)", async () => {
		const base = manager();
		const dir = chromeUserDataDir(base.dataDir);
		mkdirSync(dir, { recursive: true });
		rmSync(path.join(dir, "SingletonLock"), { force: true });
		symlinkSync("box-4242", path.join(dir, "SingletonLock"));
		const signal = vi.fn();
		const { pm, spawned } = manager({
			dataDir: base.dataDir,
			lockOwner: async () => ({ host: "box", pid: 4242 }),
			hostname: "box",
			processAlive: () => true,
			processCommand: async () => "",
			signal,
		});
		await expect(pm.ensure()).rejects.toMatchObject({ code: "BROWSER_CHROME_PROFILE_IN_USE" });
		expect(signal).not.toHaveBeenCalled();
		expect(spawned).toHaveLength(0);
		expect(lstatSync(path.join(dir, "SingletonLock"), { throwIfNoEntry: false })).toBeDefined();
	});

	it("removes a stale lock: reused pid, other host or dead holder (a9)", async () => {
		const base = manager();
		const dir = chromeUserDataDir(base.dataDir);
		const cases: [string, boolean, string][] = [
			["box", true, "/usr/bin/vim notes.txt"],
			["box", true, `/usr/bin/python serve.py --user-data-dir=${dir}`],
			["other-host", true, `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=${dir}`],
			["box", false, `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=${dir}`],
		];
		for (const [host, alive, command] of cases) {
			mkdirSync(dir, { recursive: true });
			rmSync(path.join(dir, "SingletonLock"), { force: true });
			symlinkSync(`${host}-4242`, path.join(dir, "SingletonLock"));
			const signal = vi.fn();
			const { pm, spawned } = manager({
				dataDir: base.dataDir,
				lockOwner: async () => ({ host, pid: 4242 }),
				hostname: "box",
				processAlive: () => alive,
				processCommand: async () => command,
				signal,
			});
			await pm.ensure();
			expect(signal).not.toHaveBeenCalled();
			expect(spawned).toHaveLength(1);
			expect(lstatSync(path.join(dir, "SingletonLock"), { throwIfNoEntry: false }), `${host} ${command}`).toBeUndefined();
		}
	});

	it("never launches Chrome on macOS when the login keychain is unreachable", async () => {
		const seen: Record<string, string>[] = [];
		const { pm, spawned } = manager({ keychainReachable: async (env) => { seen.push(env); return false; } });
		await expect(pm.ensure()).rejects.toMatchObject({ code: "BROWSER_CHROME_KEYCHAIN_UNAVAILABLE" });
		expect(spawned).toHaveLength(0);
		// Checked with the env Chrome would get.
		expect(seen).toEqual([{ HOME: "/Users/p", PATH: "/usr/bin", LANG: "en_US.UTF-8" }]);
	});

	describe("signing in to sites (B854)", () => {
		it("stops the controlled Chrome, opens the profile without the pipe, and reattaches when that Chrome quits", async () => {
			const { pm, spawned, dataDir } = manager();
			const changes = vi.fn();
			pm.onChange(changes);
			const controlled = await pm.ensure();
			await pm.signIn();
			expect(spawned[0].child.methods).toContain("Browser.close");
			expect(controlled.closed).toBe(true);
			expect(pm.signingIn).toBe(true);
			expect(pm.running).toBe(false);
			expect(spawned).toHaveLength(2);
			const signIn = spawned[1];
			expect(signIn.args).toEqual([`--user-data-dir=${chromeUserDataDir(dataDir)}`, "--no-first-run", "--no-default-browser-check", CHROME_SIGN_IN_URL]);
			expect(signIn.args.some((arg) => arg.startsWith("--remote-debugging") || arg === "--enable-automation")).toBe(false);
			expect(signIn.options.stdio).toBe("ignore");
			expect(Object.keys(signIn.options.env ?? {})).not.toContain("TALLPINE_DATA_DIR");
			// Agent commands wait for the person instead of fighting over the profile.
			await expect(pm.ensure()).rejects.toMatchObject({ code: "BROWSER_CHROME_SIGNING_IN" });
			expect(spawned).toHaveLength(2);

			signIn.child.exit(0);
			await vi.waitFor(() => expect(pm.running).toBe(true));
			expect(pm.signingIn).toBe(false);
			expect(spawned).toHaveLength(3);
			expect(spawned[2].args).toContain("--remote-debugging-pipe");
			expect(spawned[2].child.methods).toEqual(["Browser.getVersion"]);
			expect(await pm.ensure()).not.toBe(controlled);
			expect(changes.mock.calls.length).toBeGreaterThanOrEqual(3);
		});

		it("an agent command during sign-in never ends the person's sign-in Chrome", async () => {
			const signal = vi.fn();
			let lock: { host: string; pid: number } | null = null;
			const { pm, spawned, dataDir } = manager({
				lockOwner: async () => lock,
				hostname: "here",
				processAlive: () => true,
				processCommand: async () => `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=${chromeUserDataDir(dataDir)}`,
				signal,
			});
			await pm.signIn();
			lock = { host: "here", pid: 4242 };
			await expect(pm.ensure()).rejects.toMatchObject({ code: "BROWSER_CHROME_SIGNING_IN" });
			expect(signal).not.toHaveBeenCalled();
			expect(spawned).toHaveLength(1);
		});

		it("Done quits the sign-in Chrome and resolves once the controlled Chrome is back", async () => {
			const { pm, spawned } = manager();
			await pm.signIn();
			expect(spawned).toHaveLength(1);
			await pm.finishSignIn();
			expect(spawned[0].child.signals).toEqual(["SIGTERM"]);
			expect(pm.signingIn).toBe(false);
			expect(pm.running).toBe(true);
			expect(spawned[1].args).toContain("--remote-debugging-pipe");
		});

		it("Done quits the sign-in Chrome like Cmd+Q, never SIGTERM, so a fresh sign-in's cookies reach disk", async () => {
			const asked: number[] = [];
			const { pm, spawned } = manager({
				quitProcess: async (pid) => {
					asked.push(pid);
					setImmediate(() => spawned[0].child.exit(0));
					return true;
				},
			});
			await pm.signIn("https://analytics.google.com/analytics/web/");
			expect(spawned[0].args.at(-1)).toBe("https://analytics.google.com/analytics/web/");
			await pm.finishSignIn();
			expect(asked).toEqual([spawned[0].child.pid]);
			expect(spawned[0].child.signals).toEqual([]);
			expect(pm.running).toBe(true);
		});

		it("a sign-in Chrome that ignores the quit is killed only after the grace period", async () => {
			const { pm, spawned } = manager({ quitProcess: async () => true, signInQuitTimeoutMs: 30 });
			await pm.signIn();
			await pm.finishSignIn();
			expect(spawned[0].child.signals).toEqual(["SIGKILL"]);
			expect(pm.running).toBe(true);
		});

		it("a second sign-in while one is open launches nothing", async () => {
			const { pm, spawned } = manager();
			await pm.signIn();
			await pm.signIn();
			expect(spawned).toHaveLength(1);
		});

		it("quitting the app ends the sign-in Chrome without relaunching", async () => {
			const { pm, spawned } = manager();
			await pm.signIn();
			await pm.closeAll();
			expect(spawned[0].child.signals).toEqual(["SIGTERM"]);
			expect(pm.signingIn).toBe(false);
			expect(spawned).toHaveLength(1);
			expect(pm.running).toBe(false);
		});

		it("never opens the sign-in Chrome when the app quits while it is preparing", async () => {
			const gate: { release?: (reachable: boolean) => void } = {};
			const { pm, spawned } = manager({ keychainReachable: () => new Promise<boolean>((resolve) => (gate.release = resolve)) });
			const signIn = pm.signIn();
			await vi.waitFor(() => expect(gate.release).toBeDefined());
			expect(pm.signingIn).toBe(true);
			await pm.closeAll();
			gate.release?.(true);
			await expect(signIn).rejects.toMatchObject({ code: "BROWSER_CHROME_CLOSED" });
			expect(spawned).toHaveLength(0);
			expect(pm.signingIn).toBe(false);
			await expect(pm.signIn()).rejects.toMatchObject({ code: "BROWSER_CHROME_CLOSED" });
			expect(spawned).toHaveLength(0);
		});

		it("a sign-in Chrome that cannot start leaves sign-in mode and relaunches nothing", async () => {
			const spawned: Spawned[] = [];
			const { pm } = manager({
				spawn: (file, args, options) => {
					const child = new FakeChrome();
					spawned.push({ file, args, options, child });
					setImmediate(() => child.emit("error", new Error("spawn ENOENT")));
					return child as unknown as ChildProcess;
				},
			});
			await expect(pm.signIn()).rejects.toMatchObject({ code: "BROWSER_CHROME_NOT_FOUND" });
			expect(pm.signingIn).toBe(false);
			expect(spawned).toHaveLength(1);
		});

		it("refuses while another program holds the profile, then leaves sign-in mode", async () => {
			const { pm, spawned } = manager({
				lockOwner: async () => ({ host: os.hostname(), pid: 4242 }),
				hostname: os.hostname(),
				processAlive: () => true,
				processCommand: async () => "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=/elsewhere",
			});
			await expect(pm.signIn()).rejects.toMatchObject({ code: "BROWSER_CHROME_PROFILE_IN_USE" });
			expect(pm.signingIn).toBe(false);
			expect(spawned).toHaveLength(0);
		});
	});

	it("matches the profile argument exactly", () => {
		expect(isTallpineChromeCommand("chrome --user-data-dir=/d/default --x", "/d/default")).toBe(true);
		expect(isTallpineChromeCommand("chrome --user-data-dir=/d/default", "/d/default")).toBe(true);
		expect(isTallpineChromeCommand("chrome --user-data-dir=/d/default2", "/d/default")).toBe(false);
		expect(isTallpineChromeCommand("chrome --user-data-dir=/x/d/default", "/d/default")).toBe(false);
		expect(isTallpineChromeCommand("/usr/bin/node --user-data-dir=/browser/chrome/default", "/browser/chrome/default")).toBe(false);
	});
});
