import { useCallback, useEffect } from "react";
import { ApiError, getNotifications, getSessions, type DashboardSession, type DashboardStats, type OrchestratorLink } from "../api";
import { isConfigured, machineIdentity, type ServerConfig } from "../config";
import { pollIntervalFor } from "../pollInterval";
import { rejectedEndpointNeedsRace, type ConnectOptions } from "../connectRuntime";
import { shouldReRace } from "../reRace";
import { pollResultIsCurrent, sameServerConfig } from "../sameConfig";
import { shouldShowLoading } from "../configLoading";
import { shouldKeepPolling, userFacingError } from "../connectionError";
import { retainProjects, type KnownProjects } from "../projectFilter";
import { sameArchiveSummary, type ArchiveSummary } from "../archive/archiveSummary";
import { reconcileRows } from "../rowSharing";
import { ConnStatus } from "../store";

export function useSessionsPoll({
	mountedRef,
	pollActiveRef,
	cfgRef,
	setKnownProjects,
	setSessions,
	setArchive,
	setOrchestrators,
	setSessionMachine,
	setOrchestratorId,
	setStats,
	setError,
	setErrorStatus,
	setErrorCode,
	setConnection,
	lastTickOkRef,
	lastSyncAtRef,
	openRef,
	everConnectedRef,
	setNotificationsUnread,
	lastFailUnreachableRef,
	setConfig,
	reloadConfig,
	setLoading,
	primaryLoadRef,
	promotedConfigRef,
	config,
	configResolved,
	appActive,
	failStreak,
	lastReRaceAt,
}: {
	mountedRef: React.RefObject<boolean>;
	pollActiveRef: React.RefObject<boolean>;
	cfgRef: React.RefObject<ServerConfig | null>;
	setKnownProjects: React.Dispatch<React.SetStateAction<KnownProjects>>;
	setSessions: React.Dispatch<React.SetStateAction<DashboardSession[]>>;
	setArchive: React.Dispatch<React.SetStateAction<ArchiveSummary | null>>;
	setOrchestrators: React.Dispatch<React.SetStateAction<OrchestratorLink[]>>;
	setSessionMachine: React.Dispatch<React.SetStateAction<string>>;
	setOrchestratorId: React.Dispatch<React.SetStateAction<string | null>>;
	setStats: React.Dispatch<React.SetStateAction<DashboardStats>>;
	setError: React.Dispatch<React.SetStateAction<string | null>>;
	setErrorStatus: React.Dispatch<React.SetStateAction<number | null>>;
	setErrorCode: React.Dispatch<React.SetStateAction<string | null>>;
	setConnection: React.Dispatch<React.SetStateAction<ConnStatus>>;
	lastTickOkRef: React.RefObject<boolean>;
	lastSyncAtRef: React.RefObject<number>;
	openRef: React.RefObject<boolean>;
	everConnectedRef: React.RefObject<boolean>;
	setNotificationsUnread: React.Dispatch<React.SetStateAction<number>>;
	lastFailUnreachableRef: React.RefObject<boolean>;
	setConfig: React.Dispatch<React.SetStateAction<ServerConfig | null>>;
	reloadConfig: (options?: ConnectOptions | undefined) => Promise<ServerConfig | null>;
	setLoading: React.Dispatch<React.SetStateAction<boolean>>;
	primaryLoadRef: React.RefObject<{ config: ServerConfig; controller: AbortController; promise: Promise<boolean>; queued?: Promise<boolean> | undefined; } | null>;
	promotedConfigRef: React.RefObject<ServerConfig | null>;
	config: ServerConfig | null;
	configResolved: boolean;
	appActive: boolean;
	failStreak: React.RefObject<number>;
	lastReRaceAt: React.RefObject<number>;
}) {

	// fetchAll returns false when it hit an auth failure (missing/wrong password
	// or a 429 lockout). The poll loop uses that to STOP hammering: a phone that
	// keeps polling with a bad password would otherwise rack up a failed attempt
	// every few seconds and keep the daemon's brute-force lockout armed forever.
	// Polling resumes when the config changes (the user fixes the password and
	// reconnects), which re-runs the effect below.
	const loadAll = useCallback(async (c: ServerConfig, controller: AbortController): Promise<boolean> => {
		const isCurrent = () => mountedRef.current && !controller.signal.aborted && pollActiveRef.current && pollResultIsCurrent(c, cfgRef.current);
		try {
			// getSessions returns projects, so don't fetch /projects again alongside
			// it — that duplicate doubled the auth attempts spent per failing tick.
			const sess = await getSessions(c, "all", { signal: controller.signal });
			// A poll that started against the previous pairing must not publish any
			// of its board state after the user has moved to another machine.
			if (!isCurrent()) return false;
			setKnownProjects((prev) => retainProjects(
				prev,
				{ machine: machineIdentity(c), projects: sess.projects },
				machineIdentity(c),
			));
			setSessions((previous) => reconcileRows(previous, sess.sessions));
			setArchive((previous) => (sameArchiveSummary(previous, sess.archive) ? previous : sess.archive ?? null));
			setOrchestrators((previous) => reconcileRows(previous, sess.orchestrators));
			setSessionMachine(machineIdentity(c));
			setOrchestratorId(sess.orchestratorId);
			setStats(sess.stats);
			setError(null);
			setErrorStatus(null);
				setErrorCode(null);
			setConnection("open");
			lastTickOkRef.current = true;
			lastSyncAtRef.current = Date.now();
			if (!openRef.current) {
				openRef.current = true;
				const trigger = everConnectedRef.current ? "reconnect" : "launch";
				everConnectedRef.current = true;
			}
			// Badge count for the board's bell. Deliberately after the session fetch
			// and separately caught: an older daemon without /notifications must not
			// knock the board offline. limit:1 because we only read unreadCount.
			// The app may have gone to the background while the sessions request was
			// in flight. Stop here rather than spending another request that would
			// re-mark this device live after the user left.
			if (!isCurrent()) return false;
			try {
				const page = await getNotifications(c, { status: "unread", limit: 1 }, { signal: controller.signal });
				if (!isCurrent()) return false;
				setNotificationsUnread(page.unreadCount);
			} catch {
				if (!isCurrent()) return false;
				setNotificationsUnread(0);
			}
			return true;
		} catch (e) {
			if (!isCurrent()) return false;
			lastTickOkRef.current = false;
			const msg = userFacingError(e, "Failed to load");
			setError(msg);
			// Keep the HTTP status alongside the raw message so screens can render
			// human copy via describeConnectionFailure instead of surfacing strings
			// like "401 - missing or invalid connection password". Null means the
			// server was never reached (DNS failure, refused, timeout).
			const status = e instanceof ApiError ? e.status : undefined;
			// No status means the server was never reached. That is the signal to
			// race again immediately rather than ride out another poll.
			lastFailUnreachableRef.current = status === undefined;
			setErrorStatus(status ?? null);
			const code = e instanceof ApiError ? e.code : undefined;
			setErrorCode(code ?? null);
			openRef.current = false;
			setConnection("closed");
			// Auth failures are not transient — don't keep polling into a lockout.
			// Network/other errors are transient, so keep polling for recovery.
			// Decided from the status, not the message text: see shouldKeepPolling.
			const keepPolling = shouldKeepPolling(status, code);
			if (status === 426) {
				cfgRef.current = null;
				setConfig(null);
			}
			if (await rejectedEndpointNeedsRace(c, status) && isCurrent()) {
				// A different machine can acquire the same LAN address while the app
				// stays foregrounded. Drop that URL before racing verified endpoints.
				cfgRef.current = null;
				setConfig(null);
				void reloadConfig().catch(() => {});
			}
			return keepPolling;
		} finally {
			if (isCurrent()) setLoading(false);
		}
	}, [reloadConfig]);

	const fetchAll = useCallback((afterCurrent = false): Promise<boolean> => {
		const c = cfgRef.current;
		if (!mountedRef.current || !c || !isConfigured(c) || !pollActiveRef.current) return Promise.resolve(false);
		const existing = primaryLoadRef.current;
		if (existing && !existing.controller.signal.aborted && sameServerConfig(existing.config, c)) {
			// A poll joins the load. A mutation/manual refresh requires one newer
			// load after it, shared by all callers instead of racing old responses.
			if (!afterCurrent) return existing.promise;
			existing.queued ??= existing.promise.then((keepGoing) =>
				keepGoing && !existing.controller.signal.aborted && pollResultIsCurrent(c, cfgRef.current)
					? fetchAll() : false);
			return existing.queued;
		}
		existing?.controller.abort();
		const controller = new AbortController();
		const load = { config: c, controller, promise: Promise.resolve(false) };
		primaryLoadRef.current = load;
		load.promise = loadAll(c, controller).finally(() => {
			if (primaryLoadRef.current === load) primaryLoadRef.current = null;
		});
		return load.promise;
	}, [loadAll]);

	// (Re)start the REST poll whenever the config changes. Stops polling on an
	// auth failure so the phone can't lock itself out by hammering a bad password.
	useEffect(() => {
		// A config change (unpair / re-pair / new host) restarts polling; reset the
		// connected gate so the first open of the new session is a real transition.
		const preserveVerifiedOpen = promotedConfigRef.current === config;
		promotedConfigRef.current = null;
		openRef.current = preserveVerifiedOpen;
		if (!config || !isConfigured(config)) {
			setConnection("closed");
			// Not simply false: until resolution has finished this is "still
			// finding a path", and turning the loader off here left the screen
			// rendering an empty list — a black screen — for the whole race.
			setLoading(shouldShowLoading({ resolved: configResolved, configured: false }));
			return;
		}
		if (!appActive) return; // backgrounded: stop polling, stop heartbeating
		if (!preserveVerifiedOpen) {
			setLoading(true);
			setConnection("connecting");
		}
		let stopped = false;
		let poll: ReturnType<typeof setTimeout> | undefined;
		const tick = async () => {
			if (stopped) return;
			const keepGoing = await fetchAll();
			if (stopped) return;
			if (!keepGoing) {
				stopped = true;
				return;
			}
			// fetchAll reports success by opening the connection. A run of
			// failures means the endpoint we raced onto is gone — the usual cause
			// is leaving the Wi-Fi network the LAN address belonged to — so race
			// the candidates again and pick up the tunnel.
			if (lastTickOkRef.current) {
				failStreak.current = 0;
			} else {
				failStreak.current += 1;
				const now = Date.now();
				if (
					shouldReRace({
						consecutiveFailures: failStreak.current,
						lastReRaceAt: lastReRaceAt.current,
						now,
						unreachable: lastFailUnreachableRef.current,
					})
				) {
					lastReRaceAt.current = now;
					failStreak.current = 0;
					void reloadConfig();
				}
			}
			// Pace from completion, not start time: slow hosts must not accumulate
			// overlapping full-history loads while the user scrolls or switches tabs.
			if (!stopped) poll = setTimeout(() => void tick(), pollIntervalFor(config));
		};
		void tick();
		// Paced by which endpoint won: the event stream cannot deliver over the
		// tunnel, so the poll is the only live signal there and has to be quick.
		// The effect re-runs whenever the config changes, so switching paths
		// re-paces this without anything extra.
		return () => {
			clearTimeout(poll);
			// Stop the timer and its in-flight requests together. Every request
			// carries the install-id header; it must not keep the phone live after
			// backgrounding or keep publishing after ownership changes.
			stopped = true;
			if (primaryLoadRef.current?.config === config) primaryLoadRef.current.controller.abort();
		};
	}, [config, fetchAll, appActive, reloadConfig, configResolved]);

	return { fetchAll };
}
