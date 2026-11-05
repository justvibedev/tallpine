// DaemonStatus is the supervisor → renderer handshake payload, shared by the
// Electron main process (which derives it) and the preload bridge (which types
// the IPC surface). The renderer picks it up through the preload's TallpineBridge type.
// Machine-readable failure classification for telemetry. `message` is
// human-facing and may contain local paths; `code` is what gets reported.
// Statuses without a code (normal ready, user-initiated stop) are not failures.
export type DaemonFailureCode =
	| "not_configured"
	| "daemon_unreachable"
	| "binary_missing"
	| "signature_invalid"
	| "spawn_failed"
	| "exited"
	| "port_unconfirmed"
	| "not_ready"
	| "identity_mismatch"
	| "datadir_unwritable"
	// The daemon exited 75: another Tallpine process still holds the data-dir lock.
	| "datadir_locked";

export type DaemonStatus = {
	state: "starting" | "ready" | "stopped" | "error";
	port?: number;
	pid?: number;
	executablePath?: string;
	workingDirectory?: string;
	message?: string;
	// Recent daemon stdout/stderr retained by the Electron supervisor for local
	// troubleshooting. It is never sent to telemetry.
	details?: string;
	code?: DaemonFailureCode;
	exitCode?: number | null;
	signal?: string | null;
	// A starting daemon that is waiting for its predecessor to release the data dir.
	phase?: "datadir_lock_wait";
	// PID recorded in the data-dir lock by the process that still holds it.
	lockHolderPid?: number;
	// The daemon's own last error line (for example its lock refusal).
	daemonError?: string;
};
