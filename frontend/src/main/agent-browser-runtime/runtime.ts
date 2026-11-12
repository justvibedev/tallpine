/* Split out of agent-browser-runtime.ts (B297, P53). */

import { randomBytes } from "node:crypto";
import { access, mkdir, mkdtemp, readFile, symlink, utimes, writeFile } from "node:fs/promises";
import path from "node:path";
import { AgentBrowserCDPBridge, type AgentBrowserTargetProvider } from "../agent-browser-cdp-bridge";
import { AgentBrowserBridge, AgentBrowserJSONResult, AgentBrowserRunResult, AgentBrowserRuntimeOptions, CLOSE_TIMEOUT_MS, COMMAND_TIMEOUT_MS, MAX_SCREENSHOT_BYTES, NATIVE_ENV_ALLOWLIST, NativeProcessRunner, RUNTIME_OWNER_FILE, RUNTIME_OWNER_MARKER, RuntimeOwner, SCREENSHOT_TIMEOUT_MS, SessionRuntime, assertAgentBrowserSocketPath, closeBridgeWithTimeout, defaultProcessAlive, ensurePrivateDirectory, isRecord, removePath, runtimeError, sessionNamespace } from "./common";
import { nativeArgumentsForAction, optionalStringValue, parseAgentBrowserJSON, pngDimensions } from "./native-actions";
import { runNativeProcess } from "./native-process";
import { reapAgentBrowserDaemons, reapOrphanedAgentBrowserProcesses } from "./reap";
import { scavengeBrowserRuntime, scavengeBrowserSocketAliases, streamAlreadyDisabled } from "./scavenge";
import { validateAgentBrowserArguments } from "./validate";

export class AgentBrowserRuntime {
	private readonly sessions = new Map<string, SessionRuntime>();
	private readonly initializing = new Map<string, Promise<SessionRuntime>>();
	private readonly closing = new Map<string, Promise<void>>();
	private readonly log: (message: string) => void;
	private readonly processRunner: NativeProcessRunner;
	private readonly bridgeFactory: (provider: AgentBrowserTargetProvider) => AgentBrowserBridge;
	private readonly processAlive: (pid: number) => boolean;
	private readonly platform: NodeJS.Platform;
	private readonly socketDirOverride?: string;
	private socketDir: string | null = null;
	private socketDirAlias: string | null = null;
	private runtimeRootPromise: Promise<void> | null = null;
	private runtimeRoot: string | null = null;
	private disposed = false;
	private disposePromise: Promise<void> | null = null;

	constructor(private readonly options: AgentBrowserRuntimeOptions) {
		this.log = options.log ?? (() => undefined);
		this.processRunner = options.processRunner ?? runNativeProcess;
		this.bridgeFactory = options.bridgeFactory ?? ((provider) => new AgentBrowserCDPBridge(provider));
		this.processAlive = options.processAlive ?? defaultProcessAlive;
		this.platform = options.platform ?? process.platform;
		this.socketDirOverride = options.socketDir;
	}

	/** Reclaim confirmed-dead run roots before the browser UI is created. */
	async prepare(): Promise<void> {
		if (this.disposed) throw runtimeError("AGENT_BROWSER_RUNTIME_CLOSED", "Browser automation runtime is closed");
		if (this.platform !== "win32") {
			await scavengeBrowserSocketAliases(this.options.dataDir, this.processAlive, this.log);
		}
		await scavengeBrowserRuntime(this.options.dataDir, this.processAlive, this.log, this.options.reap);
		await reapOrphanedAgentBrowserProcesses({ binaryPath: this.options.binaryPath, processAlive: this.processAlive, log: this.log, ...this.options.reap });
	}

	async run(
		sessionId: string,
		args: string[],
		provider: AgentBrowserTargetProvider,
		signal?: AbortSignal,
		timeoutMs = COMMAND_TIMEOUT_MS,
	): Promise<AgentBrowserRunResult> {
		if (this.disposed) throw runtimeError("AGENT_BROWSER_RUNTIME_CLOSED", "Browser automation runtime is closed");
		await this.assertBinary();
		validateAgentBrowserArguments(args);
		const runtime = await this.ensureSession(sessionId, provider);
		await this.touchRuntimeRoot();
		const environment = this.environment(runtime);
		runtime.bridge.reconcileTargets();
		// The native daemon can expire and be recreated independently while this
		// Tallpine session runtime remains alive. Its replacement starts streaming by
		// default, so reassert the input-surface policy immediately before every
		// command. agent-browser reports "already disabled" as a non-zero result;
		// that state is the policy we need, while every other failure remains fatal.
		const disabled = await this.processRunner(
			this.options.binaryPath,
			["stream", "disable"],
			environment,
			signal,
		);
		if (disabled.exitCode !== 0 && !streamAlreadyDisabled(disabled)) {
			throw runtimeError(
				"AGENT_BROWSER_START_FAILED",
				disabled.stderr.trim() || "Unable to disable agent-browser streaming",
			);
		}
		const result = await this.processRunner(this.options.binaryPath, args, environment, signal, timeoutMs);
		if (result.exitCode !== 0) {
			throw runtimeError(
				"AGENT_BROWSER_COMMAND_FAILED",
				result.stderr.trim() || result.stdout.trim() || `agent-browser exited with code ${result.exitCode}`,
			);
		}
		return { ...result, command: args[0], untrustedExternalContent: true };
	}

	async runAction(
		sessionId: string,
		action: string,
		args: Record<string, unknown>,
		provider: AgentBrowserTargetProvider,
		signal?: AbortSignal,
	): Promise<AgentBrowserJSONResult> {
		const nativeArgs = nativeArgumentsForAction(action, args);
		const requestedTabId = action === "tab-select" || action === "tab-close"
			? optionalStringValue(args.tabId)
			: undefined;
		if (action === "tab-close" && requestedTabId) {
			if (this.disposed) throw runtimeError("AGENT_BROWSER_RUNTIME_CLOSED", "Browser automation runtime is closed");
			if (signal?.aborted) throw runtimeError("AGENT_BROWSER_CANCELLED", "agent-browser command was cancelled");
			if (!provider.listTargets().some((target) => target.id === requestedTabId)) {
				throw runtimeError("AGENT_BROWSER_COMMAND_FAILED", `Unable to resolve Tallpine browser tab ${requestedTabId}`);
			}
			// A native daemon restart can renumber tabs between list and close.
			// Close the exact Tallpine-owned target, then notify an existing daemon.
			await provider.closeTarget(requestedTabId);
			this.sessions.get(sessionId)?.bridge.reconcileTargets();
			return { tabId: requestedTabId, targetId: requestedTabId, closed: true, untrustedExternalContent: true };
		}
		if (requestedTabId) {
			// Tallpine target IDs and the native daemon's sequential tab IDs have
			// independent lifetimes. Resolve by CDP identity for every selection.
			const listed = await this.run(sessionId, ["tab", "list", "--json"], provider, signal);
			const tabs = parseAgentBrowserJSON(listed.stdout).tabs;
			const matches = Array.isArray(tabs)
				? tabs.filter((tab) => isRecord(tab) && tab.targetId === requestedTabId)
				: [];
			const tab = matches.length === 1 ? matches[0] : undefined;
			if (!isRecord(tab) || typeof tab.tabId !== "string" || !/^t[1-9]\d*$/.test(tab.tabId)) {
				throw runtimeError("AGENT_BROWSER_COMMAND_FAILED", `Unable to resolve Tallpine browser tab ${requestedTabId}`);
			}
			nativeArgs[nativeArgs.length - 1] = tab.tabId;
		}
		const result = await this.run(sessionId, [...nativeArgs, "--json"], provider, signal);
		const parsed = parseAgentBrowserJSON(result.stdout);
		if (requestedTabId && parsed.targetId !== requestedTabId) {
			throw runtimeError("AGENT_BROWSER_COMMAND_FAILED", `Browser automation did not target Tallpine tab ${requestedTabId}`);
		}
		if (action === "tab-new" || action === "tab-select" || action === "tab-close") {
			if (typeof parsed.targetId !== "string" || !parsed.targetId) {
				throw runtimeError("AGENT_BROWSER_COMMAND_FAILED", "Browser automation returned no tab target identity");
			}
			parsed.tabId = parsed.targetId;
		}
		return parsed;
	}

	private nativeAnnotations(stdout: string): { annotations?: unknown[]; boundary?: unknown } | undefined {
		try {
			const parsed = parseAgentBrowserJSON(stdout);
			if (!Array.isArray(parsed.annotations)) {
				this.log("agent-browser screenshot returned no annotations for an annotated capture");
				return undefined;
			}
			return { annotations: parsed.annotations, boundary: parsed._boundary };
		} catch (error) {
			// agent-browser reporting its own failure is the caller's error, not a
			// missing extra; only unreadable output degrades to "no annotations".
			if ((error as { code?: string }).code !== "AGENT_BROWSER_INVALID_OUTPUT") throw error;
			this.log(`agent-browser screenshot annotations were unreadable: ${(error as Error).message}`);
			return undefined;
		}
	}

	async screenshot(
		sessionId: string,
		provider: AgentBrowserTargetProvider,
		signal?: AbortSignal,
		options: { annotate?: boolean } = {},
	): Promise<{
		data: string;
		width: number;
		height: number;
		annotations?: unknown[];
		annotationsUnavailable?: true;
		untrustedExternalContent: true;
	}> {
		const runtime = await this.ensureSession(sessionId, provider);
		await this.touchRuntimeRoot();
		const directory = await mkdtemp(path.join(runtime.runtimeDir, "screenshot-"));
		const target = path.join(directory, "screenshot.png");
		try {
			const command = ["screenshot", target, "--json", ...(options.annotate === true ? ["--annotate"] : [])];
			const result = await this.run(sessionId, command, provider, signal, SCREENSHOT_TIMEOUT_MS);
			// Read the envelope before the file: a native capture that failed with a
			// reason reports it here, and its message beats the ENOENT that reading a
			// PNG it never wrote would raise.
			const annotated = options.annotate === true ? this.nativeAnnotations(result.stdout) : undefined;
			const image = await readFile(target);
			if (image.length > MAX_SCREENSHOT_BYTES) {
				throw runtimeError("AGENT_BROWSER_OUTPUT_TOO_LARGE", "Browser screenshot exceeded Tallpine's size limit");
			}
			const { width, height } = pngDimensions(image);
			return {
				data: image.toString("base64"),
				width,
				height,
				...(annotated?.annotations ? { annotations: annotated.annotations } : {}),
				...(annotated?.boundary ? { _boundary: annotated.boundary } : {}),
				...(options.annotate === true && !annotated?.annotations ? { annotationsUnavailable: true as const } : {}),
				untrustedExternalContent: true,
			};
		} finally {
			await removePath(directory, this.log, "screenshot directory");
		}
	}

	async closeSession(sessionId: string): Promise<void> {
		const existing = this.closing.get(sessionId);
		if (existing) return existing;
		const cleanup = this.closeSessionInternal(sessionId);
		this.closing.set(sessionId, cleanup);
		try {
			await cleanup;
		} finally {
			if (this.closing.get(sessionId) === cleanup) this.closing.delete(sessionId);
			await this.removeRuntimeRootIfIdle();
		}
	}

	async dispose(): Promise<void> {
		if (this.disposePromise) return this.disposePromise;
		this.disposed = true;
		this.disposePromise = (async () => {
			const sessionIds = new Set([...this.sessions.keys(), ...this.initializing.keys(), ...this.closing.keys()]);
			await Promise.all([...sessionIds].map((sessionId) => this.closeSession(sessionId)));
			await Promise.all([...this.closing.values()]);
			await this.removeRuntimeRootIfIdle();
		})();
		return this.disposePromise;
	}

	private async ensureSession(
		sessionId: string,
		provider: AgentBrowserTargetProvider,
	): Promise<SessionRuntime> {
		if (this.disposed) throw runtimeError("AGENT_BROWSER_RUNTIME_CLOSED", "Browser automation runtime is closed");
		const existing = this.sessions.get(sessionId);
		if (existing) return existing;
		if (this.closing.has(sessionId)) {
			throw runtimeError("AGENT_BROWSER_SESSION_CLOSING", "Browser session is closing");
		}
		const pending = this.initializing.get(sessionId);
		if (pending) return pending;
		const creation = this.createSession(sessionId, provider);
		this.initializing.set(sessionId, creation);
		try {
			const runtime = await creation;
			if (this.disposed || this.closing.has(sessionId)) {
				await this.closeRuntime(sessionId, runtime);
				throw runtimeError("AGENT_BROWSER_SESSION_CLOSING", "Browser session is closing");
			}
			this.sessions.set(sessionId, runtime);
			return runtime;
		} finally {
			if (this.initializing.get(sessionId) === creation) this.initializing.delete(sessionId);
			await this.removeRuntimeRootIfIdle();
		}
	}

	private async createSession(sessionId: string, provider: AgentBrowserTargetProvider): Promise<SessionRuntime> {
		await this.ensureRuntimeRoot();
		const namespace = `${sessionNamespace(sessionId)}-${randomBytes(6).toString("hex")}`;
		assertAgentBrowserSocketPath(this.socketDir!, namespace, this.platform);
		const bridge = this.bridgeFactory(provider);
		let runtimeDir: string | undefined;
		try {
			const endpoint = await bridge.start();
			runtimeDir = path.join(this.runtimeRoot!, namespace);
			const configPath = path.join(runtimeDir, "config.json");
			await mkdir(runtimeDir, { recursive: true, mode: 0o700 });
			await writeFile(configPath, "{}\n", "utf8");
			return { bridge, endpoint, namespace, runtimeDir, configPath };
		} catch (error) {
			try {
				await closeBridgeWithTimeout(bridge);
			} catch (closeError) {
				this.log(`agent-browser bridge cleanup failed during startup: ${String(closeError)}`);
			}
			if (runtimeDir) await removePath(runtimeDir, this.log, "failed session directory");
			throw error;
		}
	}

	private async closeSessionInternal(sessionId: string): Promise<void> {
		const pending = this.initializing.get(sessionId);
		if (pending) {
			try {
				await pending;
			} catch {
				return;
			}
		}
		const runtime = this.sessions.get(sessionId);
		if (!runtime) return;
		this.sessions.delete(sessionId);
		await this.closeRuntime(sessionId, runtime);
	}

	private async closeRuntime(sessionId: string, runtime: SessionRuntime): Promise<void> {
		try {
			await this.processRunner(
				this.options.binaryPath,
				["close"],
				this.environment(runtime),
				undefined,
				CLOSE_TIMEOUT_MS,
			);
		} catch (error) {
			this.log(`agent-browser close failed for ${sessionId}: ${String(error)}`);
		}
		// `close` can fail or time out; never leave the native daemon behind.
		if (this.socketDir) {
			await reapAgentBrowserDaemons(this.socketDir, this.log, { ...this.options.reap, filter: (file) => file.includes(runtime.namespace) });
		}
		try {
			await closeBridgeWithTimeout(runtime.bridge);
		} catch (error) {
			this.log(`agent-browser bridge close failed for ${sessionId}: ${String(error)}`);
		} finally {
			await removePath(runtime.runtimeDir, this.log, `session directory for ${sessionId}`);
		}
	}

	private async ensureRuntimeRoot(): Promise<void> {
		if (this.runtimeRootPromise) return this.runtimeRootPromise;
		this.runtimeRootPromise = (async () => {
			await this.prepare();
			await ensurePrivateDirectory(this.options.dataDir);
			const rootName = `r-${randomBytes(5).toString("hex")}`;
			const root = path.join(this.options.dataDir, rootName);
			await mkdir(root, { recursive: false, mode: 0o700 });
			const owner: RuntimeOwner = {
				marker: RUNTIME_OWNER_MARKER,
				pid: process.pid,
				startedAt: new Date().toISOString(),
				token: randomBytes(16).toString("hex"),
			};
			try {
				await writeFile(path.join(root, RUNTIME_OWNER_FILE), `${JSON.stringify(owner)}\n`, {
					encoding: "utf8",
					flag: "wx",
				});
				const actualSocketDir = this.socketDirOverride ?? path.join(root, "s");
				await ensurePrivateDirectory(actualSocketDir);
				let socketDir = actualSocketDir;
				let socketDirAlias: string | null = null;
				if (!this.socketDirOverride && process.platform !== "win32") {
					// agent-browser applies the macOS 103-byte Unix socket limit on
					// every Unix build. Keep the real runtime state under ~/.tallpine, but
					// hand the native process a short symlink path whose length does
					// not depend on the user's home directory or username.
					socketDirAlias = path.join("/tmp", `tp-br-${process.pid}-${randomBytes(6).toString("hex")}`);
					await symlink(actualSocketDir, socketDirAlias, "dir");
					socketDir = socketDirAlias;
				}
				this.socketDir = socketDir;
				this.socketDirAlias = socketDirAlias;
			} catch (error) {
				if (this.socketDirAlias) {
					await removePath(this.socketDirAlias, this.log, "failed short socket alias");
					this.socketDirAlias = null;
				}
				await removePath(root, this.log, "failed runtime root");
				throw error;
			}
			this.runtimeRoot = root;
		})();
		try {
			await this.runtimeRootPromise;
		} catch (error) {
			this.runtimeRootPromise = null;
			throw error;
		}
	}

	private async removeRuntimeRootIfIdle(): Promise<void> {
		if (!this.runtimeRoot || this.sessions.size > 0 || this.initializing.size > 0 || this.closing.size > 0) return;
		const root = this.runtimeRoot;
		const socketDirAlias = this.socketDirAlias;
		this.runtimeRoot = null;
		this.runtimeRootPromise = null;
		this.socketDirAlias = null;
		if (socketDirAlias) await removePath(socketDirAlias, this.log, "short socket alias");
		await removePath(root, this.log, "runtime root");
		this.socketDir = null;
	}

	private async touchRuntimeRoot(): Promise<void> {
		if (!this.runtimeRoot) return;
		try {
			const now = new Date();
			await utimes(path.join(this.runtimeRoot, RUNTIME_OWNER_FILE), now, now);
		} catch (error) {
			this.log(`agent-browser runtime heartbeat failed: ${String(error)}`);
		}
	}

	private environment(runtime: SessionRuntime): NodeJS.ProcessEnv {
		// The native runtime is a third-party executable. Inheriting Electron's
		// complete environment would expose shell credentials, cloud/API tokens,
		// proxy credentials, and runtime injection flags to it. Keep only process
		// execution/locale essentials and add Tallpine's explicitly scoped contract below.
		const environment: NodeJS.ProcessEnv = {};
		for (const [name, value] of Object.entries(process.env)) {
			if (value !== undefined && NATIVE_ENV_ALLOWLIST.has(name.toLowerCase())) environment[name] = value;
		}
		Object.assign(environment, {
			HOME: runtime.runtimeDir,
			USERPROFILE: runtime.runtimeDir,
			XDG_CONFIG_HOME: runtime.runtimeDir,
			XDG_CACHE_HOME: runtime.runtimeDir,
			TEMP: runtime.runtimeDir,
			TMP: runtime.runtimeDir,
			TMPDIR: runtime.runtimeDir,
			AGENT_BROWSER_CONFIG: runtime.configPath,
			AGENT_BROWSER_CDP: runtime.endpoint,
			AGENT_BROWSER_SOCKET_DIR: this.socketDir!,
			AGENT_BROWSER_SESSION: runtime.namespace,
			AGENT_BROWSER_NAMESPACE: runtime.namespace,
			AGENT_BROWSER_CONTENT_BOUNDARIES: "1",
			AGENT_BROWSER_MAX_OUTPUT: "50000",
			AGENT_BROWSER_IDLE_TIMEOUT_MS: "300000",
			AGENT_BROWSER_AUTO_CONNECT: "0",
		});
		return environment;
	}

	private async assertBinary(): Promise<void> {
		try {
			await access(this.options.binaryPath);
		} catch {
			throw runtimeError(
				"AGENT_BROWSER_NOT_INSTALLED",
				`Tallpine's browser automation component was not found at ${this.options.binaryPath}. Reinstall or rebuild the desktop app.`,
			);
		}
	}
}
