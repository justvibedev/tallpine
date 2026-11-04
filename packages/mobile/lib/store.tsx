import AsyncStorage from "@react-native-async-storage/async-storage";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

// Aliased: this file already declares its own `AppState` type for the
// provider's context value, so the React Native app-lifecycle API imports
// under a different name to avoid colliding with it.
import { AppState as RNAppState } from "react-native";
import { shouldPoll } from "./appStatePoll";
import { type DashboardPR, type DashboardSession, type DashboardStats, type OrchestratorLink, type ProjectInfo, type SessionMode, type SpawnAttachmentInput } from "./api";
import { scopeLaunchConductor } from "./hostScopeLaunch";
import { isConfigured, machineIdentity, type ServerConfig } from "./config";
import { type ConnectOptions } from "./connectRuntime";
import type { Endpoint } from "./endpoints";
import { loadHosts, sameHostConnections, type Host } from "./hosts";
import { isDesktopUnreachable } from "./connectionError";
import { primeInstallId } from "./installId";
import { collectPRs } from "./prView";
import { ALL_PROJECTS, NO_PROJECTS_KNOWN, projectsForMachine, resolveActiveProject, sessionRowsForMachine, type KnownProjects } from "./projectFilter";
import { useConversationEventTransport } from "./chat/conversationEvents";
import { useOtherHosts, type HostSnapshot } from "./otherHosts";
import { type ArchiveSummary } from "./archive/archiveSummary";
import { type HostedOrchestrator, type HostedProject, type HostedSession } from "./hostedRows";
import { useHostActions } from "./store/useHostActions";
import { useHostedState } from "./store/useHostedState";
import { useSessionsPoll } from "./store/useSessionsPoll";
import { usePairingLifecycle } from "./store/usePairingLifecycle";
import { useHostSwitching } from "./store/useHostSwitching";
import { useReloadConfig } from "./store/useReloadConfig";

const ACTIVE_PROJECT_KEY = "ao.activeProject";

// Board-level connection state is derived from the REST poll. The session screen
// tracks its own terminal mux connection separately.
export type ConnStatus = "closed" | "connecting" | "open";

// An options object rather than four optional positionals: `spawn(a, b, c, d)`
// with every argument optional and same-typed is where call-site mistakes live.
export type SpawnOptions = {
	/** Prevent a stale composer from posting a same-ID project to a new machine. */
	hostId: string;
	/** Falls back to the active project, or the only project. The standalone picker value skips that fallback. */
	projectId?: string;
	prompt?: string;
	harness?: string;
	model?: string;
	attachments?: SpawnAttachmentInput[];
	/** Mobile defaults to Chat; TUI remains an explicit compatibility choice. */
	mode?: SessionMode;
	clientRequestId?: string;
	/** Send the task to the default orchestrator although a stopped lane is selected. */
	useDefaultLane?: boolean;
	/** Parent the worker to this lane orchestrator, e.g. the one just started; beats the stored selection. */
	laneParentId?: string;
};

export type AppState = {
	config: ServerConfig | null;
	configured: boolean;
	/** Paired machine owning this view, even while its endpoint is offline. */
	currentHostId: string | undefined;
	selectedHostName: string | null;
	/** One independently connected snapshot per paired machine. */
	hostStates: HostSnapshot[];
	allSessions: HostedSession[];
	allProjects: HostedProject[];
	allOrchestrators: HostedOrchestrator[];
	/** Whether the first config resolution has finished. Until it has, an
	 *  unconfigured store means "still finding the machine", not "unpaired". */
	configResolved: boolean;
	/** Every way the active machine says it can be reached, for telling a
	 *  rotated tunnel hostname apart from being simply out of range. */
	activeEndpoints: Endpoint[];
	projects: ProjectInfo[];
	/** Whether the current projects value came from the latest daemon response. */
	projectsKnown: boolean;
	sessions: DashboardSession[];
	/**
	 * This machine's archive size when it pages its archive, which `sessions`
	 * then leaves out. Null for a daemon that still sends the archive inline.
	 */
	archive: ArchiveSummary | null;
	orchestrators: OrchestratorLink[];
	orchestratorId: string | null;
	stats: DashboardStats;
	activeProjectId: string; // 'all' or a projectId
	connection: ConnStatus;
	/** Unread notification count, for the board's bell badge. 0 when unknown. */
	notificationsUnread: number;
	loading: boolean;
	error: string | null;
	// HTTP status behind `error`, or null when the server was never reached.
	errorStatus: number | null;
	/** The daemon's envelope code behind errorStatus, when it sent one. */
	errorCode?: string | null;
	/**
	 * The last poll failed because nothing answered, so a reconnect can clear it.
	 * False for rejections (401/403/429), which stop the poll, and for 5xx.
	 */
	unreachable: boolean;
	/**
	 * When the last successful poll landed, in epoch milliseconds. 0 if none has.
	 *
	 * Deliberately a getter rather than a value: a timestamp that changed on every
	 * successful tick would re-render every consumer of this store once per poll.
	 * Read it through useStaleness, which owns the clock.
	 */
	getLastSyncAt: () => number;
	// actions
	/**
	 * Races the selected machine's endpoints. Null means none passed the host
	 * identity check, so a cached address must not receive its credential.
	 */
	reloadConfig: (options?: ConnectOptions) => Promise<ServerConfig | null>;
	switchHost: (id: string) => Promise<void>;
	refresh: () => Promise<void>;
	refreshHost: (hostId: string) => Promise<void>;
	refreshAll: () => Promise<void>;
	reloadHostMetadata: () => Promise<void>;
	configForHost: (hostId: string) => ServerConfig | null;
	setActiveProject: (id: string) => void;
	spawn: (opts: SpawnOptions) => Promise<DashboardSession>;
	launchConductor: (projectId: string, clean?: boolean, mode?: SessionMode, hostId?: string, lane?: string) => Promise<OrchestratorLink>;
	merge: (pr: DashboardPR, hostId?: string) => Promise<void>;
	kill: (id: string, hostId?: string) => Promise<void>;
	renameWorker: (id: string, displayName: string, hostId?: string) => Promise<void>;
	setWorkerPinned: (id: string, pinned: boolean, hostId?: string) => Promise<void>;
	restore: (id: string, hostId?: string) => Promise<void>;
	/** Restart a stopped agent without restoring a terminated Tallpine session. */
	resumeAgent: (id: string, hostId?: string) => Promise<void>;
	send: (id: string, message: string, hostId?: string) => Promise<void>;
};

const AppContext = createContext<AppState | null>(null);

export function useApp(): AppState {
	const ctx = useContext(AppContext);
	if (!ctx) throw new Error("useApp must be used within <AppProvider>");
	return ctx;
}

/** Reuse the normal session/project screens with a different daemon behind them. */
export function HostScope({ hostId, children }: { hostId: string; children: ReactNode }) {
	const app = useApp();
	const host = app.hostStates.find((item) => item.hostId === hostId);
	const isSelected = app.currentHostId === hostId;
	const scoped = useMemo<AppState>(() => {
		if (isSelected) return app;
		const config = host?.config ?? null;
		const projects = host?.projects ?? [];
		return {
			...app,
			config,
			configured: !!config && isConfigured(config),
			currentHostId: host?.hostId,
			selectedHostName: host?.name ?? null,
			activeEndpoints: host?.endpoints ?? [],
			projects,
			projectsKnown: host?.projectsKnown ?? false,
			sessions: host?.sessions ?? [],
			archive: host?.archive ?? null,
			orchestrators: host?.orchestrators ?? [],
			orchestratorId: host?.orchestratorId ?? null,
			stats: host?.stats ?? {},
			activeProjectId: resolveActiveProject(app.activeProjectId, projects, host?.projectsKnown ?? false),
			connection: host?.connection ?? "closed",
			notificationsUnread: host?.notificationsUnread ?? 0,
			loading: host?.loading ?? false,
			error: host?.error ?? null,
			errorStatus: host?.errorStatus ?? null,
			unreachable: isDesktopUnreachable({ connection: host?.connection ?? "closed", error: host?.error ?? null, errorStatus: host?.errorStatus ?? null }),
			getLastSyncAt: () => host?.lastSyncAt ?? 0,
			refresh: () => app.refreshHost(hostId),
			spawn: (opts) => app.spawn({ ...opts, hostId }),
			launchConductor: scopeLaunchConductor(app.launchConductor, hostId),
			merge: (pr) => app.merge(pr, hostId),
			kill: (id) => app.kill(id, hostId),
			renameWorker: (id, name) => app.renameWorker(id, name, hostId),
			setWorkerPinned: (id, pinned) => app.setWorkerPinned(id, pinned, hostId),
			restore: (id) => app.restore(id, hostId),
			resumeAgent: (id) => app.resumeAgent(id, hostId),
			send: (id, message) => app.send(id, message, hostId),
		};
	}, [app, host, hostId, isSelected]);
	return <AppContext.Provider value={scoped}>{children}</AppContext.Provider>;
}

function HostConversationTransport({ config }: { config: ServerConfig }) {
	useConversationEventTransport(config);
	return null;
}

// Convenience selectors -------------------------------------------------------

export function useVisibleSessions(): DashboardSession[] {
	const { sessions, activeProjectId } = useApp();
	return useMemo(
		() => (activeProjectId === "all" ? sessions : sessions.filter((s) => s.projectId === activeProjectId)),
		[sessions, activeProjectId],
	);
}

export function usePRs() {
	const sessions = useVisibleSessions();
	return useMemo(() => collectPRs(sessions), [sessions]);
}

// Provider --------------------------------------------------------------------

export function AppProvider({ children }: { children: ReactNode }) {
	const [pairedHosts, setPairedHosts] = useState<Host[]>([]);
	const [selectedHostId, setSelectedHostId] = useState<string | null>(null);
	const [config, setConfig] = useState<ServerConfig | null>(null);
	// Whether resolution has finished at least once. Distinguishes "no config
	// yet" from "no machine paired" — identical as state, opposite to the user.
	const [configResolved, setConfigResolved] = useState(false);
	const [selectedHostName, setSelectedHostName] = useState<string | null>(null);
	const [activeEndpoints, setActiveEndpoints] = useState<Endpoint[]>([]);
	const [knownProjects, setKnownProjects] = useState<KnownProjects>(NO_PROJECTS_KNOWN);
	const [sessions, setSessions] = useState<DashboardSession[]>([]);
	const [archive, setArchive] = useState<ArchiveSummary | null>(null);
	const [orchestrators, setOrchestrators] = useState<OrchestratorLink[]>([]);
	const [sessionMachine, setSessionMachine] = useState("");
	const [orchestratorId, setOrchestratorId] = useState<string | null>(null);
	const [stats, setStats] = useState<DashboardStats>({});
	const [chosenProjectId, setChosenProjectId] = useState<string>(ALL_PROJECTS);
	const [connection, setConnection] = useState<ConnStatus>("closed");
	const [notificationsUnread, setNotificationsUnread] = useState(0);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string | null>(null);
	const [errorStatus, setErrorStatus] = useState<number | null>(null);
	// The daemon's envelope code behind errorStatus, so a 401/403 can be told
	// apart (credential vs. policy) without parsing prose.
	const [errorCode, setErrorCode] = useState<string | null>(null);
	// Read from async resolution, where the state above would be a stale closure.
	const errorStatusRef = useRef<number | null>(null);
	errorStatusRef.current = errorStatus;
	const cfgRef = useRef<ServerConfig | null>(null);
	const mountedRef = useRef(true);
	const primaryLoadRef = useRef<{
		config: ServerConfig;
		controller: AbortController;
		promise: Promise<boolean>;
		queued?: Promise<boolean>;
	} | null>(null);
	const promotedConfigRef = useRef<ServerConfig | null>(null);
	const primarySnapshotRef = useRef<HostSnapshot | null>(null);
	const configResolution = useRef(0);
	// The machine the board was showing when the app went to the background.
	// Backgrounding drops the connection (see the AppState effect), and without
	// this the board hid its rows until the endpoint race finished, so a
	// one-second trip to another app came back to a full-screen loader. Rows
	// are still keyed to this machine, so they show only while it is the one
	// selected. "" means nothing is held. The ref keeps the host id for
	// reloadConfig, which must not wipe rows for the same host on resume.
	const [heldMachine, setHeldMachine] = useState("");
	const heldHostIdRef = useRef<string | null>(null);
	const releaseHeld = useCallback(() => {
		heldHostIdRef.current = null;
		setHeldMachine("");
	}, []);
	// Gate for the connected event: emit only on the not-open -> open transition,
	// never on every poll tick. openRef tracks the current state; everConnectedRef
	// tells a fresh launch apart from a later reconnect.
	const openRef = useRef(false);
	// Whether the most recent poll reached the daemon. Distinct from openRef,
	// which latches on first connect and never clears.
	const lastTickOkRef = useRef(false);
	// When the last successful poll landed, for the stale-data banner. 0 means
	// "never synced".
	//
	// A ref rather than state, and read through a stable getter below, because a
	// fresh timestamp in the context value on every successful tick would
	// re-render every consumer of this store once per poll — which is precisely
	// what "re-render the board on a change, not on the poll tick" removed. Only
	// the banner subscribes to the passage of time; the board does not.
	const lastSyncAtRef = useRef(0);
	// Whether the last failure had no HTTP status — nothing answered at all,
	// which is what leaving a network looks like.
	const lastFailUnreachableRef = useRef(false);
	const everConnectedRef = useRef(false);
	// Mirrors appActive for code that runs mid-flight, where reading the state
	// value would see a stale closure. fetchAll consults it between requests so a
	// poll interrupted by backgrounding does not fire its remaining calls — each
	// would carry the install-id header and keep the device "live" on the desktop
	// past the point the user left the app.
	const pollActiveRef = useRef(shouldPoll(RNAppState.currentState));

	useEffect(() => {
		mountedRef.current = true;
		pollActiveRef.current = shouldPoll(RNAppState.currentState);
		return () => {
			mountedRef.current = false;
			pollActiveRef.current = false;
			primaryLoadRef.current?.controller.abort();
			cfgRef.current = null;
			configResolution.current++;
		};
	}, []);

	// The poll is the daemon's liveness signal (see shouldPoll), so it must stop
	// while backgrounded rather than rely on the OS suspending the JS timer
	// whenever it feels like it.
	const [appActive, setAppActive] = useState(() => shouldPoll(RNAppState.currentState));
	const otherHosts = useOtherHosts(pairedHosts, selectedHostId, appActive);
	const reloadHostMetadata = useCallback(async () => {
		const hosts = await loadHosts();
		setPairedHosts((previous) => sameHostConnections(previous, hosts) ? previous : hosts);
	}, []);

	// Warm the install id cache as early as possible so the first REST poll tick
	// (fired from the config effect below) can send X-AO-Install-Id synchronously
	// via cachedInstallId() in api.ts's req(). A module-load side effect would run
	// this before React Native's AsyncStorage native module is guaranteed ready;
	// a mount-time effect matches this file's existing pattern (see the active
	// project load just below) and keeps the async I/O inside the component
	// lifecycle instead of hidden at import time.
	useEffect(() => {
		void primeInstallId();
	}, []);

	// Load persisted active project once.
	useEffect(() => {
		AsyncStorage.getItem(ACTIVE_PROJECT_KEY).then((v) => {
			if (v) setChosenProjectId(v);
		});
	}, []);
	const { failStreak, lastReRaceAt, resumedRef, reloadConfig } = useReloadConfig({ mountedRef, configResolution, setSelectedHostName, setSelectedHostId, cfgRef, heldHostIdRef, releaseHeld, primaryLoadRef, setConfig, setConfigResolved, setSessions, setArchive, setOrchestrators, setSessionMachine, setOrchestratorId, setStats, setNotificationsUnread, lastSyncAtRef, setPairedHosts, setError, setErrorStatus, setErrorCode, setConnection, errorStatusRef, setActiveEndpoints });
	const { switchHost } = useHostSwitching({ pollActiveRef, primaryLoadRef, configResolution, promotedConfigRef, cfgRef, heldHostIdRef, setHeldMachine, setConfig, setConfigResolved, setConnection, resumedRef, reloadConfig, setAppActive, otherHosts, primarySnapshotRef, setSelectedHostId, setSelectedHostName, setActiveEndpoints, setKnownProjects, setSessions, setArchive, setOrchestrators, setSessionMachine, setOrchestratorId, setStats, setNotificationsUnread, lastSyncAtRef, setLoading, setError, setErrorStatus, setErrorCode, releaseHeld });
	usePairingLifecycle({ reloadConfig, mountedRef, setPairedHosts, pairedHosts, selectedHostId, appActive, configResolved, config, resumedRef, lastReRaceAt });
	const { fetchAll } = useSessionsPoll({ mountedRef, pollActiveRef, cfgRef, setKnownProjects, setSessions, setArchive, setOrchestrators, setSessionMachine, setOrchestratorId, setStats, setError, setErrorStatus, setErrorCode, setConnection, lastTickOkRef, lastSyncAtRef, openRef, everConnectedRef, setNotificationsUnread, lastFailUnreachableRef, setConfig, reloadConfig, setLoading, primaryLoadRef, promotedConfigRef, config, configResolved, appActive, failStreak, lastReRaceAt });

	const setActiveProject = useCallback((id: string) => {
		setChosenProjectId(id);
		AsyncStorage.setItem(ACTIVE_PROJECT_KEY, id).catch(() => {});
	}, []);

	// During a re-pair, the previous machine's retained list is not evidence
	// about the new machine. Keep it hidden until the active machine answers.
	// While resuming, the held machine stands in so the rows already on screen
	// stay up instead of a loader (see heldMachine).
	const activeMachine = config && isConfigured(config) ? machineIdentity(config) : heldMachine;
	const visibleSessions = useMemo(
		() => sessionRowsForMachine(sessions, sessionMachine, activeMachine),
		[sessions, sessionMachine, activeMachine],
	);
	// Like the rows: one machine's archive size is not evidence about another's.
	const visibleArchive = sessionMachine === activeMachine ? archive : null;
	const visibleOrchestrators = useMemo(
		() => sessionRowsForMachine(orchestrators, sessionMachine, activeMachine),
		[orchestrators, sessionMachine, activeMachine],
	);
	const { projects, known: projectsKnown } = projectsForMachine(
		knownProjects,
		activeMachine,
	);
	const activeProjectId = useMemo(
		() => resolveActiveProject(chosenProjectId, projects, projectsKnown),
		[chosenProjectId, projects, projectsKnown],
	);
	const { hostStates, allSessions, allProjects, allOrchestrators, configForHost, requireConfig, refreshHost, refreshAll } = useHostedState({ pairedHosts, selectedHostId, otherHosts, activeEndpoints, config, connection, loading, error, errorStatus, errorCode, projects, projectsKnown, visibleSessions, visibleArchive, visibleOrchestrators, orchestratorId, stats, notificationsUnread, lastSyncAtRef, primarySnapshotRef, cfgRef, mountedRef, fetchAll });
	const { spawn, launchConductor, merge, kill, renameWorker, setWorkerPinned, restore, resumeAgent, send, refresh } = useHostActions({ activeProjectId, projects, requireConfig, refreshHost, hostStates, selectedHostId, fetchAll });

	// Memoized so the provider doesn't hand every useApp() consumer a brand-new
	// object (causing re-renders) on each render. Re-renders now track real state changes.
	// Stable for the life of the provider, which is what lets it sit in the memo's
	// dependency list below without ever busting it.
	const getLastSyncAt = useCallback(() => lastSyncAtRef.current, []);

	const value = useMemo<AppState>(
		() => ({
			config,
			configured: !!config && isConfigured(config),
			currentHostId: selectedHostId ?? undefined,
			selectedHostName,
			hostStates,
			allSessions,
			allProjects,
			allOrchestrators,
			configResolved,
			activeEndpoints,
			projects,
			projectsKnown,
			sessions: visibleSessions,
			archive: visibleArchive,
			orchestrators: visibleOrchestrators,
			orchestratorId: sessionMachine === activeMachine ? orchestratorId : null,
			stats: sessionMachine === activeMachine ? stats : {},
			activeProjectId,
			connection,
			notificationsUnread: sessionMachine === activeMachine ? notificationsUnread : 0,
			loading,
			error,
			errorStatus,
			errorCode,
			unreachable: isDesktopUnreachable({ connection, error, errorStatus }),
			getLastSyncAt,
			reloadConfig,
			switchHost,
			refresh,
			refreshHost,
			refreshAll,
			reloadHostMetadata,
			configForHost,
			setActiveProject,
			spawn,
			launchConductor,
			merge,
			kill,
			renameWorker,
			setWorkerPinned,
			restore,
			resumeAgent,
			send,
		}),
		[
			config,
			selectedHostId,
			selectedHostName,
			hostStates,
			allSessions,
			allProjects,
			allOrchestrators,
			configResolved,
			projects,
			projectsKnown,
			visibleSessions,
			visibleArchive,
			visibleOrchestrators,
			sessionMachine,
			activeMachine,
			orchestratorId,
			stats,
			activeProjectId,
			connection,
			notificationsUnread,
			loading,
			error,
			errorStatus,
			errorCode,
			getLastSyncAt,
			reloadConfig,
			switchHost,
			refresh,
			refreshHost,
			refreshAll,
			reloadHostMetadata,
			configForHost,
			setActiveProject,
			spawn,
			launchConductor,
			merge,
			kill,
			renameWorker,
			setWorkerPinned,
			restore,
			resumeAgent,
			send,
		],
	);

	return <AppContext.Provider value={value}>
		{/* Exactly one live CDC stream per connected host, independent of which
		    routes remain mounted in the navigation stack. */}
		{hostStates.map((host) => host.config && host.connection === "open"
			? <HostConversationTransport key={host.hostId} config={host.config} />
			: null)}
		{children}
	</AppContext.Provider>;
}
