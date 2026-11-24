/** Types of the in-page fake agent installed by fake-bridge.ts (installFakeAgent). */

export type FakeWorker = {
	id: string;
	title: string;
	createdAt?: string;
	mode?: "chat" | "tui";
	provider?: string;
	branch?: string;
	status?: string;
	activity?: "active" | "idle" | "waiting_input" | "exited";
	previewUrl?: string;
	previewRevision?: number;
};

export type FakeAgentOptions = {
	version?: string;
	daemonPort?: number;
	projectId?: string;
	projectName?: string;
	/** Override navigator.platform (e.g. "Linux …") so platform-gated UI mounts. */
	platform?: string;
	/** Worker sessions present at first paint. */
	workers?: FakeWorker[];
	/** Persist the fixture project's disclosure as expanded before the app mounts. */
	expandProject?: boolean;
};

/**
 * The in-page fake-agent controller, callable from specs via `page.evaluate`.
 * Normal mutations change the snapshot and push the matching SSE frame, so the
 * renderer repaints through its real invalidation path. Diagnostic controls
 * can deliberately defer or omit a frame to inspect the cache boundary.
 */
export type FakeAgentController = {
	snapshot: () => unknown[];
	createWorker: (worker: FakeWorker) => void;
	createWorkerWithoutEvent: (worker: FakeWorker) => void;
	createWorkerDuringNextSnapshot: (worker: FakeWorker) => void;
	removeWorker: (id: string) => void;
	setStatus: (id: string, status: string, activity?: string) => void;
	setTerminalHandle: (id: string, handleId: string) => void;
	/** Moves a session to another agent, as a provider switch does. */
	setProvider: (id: string, provider: string) => void;
	setPreview: (id: string, previewUrl: string, previewRevision?: number) => void;
	setBrowserError: (message: string | null) => void;
	reconnectEventsWithReplayBacklog: (count: number) => void;
	replayProgress: () => FakeReplayProgress;
	closeEventsStream: () => void;
	/** `openedAt` is the `Date.now()` at which the stream last fired `open`. */
	eventStreams: () => Array<{ url: string; readyState: number; openedAt: number | null }>;
	startContinuousWorkspaceEvents: (intervalMs: number, eventsPerTick?: number) => void;
	stopContinuousWorkspaceEvents: () => void;
	continuousWorkspaceEvents: () => FakePressureProgress;
	setSnapshotFailures: (count: number) => void;
	snapshotCallCount: () => number;
	notify: (n: { id: string; type: string; title: string; body?: string; sessionId?: string }) => void;
};

/** The current (or last) CDC gap replay: `remaining` frames are still queued,
 *  `abandoned` frames (deferred events included) were dropped when the app
 *  replaced the stream. The counters reset when a new replay starts. */
export type FakeReplayProgress = {
	phase: "idle" | "connecting" | "replaying";
	remaining: number;
	delivered: number;
	abandoned: number;
};

/** Continuous SSE pressure: frames the daemon has `sent` by elapsed time, and
 *  how many of them the renderer has been handed so far. */
export type FakePressureProgress = {
	sent: number;
	delivered: number;
};

declare global {
	interface Window {
		__tallpineFakeAgent?: FakeAgentController;
	}
}
