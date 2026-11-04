/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import {
resolveShellEnv,
resolveShellEnvWithSpec,
resolveWindowsShellProbe,
type ShellRunner,
} from "../shared/shell-env";
import { DEFAULT_TERMINAL_SHELL } from "../shared/ui-locale";
import { ctx } from "./app-context";



// How long to wait for the login shell to print its env before giving up. A
// misconfigured rc that blocks (or a slow nvm/pyenv chain) must not hang startup;
// the daemon then falls back to the static PATH floor.
export const SHELL_ENV_TIMEOUT_MS = 3_000;

export const configuredLoginShell = process.platform === "darwin" ? (os.userInfo().shell ?? undefined) : undefined;


// Run the user's login shell to dump its env. stdin is ignored so an rc that
// reads input hits EOF instead of hanging; stderr is ignored to drop banner
// noise. Never rejects: resolves null on spawn error, non-zero exit, or timeout
// (SIGKILLed), so the caller degrades to the static PATH floor.
export const runLoginShell: ShellRunner = (shellPath, args) =>
	new Promise((resolve) => {
		let settled = false;
		const finish = (value: string | null) => {
			if (settled) return;
			settled = true;
			resolve(value);
		};
		let child: ReturnType<typeof spawn>;
		try {
			child = spawn(shellPath, args, { stdio: ["ignore", "pipe", "ignore"], windowsHide: true });
		} catch {
			finish(null);
			return;
		}
		const timer = setTimeout(() => {
			child.kill("SIGKILL");
			finish(null);
		}, SHELL_ENV_TIMEOUT_MS);
		let stdout = "";
		// stdout may be typed Readable | null under this stdio config; guard it.
		child.stdout?.on("data", (chunk: Buffer) => {
			stdout += chunk.toString("utf8");
		});
		child.once("error", () => {
			clearTimeout(timer);
			finish(null);
		});
		child.once("exit", (code) => {
			clearTimeout(timer);
			finish(code === 0 ? stdout : null);
		});
	});


export function lookupWindowsExecutable(candidate: string): string | null {
	const trimmed = candidate.trim().replace(/^"|"$/g, "");
	if (!trimmed) return null;
	const hasDirectory = trimmed.includes("\\") || trimmed.includes("/") || path.isAbsolute(trimmed);
	const candidates = hasDirectory
		? [trimmed]
		: (process.env.PATH ?? process.env.Path ?? "")
				.split(path.delimiter)
				.filter(Boolean)
				.map((directory) => path.join(directory, trimmed));
	for (const resolved of candidates) {
		if (existsSync(resolved)) return resolved;
	}
	return null;
}


// Resolve the login-shell env once and cache it. On Windows the selected
// terminal shell is used for the probe too, so shell profiles and PATH exports
// stay consistent between standalone terminals and daemon startup.
export function ensureShellEnv(): Promise<void> {
	if (!ctx.shellEnvPromise) {
		if (process.platform === "win32") {
			const probe =
				resolveWindowsShellProbe(ctx.terminalShellPreference, process.env, lookupWindowsExecutable) ??
				resolveWindowsShellProbe(DEFAULT_TERMINAL_SHELL, process.env, lookupWindowsExecutable);
			if (!probe) {
				ctx.shellEnvPromise = Promise.resolve();
				ctx.cachedShellEnv = null;
				return ctx.shellEnvPromise;
			}
			ctx.shellEnvPromise = resolveShellEnvWithSpec(probe, runLoginShell).then((resolved) => {
				ctx.cachedShellEnv = resolved;
				if (!resolved) {
					console.error("Tallpine: could not read the login-shell environment; falling back to the process environment.");
				}
			});
			return ctx.shellEnvPromise;
		}
		ctx.shellEnvPromise = resolveShellEnv(process.env, runLoginShell, configuredLoginShell).then((resolved) => {
			ctx.cachedShellEnv = resolved;
			if (!resolved) {
				console.error("Tallpine: could not read the login-shell environment; falling back to a static PATH floor.");
			}
		});
	}
	return ctx.shellEnvPromise;
}

export function registerShellEnvHost(): void {

	if (process.platform !== "win32") {
		ctx.earlyShellEnvProbe = ensureShellEnv();
		ctx.earlyShellEnvProbe.catch(() => undefined);
	}
}
