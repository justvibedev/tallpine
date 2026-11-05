/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
BaseWindow
} from "electron";
import { type ChildProcess } from "node:child_process";
import {
type ChatDraftBoundaryKind,
type ChatDraftDialogCopy
} from "../shared/chat-draft-risk";
import type { DaemonStatus } from "../shared/daemon-status";
import {
type KeybindingOverrides
} from "../shared/shortcuts";
import { DEFAULT_TERMINAL_SHELL, type TerminalShellPreference } from "../shared/ui-locale";
import { BrowserProfileImportService } from "./browser-profile-import/service";
import {
type BrowserProfileIpc
} from "./browser-profile-ipc";
import { type BrowserRuntimeIdentity } from "./browser-runtime-identity";
import {
type BrowserRuntimeLinkHandle
} from "./browser-runtime-link";
import {
type BrowserStreamLinkHandle
} from "./browser-stream-link";
import {
type BrowserViewHost
} from "./browser-view-host";
import type { GitHubOwner } from "./ipc/github-ipc";
import { type SupervisorLinkHandle } from "./supervisor-link";
import { type TrayController } from "./tray";
import {
DEFAULT_UI_SETTINGS
} from "./ui-settings";
import { type WindowComposition } from "./window-composition";

/** Mutable main-process state shared by the split modules (formerly module-level lets in main.ts). */
export const ctx = {
	mainWindow: (null) as BaseWindow | null,
	// One adopted-daemon replacement per launch; see handleBrowserRuntimeRejection.
	browserRuntimeRecoveryAttempted: false,
	// Set when a recovery asks for a replacement and cleared once we own the daemon,
	// so an ordinary later startup is not forced to retire it. See startDaemon.
	forceReplaceAdoptedRequested: false,
	trayController: (null) as TrayController | null,
	// Folder path from a cold-start launch (icon/shortcut drop while not running)
	// whose renderer isn't mounted yet. Flushed once the shell signals readiness
	// via TRAY_RENDERER_READY_CHANNEL — see the OPEN_FOLDER_PATH_CHANNEL handler.
	pendingFolderPath: (null) as string | null,
	daemonProcess: (null) as ChildProcess | null,
	daemonStoppingProcess: (null) as ChildProcess | null,
	daemonRestartAfterExitProcess: (null) as ChildProcess | null,
	daemonStartPromise: (null) as Promise<DaemonStatus> | null,
	daemonStartEpoch: 0,
	daemonStartIsAutoRestart: false,
	daemonStatus: ({ state: "stopped" }) as DaemonStatus,
	daemonAttachmentGeneration: 0,
	daemonOutput: "",
	browserViewHost: (null) as BrowserViewHost | null,
	browserProfileIpc: (null) as BrowserProfileIpc | null,
	browserProfileImporter: (null) as BrowserProfileImportService | null,
	windowComposition: (null) as WindowComposition | null,
	browserQuitCleanupPromise: (null) as Promise<void> | null,
	browserCleanupComplete: false,
	browserQuitRequested: false,
	// Set by the updater right before Squirrel quits to install a staged update.
	updateRestartRequested: false,
	createWindowPromise: (null) as Promise<void> | null,
	browserRuntimeLink: (null) as BrowserRuntimeLinkHandle | null,
	browserRuntimeLinkIdentity: (null) as BrowserRuntimeIdentity | null,
	browserStreamLink: (null) as BrowserStreamLinkHandle | null,
	browserStreamLinkIdentity: (null) as BrowserRuntimeIdentity | null,
	keybindingOverrides: ({}) as KeybindingOverrides,
	keybindingRecordingActive: false,
	closeShellTerminalShortcutEnabled: false,
	terminalFocused: false,
	terminalFontSizeFocused: false,
	chatDraftRisks: ([]) as ChatDraftBoundaryKind[],
	chatDraftDialog: (undefined) as ChatDraftDialogCopy | undefined,
	chatDraftQuitConfirmed: false,
	chatDraftWindowCloseConfirmed: false,
	// Held for the app lifetime. Dropping it (on any exit) triggers daemon self-stop.
	supervisorLink: (null) as SupervisorLinkHandle | null,
	// Guard: prevents stacking multiple flashFrame(true) calls when notifications arrive rapidly.
	isFlashing: false,
	// macOS: the in-flight dock bounce, cancelled once the window regains focus
	// so a "critical" bounce stops as soon as the user looks at the app. Held as
	// one object so the id and its criticality can never drift apart; a pending
	// critical bounce is not replaced by a later informational notification.
	pendingBounce: (null) as { id: number; critical: boolean } | null,
	// Live mirror of the persisted `soundNotificationsEnabled` UI setting, kept in sync by the
	// uiSettings:set handler so a toggle flip takes effect without an app restart.
	soundNotificationsEnabled: DEFAULT_UI_SETTINGS.soundNotificationsEnabled,
	// Renderer-resolved sidebar colour. The shell root is transparent while a live
	// browser page shows, so the native window background must match it or the
	// gutters around the panels render in the fallback colour.
	rendererWindowBackground: (null) as string | null,
	// macOS window transparency: the shell paints translucent surfaces and the
	// native vibrancy material underneath blurs the desktop.
	windowTranslucent: false,
	sideClaimedDaemonPID: (undefined) as number | undefined,
	sideLaunchState: (undefined) as { appRunId: string; sides: unknown[]; numbers?: Record<string, number> } | undefined,
	sideStateTimer: (undefined) as ReturnType<typeof setInterval> | undefined,
	sideStatePolling: false,
	sideStateRequest: 0,
	sideStateApplied: 0,
	// The login-shell env resolved once at startup (see docs/daemon-environment.md),
	// or null when the probe failed/timed out. Read synchronously by daemonEnv().
	cachedShellEnv: (null) as Record<string, string> | null,
	// Memoize the in-flight resolution so concurrent/repeat awaits are cheap.
	shellEnvPromise: (null) as Promise<void> | null,
	terminalShellPreference: ({ ...DEFAULT_TERMINAL_SHELL }) as TerminalShellPreference,
	// The daemon spawn waits on this probe (0.2-2s), so start it with the process
	// rather than after the window opens. Windows probes the shell chosen in UI
	// settings, which are not loaded yet, so it still starts with the daemon.
	earlyShellEnvProbe: (null) as Promise<void> | null,
	stagedBundledTmuxBinary: (null) as string | null,
	cachedGitHubOwners: ([]) as GitHubOwner[],
};
