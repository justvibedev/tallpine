/* Split out of agent-browser-runtime.ts (B297, P53). */

import { createHash } from "node:crypto";
import { chmod, mkdir, rm } from "node:fs/promises";
import path from "node:path";
import { AgentBrowserCDPBridge, type AgentBrowserTargetProvider } from "../agent-browser-cdp-bridge";

export const MAX_ARGUMENTS = 100;

export const MAX_ARGUMENT_CHARS = 16_384;

export const MAX_OUTPUT_BYTES = 1 << 20;

export const MAX_SCREENSHOT_BYTES = 5 << 20;

export const COMMAND_TIMEOUT_MS = 60_000;

export const SCREENSHOT_TIMEOUT_MS = 30_000;

export const CLOSE_TIMEOUT_MS = 10_000;

export const BRIDGE_CLOSE_TIMEOUT_MS = 5_000;

export const BROWSER_RUNTIME_RECLAIM_GRACE_MS = 15 * 60_000;

export const RUNTIME_OWNER_MARKER = "TALLPINE_BROWSER_RUNTIME_V1";

export const RUNTIME_OWNER_FILE = "owner.json";

export const RUNTIME_ROOT_PATTERN = /^(?:run-(\d+)-[0-9a-f]{12}|r-[0-9a-f]{10})$/;

export const SOCKET_ALIAS_PATTERN = /^(?:tp|tallpine|ao)-br-(\d+)-[0-9a-f]{12}$/;

export const STREAM_ALREADY_DISABLED_MESSAGE = "Streaming is not enabled for this session";

/** Agent Browser's Unix preflight uses the macOS-sized 103-byte limit on all Unix builds. */
export const AGENT_BROWSER_UNIX_SOCKET_PATH_MAX_BYTES = 103;

export const NATIVE_ENV_ALLOWLIST = new Set([
	"path",
	"pathext",
	"systemroot",
	"windir",
	"comspec",
	"temp",
	"tmp",
	"tmpdir",
	"lang",
	"lc_all",
	"lc_ctype",
	"term",
	"colorterm",
	"no_color",
	"force_color",
]);

export const ALLOWED_COMMANDS = new Set([
	"open",
	"snapshot",
	"click",
	"dblclick",
	"focus",
	"type",
	"fill",
	"press",
	"keyboard",
	"keydown",
	"keyup",
	"hover",
	"select",
	"check",
	"uncheck",
	"scroll",
	"scrollintoview",
	"drag",
	"screenshot",
	"wait",
	"get",
	"is",
	"find",
	"tab",
	"frame",
	"dialog",
	"console",
	"errors",
	"highlight",
	"diff",
]);

export const FORBIDDEN_FLAGS = [
	"--cdp",
	"--auto-connect",
	"--session",
	"--namespace",
	"--profile",
	"--state",
	"--restore",
	"--executable-path",
	"--extension",
	"--init-script",
	"--args",
	"--headers",
	"--proxy",
	"--plugin",
	"--allowed-domains",
];

export type AgentBrowserRunResult = {
	command: string;
	stdout: string;
	stderr: string;
	exitCode: number;
	untrustedExternalContent: true;
};

export type NativeProcessResult = Pick<AgentBrowserRunResult, "stdout" | "stderr" | "exitCode">;

export type AgentBrowserRuntimeOptions = {
	binaryPath: string;
	dataDir: string;
	/** Internal seam for testing path validation; production derives a per-run private directory. */
	socketDir?: string;
	log?: (message: string) => void;
	/** Internal seams used by lifecycle tests; production uses the defaults. */
	platform?: NodeJS.Platform;
	processRunner?: NativeProcessRunner;
	bridgeFactory?: (provider: AgentBrowserTargetProvider) => AgentBrowserBridge;
	processAlive?: (pid: number) => boolean;
};

export type AgentBrowserJSONResult = Record<string, unknown>;

export type AgentBrowserBridge = Pick<AgentBrowserCDPBridge, "start" | "close" | "reconcileTargets">;

export type NativeProcessRunner = (
	binaryPath: string,
	args: string[],
	environment: NodeJS.ProcessEnv,
	signal?: AbortSignal,
	timeoutMs?: number,
) => Promise<NativeProcessResult>;

export type SessionRuntime = {
	bridge: AgentBrowserBridge;
	endpoint: string;
	namespace: string;
	runtimeDir: string;
	configPath: string;
};

export type RuntimeOwner = {
	marker: typeof RUNTIME_OWNER_MARKER;
	pid: number;
	startedAt: string;
	token: string;
};

export function numberIsInteger(value: unknown): value is number {
	return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

export function defaultProcessAlive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch (error) {
		return (error as NodeJS.ErrnoException).code === "EPERM";
	}
}

export async function removePath(target: string, log: (message: string) => void, label: string): Promise<void> {
	try {
		await rm(target, { recursive: true, force: true, maxRetries: 4, retryDelay: 100 });
	} catch (error) {
		log(`agent-browser ${label} cleanup failed: ${String(error)}`);
	}
}

export async function ensurePrivateDirectory(target: string): Promise<void> {
	await mkdir(target, { recursive: true, mode: 0o700 });
	if (process.platform !== "win32") await chmod(target, 0o700);
}

export async function closeBridgeWithTimeout(bridge: AgentBrowserBridge): Promise<void> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	try {
		await Promise.race([
			bridge.close(),
			new Promise<never>((_, reject) => {
				timer = setTimeout(() => reject(new Error("bridge close timed out")), BRIDGE_CLOSE_TIMEOUT_MS);
			}),
		]);
	} finally {
		if (timer) clearTimeout(timer);
	}
}

export function isRecord(value: unknown): value is Record<string, unknown> {
	return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

export function sessionNamespace(sessionId: string): string {
	return `tp-${createHash("sha256").update(sessionId).digest("hex").slice(0, 4)}`;
}

export function agentBrowserSocketPath(socketDir: string, namespace: string): string {
	return path.join(socketDir, "namespaces", namespace, "run", `${namespace}.sock`);
}

export function assertAgentBrowserSocketPath(socketDir: string, namespace: string, platform: NodeJS.Platform): void {
	if (platform === "win32") return;
	const socketPath = agentBrowserSocketPath(socketDir, namespace);
	const byteLength = Buffer.byteLength(socketPath, "utf8");
	if (byteLength > AGENT_BROWSER_UNIX_SOCKET_PATH_MAX_BYTES) {
		throw runtimeError(
			"AGENT_BROWSER_START_FAILED",
			`Agent Browser socket path is ${byteLength} bytes; Unix supports at most ${AGENT_BROWSER_UNIX_SOCKET_PATH_MAX_BYTES}. Tallpine needs a shorter socket directory.`,
		);
	}
}

export function runtimeError(code: string, message: string): Error & { code: string } {
	return Object.assign(new Error(message), { code });
}
