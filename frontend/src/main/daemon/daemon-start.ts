/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app
} from "electron";
import { existsSync } from "node:fs";
import { mkdir, readFile } from "node:fs/promises";
import os from "node:os";
import { brandEnv } from "../../shared/brand";
import {
resolveDaemonFromPort
} from "../../shared/daemon-attach";
import { createListenPortScanner, parseRunFile } from "../../shared/daemon-discovery";
import { resolveDaemonLaunch } from "../../shared/daemon-launch";
import {
slowDaemonStartupStatus
} from "../../shared/daemon-startup-status";
import type { DaemonStatus } from "../../shared/daemon-status";
import {
browserDaemonOwnershipDecision,
keepAliveForReplacement,
shouldReplaceDaemonForStartup
} from "../../shared/daemon-takeover";
import { adoptionSignatureError, bundledHelperPaths } from "../adopt-signature";
import { APP_DISPLAY_NAME } from "../app-bootstrap";
import { ctx } from "../app-context";
import { establishSupervisorLink } from "../browser-runtime-link-host";
import { appRunId, browserRuntimeToken, ensureBundledTmuxStaged, helperSignatureOptions } from "../bundled-runtime-staging";
import { keepDaemonAlive, shouldLinkOnAttach } from "../daemon-owner";
import { verifyBundledHelpers } from "../helper-signature";
import { legacyStagedTmuxRootFor, removeLegacyStagedTmux } from "../legacy-tmux-cleanup";
import { ensureShellEnv } from "../shell-env-host";
import { appendDaemonOutput, daemonIdentityError, daemonLaunchEnv, gracefullyReplaceDaemonForBrowser, inspectExistingDaemon, PORT_DISCOVERY_TIMEOUT_MS, repairDevAcpRuntime, resolvedDaemonPort, RUN_FILE_FRESHNESS_SKEW_MS, RUN_FILE_POLL_MS, runFilePath, setDaemonStatus, startDaemonForRestart } from "./daemon-lifecycle";
import { reclaimOrphanedPortHolder } from "./daemon-port-takeover";
import { readDaemonProbe } from "./daemon-probe";
import { spawnDaemonProcess } from "./daemon-spawn";

export async function startDaemonInner(startEpoch: number): Promise<DaemonStatus> {
	if (ctx.daemonProcess) {
		return ctx.daemonStatus;
	}

	await repairDevAcpRuntime();
	if (startEpoch !== ctx.daemonStartEpoch) {
		return ctx.daemonStatus;
	}

	// Single chokepoint: make sure the login-shell env is resolved before the
	// daemon is spawned, so a Finder/Dock launch hands the daemon a real PATH and
	// shell-exported credentials rather than launchd's minimal env. The probe
	// spawns the user's login shell (0.2-2s with a heavy profile) and only feeds
	// the spawn env, so start it now but only wait for it when we actually spawn:
	// attaching to a running daemon (the common relaunch) no longer waits on it.
	const shellEnvReady = ensureShellEnv();
	// Attach paths return without awaiting it; keep a rejection observed there.
	shellEnvReady.catch(() => undefined);

	const launch = resolveDaemonLaunch(
		daemonLaunchEnv(),
		app.isPackaged,
		process.resourcesPath,
		app.getAppPath(),
		os.homedir(),
		process.platform,
	);
	if (!launch) {
		setDaemonStatus({
			state: "stopped",
			message: `${brandEnv("DAEMON_COMMAND")} is not configured; renderer uses loopback REST when available.`,
			code: "not_configured",
		});
		return ctx.daemonStatus;
	}

	let replacementKeepAlive: boolean | undefined;
	const existing = await inspectExistingDaemon(launch);
	if (startEpoch !== ctx.daemonStartEpoch) {
		return ctx.daemonStatus;
	}
	if (existing) {
		const ownership = browserDaemonOwnershipDecision(appRunId, existing);
		if (shouldReplaceDaemonForStartup({ ownership, forceRequested: ctx.forceReplaceAdoptedRequested })) {
			try {
				await gracefullyReplaceDaemonForBrowser(existing.status);
				replacementKeepAlive = keepAliveForReplacement({
					ownership,
					forceRequested: ctx.forceReplaceAdoptedRequested,
				});
			} catch (err) {
				setDaemonStatus({
					state: "error",
					message: `Could not take ownership of the browser runtime: ${(err as Error).message}`,
					code: "not_ready",
				});
				return ctx.daemonStatus;
			}
		} else {
			const adoptError = await adoptionSignatureError(launch, process.resourcesPath, helperSignatureOptions());
			if (startEpoch !== ctx.daemonStartEpoch) {
				return ctx.daemonStatus;
			}
			if (adoptError) {
				setDaemonStatus({ state: "error", message: adoptError, code: "signature_invalid" });
				return ctx.daemonStatus;
			}
			setDaemonStatus(existing.status);
			// Re-link the supervisor only when attaching to an app-owned daemon (one we
			// previously spawned). Headless `tallpine start` daemons (owner unset) stay unlinked
			// so they remain persistent after app quit.
			if (shouldLinkOnAttach(existing.owner)) {
				establishSupervisorLink();
			}
			return ctx.daemonStatus;
		}
	}

	// Defensive: inspectExistingDaemon only attaches when the run-file agrees with
	// a live daemon. Any divergence (missing/stale/unparseable run-file, dead PID,
	// health.pid mismatch) makes it return null — yet a daemon may still be serving
	// the port. Spawning then would just make the Go child refuse and exit 1. Probe
	// the expected port directly, independent of the run-file, and attach if a
	// daemon answers. The expected port (TALLPINE_PORT or the default) is exactly the
	// port the Go child would bind and collide on — probing a hardcoded 3101 would
	// miss an TALLPINE_PORT override.
	const directDaemon = await resolveDaemonFromPort({
		expectedPort: resolvedDaemonPort(),
		probe: readDaemonProbe,
		identityError: (probe) => daemonIdentityError(launch, probe),
	});
	if (startEpoch !== ctx.daemonStartEpoch) {
		return ctx.daemonStatus;
	}
	if (directDaemon) {
		let portAttachOwner: string | undefined;
		let portAttachAppRunId: string | undefined;
		// Re-link iff the daemon is app-owned. Read the run-file for the owner tag;
		// if unavailable (run-file absent or unreadable), treat as headless and skip.
		// ponytail: narrow TOCTOU here (the port was probed live, then the run-file
		// is read separately), so in theory a headless daemon could have replaced an
		// app-owned one in the gap. Acceptable: the window is tiny, the worst case is
		// linking a headless daemon, and establishSupervisorLink disposes any prior
		// link so nothing leaks.
		const rfp = runFilePath();
		if (rfp) {
			try {
				const info = parseRunFile(await readFile(rfp, "utf8"));
				portAttachOwner = info?.owner;
				portAttachAppRunId = info?.appRunId;
			} catch {
				// run-file absent or unreadable: treat as headless, skip link.
			}
		}
		const ownership = browserDaemonOwnershipDecision(appRunId, {
			owner: portAttachOwner,
			appRunId: portAttachAppRunId,
		});
		if (shouldReplaceDaemonForStartup({ ownership, forceRequested: ctx.forceReplaceAdoptedRequested })) {
			try {
				await gracefullyReplaceDaemonForBrowser(directDaemon);
				replacementKeepAlive = keepAliveForReplacement({
					ownership,
					forceRequested: ctx.forceReplaceAdoptedRequested,
				});
			} catch (err) {
				setDaemonStatus({
					state: "error",
					message: `Could not take ownership of the browser runtime: ${(err as Error).message}`,
					code: "not_ready",
				});
				return ctx.daemonStatus;
			}
		} else {
			const adoptError = await adoptionSignatureError(launch, process.resourcesPath, helperSignatureOptions());
			if (startEpoch !== ctx.daemonStartEpoch) {
				return ctx.daemonStatus;
			}
			if (adoptError) {
				setDaemonStatus({ state: "error", message: adoptError, code: "signature_invalid" });
				return ctx.daemonStatus;
			}
			setDaemonStatus(directDaemon);
			if (shouldLinkOnAttach(portAttachOwner)) establishSupervisorLink();
			return ctx.daemonStatus;
		}
	}

	// Every path from here spawns a daemon, which needs the login-shell env.
	await shellEnvReady;
	if (startEpoch !== ctx.daemonStartEpoch) {
		return ctx.daemonStatus;
	}
	// The early probe runs while Electron itself is starting, the busiest moment
	// of launch. If it failed there, probe once more before settling for a bare
	// PATH for the daemon's whole run.
	if (!ctx.cachedShellEnv && ctx.earlyShellEnvProbe && ctx.shellEnvPromise === ctx.earlyShellEnvProbe) {
		ctx.earlyShellEnvProbe = null;
		ctx.shellEnvPromise = null;
		await ensureShellEnv();
		if (startEpoch !== ctx.daemonStartEpoch) {
			return ctx.daemonStatus;
		}
	}

	await reclaimOrphanedPortHolder();

	if (launch.source === "bundled" && !existsSync(launch.command)) {
		setDaemonStatus({
			state: "error",
			message: `Bundled ${APP_DISPLAY_NAME} daemon binary was not found at ${launch.command}. Rebuild the desktop package.`,
			code: "binary_missing",
		});
		return ctx.daemonStatus;
	}
	try {
		await ensureBundledTmuxStaged();
	} catch (err) {
		const signatureFailure = (err as { code?: string }).code === "signature_invalid";
		setDaemonStatus({
			state: "error",
			message: signatureFailure
				? (err as Error).message
				: `Could not stage ${APP_DISPLAY_NAME}'s bundled tmux under the ${APP_DISPLAY_NAME} data directory: ${(err as Error).message}`,
			code: signatureFailure ? "signature_invalid" : "binary_missing",
		});
		return ctx.daemonStatus;
	}

	if (launch.source === "bundled") {
		const helpers = bundledHelperPaths(launch.command, process.resourcesPath, process.platform);
		const sigError = await verifyBundledHelpers(helpers, helperSignatureOptions());
		if (sigError) {
			setDaemonStatus({ state: "error", message: sigError, code: "signature_invalid" });
			return ctx.daemonStatus;
		}
	}

	ctx.daemonOutput = "";
	setDaemonStatus({ state: "starting" });
	if (launch.source === "bundled") {
		try {
			await mkdir(launch.cwd, { recursive: true, mode: 0o750 });
		} catch (err) {
			// A failure here (home unwritable, launch.cwd exists as a file) would
			// otherwise reject out of startDaemonInner; boot calls this via
			// `void startDaemon()`, so it would surface as an unhandled rejection
			// and leave the UI stuck on "starting". Report it as a failure instead.
			setDaemonStatus({
				state: "error",
				message: `Could not create the ${APP_DISPLAY_NAME} data directory at ${launch.cwd}: ${(err as Error).message}`,
				code: "datadir_unwritable",
			});
			return ctx.daemonStatus;
		}
	}

	// Capture the spawned handle locally so the async lifecycle listeners act only
	// on THIS process. Without this, a stale exit from an already-stopped daemon
	// could null out a newer daemonProcess started in the meantime, orphaning it.
	//
	// `detached` makes the child its own process-group leader. Because shell:true
	// runs the command through /bin/sh, a plain kill() would only signal the shell
	// wrapper and orphan the real daemon (which keeps holding the port). Killing
	// the whole group via killDaemon() reaches the daemon and any PTY children.
	//
	// TALLPINE_KEEP_DAEMON: the daemon must survive this app, so it cannot inherit
	// Electron-owned stdout/stderr pipes — when Electron exits, the pipe read
	// ends close and the daemon's next stderr log write (SIGPIPE/EPIPE) kills it,
	// defeating the keep-alive. Redirect stdio to ~/.tallpine/daemon.log and unref the
	// child so the parent does not wait on it. Port discovery then relies on the
	// running.json handshake (the log pipe scan is skipped).
	const keep = replacementKeepAlive ?? keepDaemonAlive(process.env);
	// We are about to own the daemon and hand it this launch's runtime token, so
	// any forced replacement has been honored and must not leak into a later
	// startup in this process.
	ctx.forceReplaceAdoptedRequested = false;
	const child = spawnDaemonProcess(launch, keep);
	// daemonEnv deliberately contains only a marker. Deliver the actual token
	// over the inherited pipe, then close it; Go does not pass this descriptor to
	// worker processes when it spawns them.
	if (child.stdin) {
		child.stdin.on("error", () => undefined);
		child.stdin.end(`${browserRuntimeToken}\n`);
	} else {
		console.warn("Tallpine: browser runtime token handoff pipe was unavailable");
	}
	if (keep) child.unref();
	ctx.daemonProcess = child;

	// Discover the port the daemon ACTUALLY bound rather than trusting TALLPINE_PORT:
	// the daemon may fall back to a different port than the one requested. Two
	// confirmed sources race — the "daemon listening" slog line (stderr, but both
	// streams are scanned) and the running.json handshake — first one wins.
	const spawnedAtMs = Date.now();
	let portConfirmed = false;
	let runFileTimer: ReturnType<typeof setInterval> | undefined;
	let fallbackTimer: ReturnType<typeof setTimeout> | undefined;

	const stopDiscovery = () => {
		if (runFileTimer) clearInterval(runFileTimer);
		runFileTimer = undefined;
		if (fallbackTimer) clearTimeout(fallbackTimer);
		fallbackTimer = undefined;
	};

	const reportBoundPort = (port: number) => {
		if (portConfirmed || ctx.daemonProcess !== child || ctx.daemonStoppingProcess === child) return;
		portConfirmed = true;
		stopDiscovery();
		setDaemonStatus({ state: "ready", port, pid: child.pid });
		// We spawned this daemon with the new tmux path, so tmux copies staged
		// under the old state root are unused: reclaim the disk.
		const legacyRoot = legacyStagedTmuxRootFor(process.env, os.homedir());
		if (ctx.stagedBundledTmuxBinary && legacyRoot) {
			void removeLegacyStagedTmux(legacyRoot, ctx.stagedBundledTmuxBinary).catch(() => undefined);
		}

		// Establish the OS-native liveness link on the spawn path (we own this
		// daemon). Holding the connection keeps the daemon alive; when Electron
		// exits for any reason, the OS closes the fd and the daemon detects EOF,
		// then self-stops after its ~5s grace period. The attach paths link only
		// when the daemon is app-owned (see establishSupervisorLink +
		// shouldLinkOnAttach); headless `tallpine start` daemons stay unlinked so they
		// remain persistent across app quit.
		//
		// TALLPINE_KEEP_DAEMON opts out of the link entirely: the daemon is spawned but
		// survives the window closing, stopping only on an explicit `tallpine stop`.
		// Reuse the `keep` captured at spawn rather than re-reading process.env
		// here: the flag is a property of this spawn, not a value that should be
		// able to flip between spawn and port-confirmation. (The process.on("exit")
		// orphan-cleanup below re-reads process.env because this `keep` is scoped
		// to the spawn function — TALLPINE_KEEP_DAEMON is set once at startup and never
		// mutated, so both reads agree.)
		if (!keep) {
			establishSupervisorLink();
		}
	};

	// One scanner per stream: each keeps its own partial-line buffer.
	// Skipped under TALLPINE_KEEP_DAEMON: stdio is redirected to a log file (no pipes
	// to scan), so port discovery falls back to the running.json handshake below.
	if (!keep) {
		// The daemon writes its own rotating ~/.tallpine/logs/daemon.log, so its output is
		// no longer echoed to this process's console (a per-chunk console.log in the
		// main process cost CPU and left no persistent log in a Finder launch). The
		// pipe is still scanned for the listening line and kept in daemonOutput for
		// startup-failure details. Set TALLPINE_DAEMON_CONSOLE=1 to echo it while developing.
		const echoDaemonOutput = process.env.TALLPINE_DAEMON_CONSOLE === "1";
		const scanStdout = createListenPortScanner(reportBoundPort);
		const scanStderr = createListenPortScanner(reportBoundPort);

		child.stdout?.on("data", (chunk: Buffer) => {
			const text = chunk.toString("utf8");
			appendDaemonOutput(text);
			if (echoDaemonOutput) console.log(text.trimEnd());
			scanStdout(text);
		});

		child.stderr?.on("data", (chunk: Buffer) => {
			const text = chunk.toString("utf8");
			appendDaemonOutput(text);
			if (echoDaemonOutput) console.error(text.trimEnd());
			scanStderr(text);
		});
	}

	const handshakePath = runFilePath();
	if (handshakePath) {
		runFileTimer = setInterval(() => {
			readFile(handshakePath, "utf8")
				.then((contents) => {
					const info = parseRunFile(contents);
					// Ignore a stale handshake left by a previous daemon: only trust a
					// file written at/after this spawn.
					if (info && info.startedAtMs >= spawnedAtMs - RUN_FILE_FRESHNESS_SKEW_MS) {
						reportBoundPort(info.port);
					}
				})
				.catch(() => undefined); // absent until the daemon binds; keep polling
		}, RUN_FILE_POLL_MS);
	}

	// Neither source confirmed startup yet. The child is still alive and both
	// discovery mechanisms remain active, so surface this as slow progress rather
	// than a failure. A later listen line or running.json handshake still moves
	// the same launch to ready.
	fallbackTimer = setTimeout(() => {
		if (portConfirmed || ctx.daemonProcess !== child || ctx.daemonStoppingProcess === child) return;
		// Keep running.json polling alive after surfacing slow startup. In
		// keep-daemon mode there are no stdout/stderr scanners, so the run file is
		// the only way a slow-but-successful boot can still recover to ready.
		fallbackTimer = undefined;
		setDaemonStatus(slowDaemonStartupStatus({
			output: ctx.daemonOutput,
			executablePath: launch.command,
			workingDirectory: launch.cwd,
			handshakePath,
		}));
	}, PORT_DISCOVERY_TIMEOUT_MS);

	child.once("error", (error) => {
		stopDiscovery();
		if (ctx.daemonProcess !== child) return;
		ctx.daemonProcess = null;
		if (ctx.daemonStoppingProcess === child) ctx.daemonStoppingProcess = null;
		setDaemonStatus({
			state: "error",
			message: error.message,
			details: ctx.daemonOutput.trim() || undefined,
			code: "spawn_failed",
		});
	});

	child.once("exit", (code, signal) => {
		stopDiscovery();
		if (ctx.daemonProcess !== child) return;
		ctx.daemonProcess = null;
		// An explicit stopDaemon() already set a clean `{ state: "stopped" }`.
		// Don't stamp `code: "exited"` on a stop the user or app asked for;
		// preserve the clean stopped status instead.
		if (ctx.daemonStoppingProcess === child) {
			ctx.daemonStoppingProcess = null;
			if (ctx.daemonRestartAfterExitProcess === child) {
				ctx.daemonRestartAfterExitProcess = null;
				void startDaemonForRestart();
				return;
			}
			setDaemonStatus({ state: "stopped" });
			return;
		}
		setDaemonStatus({
			state: "stopped",
			message: signal ? `Daemon exited with ${signal}` : `Daemon exited with code ${code ?? "unknown"}`,
			details: ctx.daemonOutput.trim() || undefined,
			code: "exited",
			exitCode: code,
			signal,
		});
	});

	return ctx.daemonStatus;
}
