/**
 * Owns the one Google Chrome that Real Chrome mode drives (B849 a3): launched
 * from Electron main with the shared Tallpine profile and --remote-debugging-pipe,
 * relaunched by the next command after it exits (a10), closed on quit (a8).
 */
import { execFile, spawn as nodeSpawn, type ChildProcess, type SpawnOptions } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, readlink, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import type { Readable, Writable } from "node:stream";
import { browserError } from "../browser-view-host/args";
import { defaultProcessCommand, type ProcessCommandLookup, type ProcessSignal } from "../agent-browser-runtime/reap";
import { CHROME_INSTALL_URL, CHROME_SIGNING_IN } from "../../shared/chrome-mode";
import { findChromeExecutable, type ChromeExecutable } from "./discover";
import { defaultQuitChromeProcess, type QuitChromeProcess } from "./graceful-quit";
import { createPipeConnection, type ChromeConnection } from "./pipe-transport";

/** The Chrome profile every Chrome-mode project shares (B849 q2, a4). */
export function chromeUserDataDir(dataDir: string): string {
	return path.join(dataDir, "browser", "chrome", "default");
}

// Chrome needs a desktop session (display, locale, home for the keychain) and
// nothing of Tallpine's: no TALLPINE_* variables, tokens or agent credentials (n1).
const CHROME_ENV_ALLOWLIST = new Set([
	"home", "user", "logname", "shell", "path", "lang", "lc_all", "lc_ctype", "lc_messages", "tmpdir", "temp", "tmp",
	"display", "wayland_display", "xauthority", "xdg_runtime_dir", "xdg_config_home", "xdg_cache_home", "xdg_data_home",
	"xdg_session_type", "xdg_current_desktop", "dbus_session_bus_address",
	"systemroot", "windir", "comspec", "pathext", "programfiles", "programfiles(x86)", "localappdata", "appdata",
	"userprofile", "homedrive", "homepath", "username", "systemdrive",
]);

export function chromeEnvironment(env: Record<string, string | undefined>): Record<string, string> {
	const out: Record<string, string> = {};
	for (const [name, value] of Object.entries(env)) {
		if (value !== undefined && CHROME_ENV_ALLOWLIST.has(name.toLowerCase())) out[name] = value;
	}
	return out;
}

export function chromeArguments(userDataDir: string): string[] {
	return [
		`--user-data-dir=${userDataDir}`,
		"--remote-debugging-pipe",
		"--no-first-run",
		"--no-default-browser-check",
		// The first session tab opens its own window (a6); no empty start window.
		"--no-startup-window",
	];
}

/** Where the sign-in Chrome opens: Google, the sign-in a controlled Chrome is refused (B854). */
export const CHROME_SIGN_IN_URL = "https://accounts.google.com/";

/** Plain Chrome on the same profile for signing in by hand (B854): no debugging pipe. */
export function chromeSignInArguments(userDataDir: string, url = CHROME_SIGN_IN_URL): string[] {
	return [`--user-data-dir=${userDataDir}`, "--no-first-run", "--no-default-browser-check", url];
}

function signingInError(): Error {
	return browserError(
		CHROME_SIGNING_IN,
		"The person is signing in to sites in Tallpine's Chrome. Agent browser commands resume when they click Done in the Browser panel or quit that Chrome; wait, then run the command again.",
	);
}

function quittingError(): Error {
	return browserError("BROWSER_CHROME_CLOSED", "Tallpine is quitting; Google Chrome will not open for signing in.");
}

/** Whether a process command line runs a Chrome (any profile). */
export function isChromeCommand(command: string): boolean {
	const trimmed = command.trim();
	const flags = trimmed.indexOf(" --");
	// The executable is what precedes the first flag (macOS paths hold spaces);
	// our profile path itself contains "chrome", so it must not count.
	const executable = flags === -1 ? trimmed : trimmed.slice(0, flags);
	return /chrom/i.test(path.basename(executable));
}

/** Whether a process command line is a Chrome running our exact profile (a9). */
export function isTallpineChromeCommand(command: string, userDataDir: string): boolean {
	return isChromeCommand(command) && ` ${command.trim()} `.includes(` --user-data-dir=${userDataDir} `);
}

export type ChromeLockOwner = { host: string; pid: number };

type SpawnChrome = (file: string, args: string[], options: SpawnOptions) => ChildProcess;

export type ChromeProcessManagerOptions = {
	dataDir: string;
	platform?: NodeJS.Platform;
	env?: Record<string, string | undefined>;
	spawn?: SpawnChrome;
	exists?: (file: string) => boolean;
	discover?: () => ChromeExecutable | null;
	/** The host and pid named by the profile's SingletonLock, or null (POSIX only). */
	lockOwner?: (userDataDir: string) => Promise<ChromeLockOwner | null>;
	hostname?: string;
	processCommand?: ProcessCommandLookup;
	/** macOS: whether Chrome's env reaches the user's login keychain (see keychainReachable). */
	keychainReachable?: (env: Record<string, string>) => Promise<boolean>;
	signal?: ProcessSignal;
	processAlive?: (pid: number) => boolean;
	log?: (message: string) => void;
	closeTimeoutMs?: number;
	startTimeoutMs?: number;
	/** Quits the sign-in Chrome like Cmd+Q so its new cookies reach disk (defaults per platform). */
	quitProcess?: QuitChromeProcess;
	/** How long the sign-in Chrome gets to quit gracefully before it is killed. */
	signInQuitTimeoutMs?: number;
};

/**
 * Chrome on macOS stores its secrets in the login keychain. Without one (a
 * sandboxed or service process, a HOME that is not the person's) every launch
 * raises a "Keychain Not Found" dialog, so Real Chrome mode refuses to start
 * instead. The keychain the env resolves must exist on disk.
 */
export function defaultKeychainReachable(env: Record<string, string>, exists: (file: string) => boolean = existsSync): Promise<boolean> {
	return new Promise((resolve) => {
		execFile("/usr/bin/security", ["default-keychain", "-d", "user"], { env, timeout: 5_000 }, (error, stdout) => {
			const keychain = String(stdout ?? "").trim().replace(/^"|"$/g, "");
			resolve(!error && keychain !== "" && exists(keychain));
		});
	});
}

// SingletonLock is a symlink to "<hostname>-<pid>" on macOS and Linux.
async function defaultLockOwner(userDataDir: string): Promise<ChromeLockOwner | null> {
	try {
		const target = await readlink(path.join(userDataDir, "SingletonLock"));
		const dash = target.lastIndexOf("-");
		const pid = Number.parseInt(target.slice(dash + 1), 10);
		return Number.isSafeInteger(pid) && pid > 1 ? { host: target.slice(0, dash), pid } : null;
	} catch {
		return null;
	}
}

const SINGLETON_FILES = ["SingletonLock", "SingletonSocket", "SingletonCookie"];

function defaultProcessAlive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch (error) {
		return (error as NodeJS.ErrnoException).code === "EPERM";
	}
}

const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export class ChromeProcessManager {
	private connection: ChromeConnection | null = null;
	private child: ChildProcess | null = null;
	private launching: Promise<ChromeConnection> | null = null;
	private signInChild: ChildProcess | null = null;
	private signInActive = false;
	private reattaching: Promise<void> | null = null;
	private quitting = false;
	private readonly changeListeners = new Set<() => void>();
	private readonly userDataDir: string;
	private readonly platform: NodeJS.Platform;
	private readonly log: (message: string) => void;

	constructor(private readonly options: ChromeProcessManagerOptions) {
		this.userDataDir = chromeUserDataDir(options.dataDir);
		this.platform = options.platform ?? process.platform;
		this.log = options.log ?? (() => undefined);
	}

	get running(): boolean {
		return this.connection !== null && !this.connection.closed;
	}

	/** Whether the profile is open in a plain Chrome for signing in (B854). */
	get signingIn(): boolean {
		return this.signInActive;
	}

	/** The live connection, launching Chrome once for all concurrent callers (n3). */
	ensure(): Promise<ChromeConnection> {
		if (this.connection && !this.connection.closed) return Promise.resolve(this.connection);
		if (!this.launching) {
			this.launching = this.launch().finally(() => {
				this.launching = null;
			});
		}
		return this.launching;
	}

	/** Runs after each Chrome exit and sign-in change; returns the unsubscribe function. */
	onChange(listener: () => void): () => void {
		this.changeListeners.add(listener);
		return () => this.changeListeners.delete(listener);
	}

	/** Browser.close, then a kill if Chrome has not exited within the timeout. */
	async close(): Promise<void> {
		const child = this.child;
		const connection = this.connection;
		if (!child) return;
		await this.stopChild(child, () => {
			if (connection && !connection.closed) void connection.send("Browser.close").catch(() => undefined);
		});
	}

	/** App quit: ends a sign-in Chrome too, without relaunching the controlled one. */
	async closeAll(): Promise<void> {
		this.quitting = true;
		const signIn = this.signInChild;
		if (signIn) await this.quitSignInChrome(signIn);
		await this.reattaching;
		await this.close();
	}

	/**
	 * Google refuses sign-in in a Chrome under CDP control (B854): stop the
	 * controlled Chrome and open the same profile in plain Chrome, without the
	 * debugging pipe. When that Chrome exits (the person quits it or clicks Done)
	 * the controlled Chrome is launched again and finds the new sign-ins.
	 */
	async signIn(url = CHROME_SIGN_IN_URL): Promise<void> {
		if (this.signInActive) return;
		if (this.quitting) throw quittingError();
		this.signInActive = true;
		this.notify();
		try {
			await this.launching?.catch(() => undefined);
			await this.close();
			const { executable, chromeEnv } = await this.prepare();
			// The app quit while this was preparing: never open a Chrome after quit.
			if (this.quitting) throw quittingError();
			const spawn = this.options.spawn ?? (nodeSpawn as SpawnChrome);
			const child = spawn(executable.path, chromeSignInArguments(this.userDataDir, url), { env: chromeEnv, stdio: "ignore", detached: false });
			this.signInChild = child;
			child.once("exit", (code, signal) => {
				this.log(`Google Chrome sign-in window exited (${signal ?? code})`);
				this.endSignIn(child, true);
			});
			await new Promise<void>((resolve, reject) => {
				child.once("spawn", () => resolve());
				child.once("error", (error) => {
					this.endSignIn(child, false);
					reject(browserError("BROWSER_CHROME_NOT_FOUND", `Google Chrome could not start: ${error.message}`));
				});
			});
			this.log("Google Chrome opened for signing in to sites");
		} catch (error) {
			if (!this.signInChild) {
				this.signInActive = false;
				this.notify();
			}
			throw error;
		}
	}

	/** The person is done signing in: quit the plain Chrome and reattach. */
	async finishSignIn(): Promise<void> {
		const child = this.signInChild;
		if (child) await this.quitSignInChrome(child);
		await this.reattaching;
	}

	/**
	 * The person's sign-in must survive the quit: Chrome on macOS drops cookies
	 * it has not committed yet when it gets SIGTERM, so it is asked to quit like
	 * Cmd+Q, and killed only if it has not exited after a generous wait.
	 */
	private async quitSignInChrome(child: ChildProcess): Promise<void> {
		const quit = this.options.quitProcess ?? defaultQuitChromeProcess(this.platform);
		const ask = () => {
			const pid = child.pid;
			void (pid ? quit(pid) : Promise.resolve(false)).then(
				(sent) => {
					if (!sent && child.exitCode === null && child.signalCode === null) child.kill("SIGTERM");
				},
				() => child.kill("SIGTERM"),
			);
		};
		await this.stopChild(child, ask, this.options.signInQuitTimeoutMs ?? 20_000);
	}

	private endSignIn(child: ChildProcess, reattach: boolean): void {
		if (this.signInChild !== child) return;
		this.signInChild = null;
		this.signInActive = false;
		this.notify();
		if (!reattach || this.quitting) return;
		this.reattaching = this.ensure()
			.then(
				() => this.notify(),
				(error: unknown) => this.log(`Relaunching Google Chrome after sign-in failed: ${error instanceof Error ? error.message : String(error)}`),
			)
			.finally(() => {
				this.reattaching = null;
			});
	}

	/** Asks a Chrome to quit, then kills it if it has not exited within the timeout. */
	private async stopChild(child: ChildProcess, ask: () => void, timeoutMs = this.options.closeTimeoutMs ?? 5_000): Promise<void> {
		const exited = new Promise<void>((resolve) => {
			if (child.exitCode !== null || child.signalCode !== null) resolve();
			else child.once("exit", () => resolve());
		});
		ask();
		const done = await Promise.race([exited.then(() => true), delay(timeoutMs).then(() => false)]);
		if (!done) {
			this.log("Google Chrome did not quit in time; killing it");
			child.kill("SIGKILL");
			await exited;
		}
	}

	private notify(): void {
		for (const listener of [...this.changeListeners]) listener();
	}

	/** Chrome's executable and env, with the profile ready and free of leftovers. */
	private async prepare(): Promise<{ executable: ChromeExecutable; chromeEnv: Record<string, string> }> {
		const env = this.options.env ?? process.env;
		const exists = this.options.exists ?? existsSync;
		const executable = this.options.discover ? this.options.discover() : findChromeExecutable(this.platform, env, exists);
		if (!executable) {
			throw browserError("BROWSER_CHROME_NOT_FOUND", `Google Chrome is not installed. Install it from ${CHROME_INSTALL_URL} and run the command again.`);
		}
		const chromeEnv = chromeEnvironment(env);
		if (this.platform === "darwin" && !(await (this.options.keychainReachable ?? defaultKeychainReachable)(chromeEnv))) {
			throw browserError(
				"BROWSER_CHROME_KEYCHAIN_UNAVAILABLE",
				"Google Chrome needs your macOS login keychain, which this Tallpine process cannot reach. Start Tallpine from your own desktop session and run the command again.",
			);
		}
		await mkdir(this.userDataDir, { recursive: true, mode: 0o700 });
		await this.clearLeftover();
		return { executable, chromeEnv };
	}

	private async launch(): Promise<ChromeConnection> {
		if (this.signInActive) throw signingInError();
		const { executable, chromeEnv } = await this.prepare();
		// A sign-in that started while this launch was preparing owns the profile now.
		if (this.signInActive) throw signingInError();
		const spawn = this.options.spawn ?? (nodeSpawn as SpawnChrome);
		const child = spawn(executable.path, chromeArguments(this.userDataDir), {
			env: chromeEnv,
			stdio: ["ignore", "ignore", "ignore", "pipe", "pipe"],
			detached: false,
		});
		const output = child.stdio[3] as Writable | null;
		const input = child.stdio[4] as Readable | null;
		if (!output || !input) {
			child.kill("SIGKILL");
			throw browserError("BROWSER_CHROME_CLOSED", "Google Chrome did not open its debugging pipe");
		}
		const connection = createPipeConnection(input, output);
		this.child = child;
		this.connection = connection;
		const spawnFailed = new Promise<never>((_, reject) => {
			child.once("error", (error) => {
				connection.close();
				reject(browserError("BROWSER_CHROME_NOT_FOUND", `Google Chrome could not start: ${error.message}`));
			});
		});
		child.once("exit", (code, signal) => {
			this.log(`Google Chrome exited (${signal ?? code})`);
			connection.close();
			if (this.child === child) this.child = null;
			if (this.connection === connection) this.connection = null;
			this.notify();
		});
		try {
			const startTimeout = this.options.startTimeoutMs ?? 30_000;
			await Promise.race([
				connection.send("Browser.getVersion"),
				spawnFailed,
				delay(startTimeout).then(() => {
					throw browserError("BROWSER_CHROME_CLOSED", "Google Chrome did not answer on its debugging pipe");
				}),
			]);
		} catch (error) {
			child.kill("SIGKILL");
			connection.close();
			throw error;
		}
		this.log(`Google Chrome (${executable.channel}) started for Real Chrome mode`);
		return connection;
	}

	/**
	 * A Chrome left from a crash still holds the profile lock (a9). It is ended
	 * only when its command line names our exact --user-data-dir. Another Chrome
	 * holding it is the person's to quit. A lock from another host, a dead pid,
	 * or a pid now reused by a program that is not Chrome is stale: it is removed
	 * so Chrome can take the profile.
	 */
	private async clearLeftover(): Promise<void> {
		if (this.platform === "win32") return;
		const lock = await (this.options.lockOwner ?? defaultLockOwner)(this.userDataDir);
		if (!lock) return;
		const alive = this.options.processAlive ?? defaultProcessAlive;
		const { pid } = lock;
		const local = lock.host === (this.options.hostname ?? os.hostname());
		const holder = local && pid !== process.pid && alive(pid);
		const command = holder ? (await (this.options.processCommand ?? defaultProcessCommand)(pid)).trim() : "";
		// A live holder whose command line could not be read is not known to be
		// stale: fail closed rather than pull the lock from under a running Chrome.
		if (!holder || (command !== "" && !isChromeCommand(command))) {
			this.log(`Removing stale Chrome profile lock (${lock.host}-${pid})`);
			await Promise.all(SINGLETON_FILES.map((name) => rm(path.join(this.userDataDir, name), { force: true })));
			return;
		}
		if (!isTallpineChromeCommand(command, this.userDataDir)) {
			throw browserError(
				"BROWSER_CHROME_PROFILE_IN_USE",
				`Another program is using Tallpine's Chrome profile (${this.userDataDir}); quit it and run the command again.`,
			);
		}
		const signal = this.options.signal ?? ((target, sig) => process.kill(target, sig));
		this.log(`Ending leftover Tallpine Chrome (pid ${pid})`);
		signal(pid, "SIGTERM");
		for (let waited = 0; waited < 5_000 && alive(pid); waited += 100) await delay(100);
		if (alive(pid)) signal(pid, "SIGKILL");
	}
}
