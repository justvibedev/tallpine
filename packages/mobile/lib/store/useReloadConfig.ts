import { useCallback, useRef } from "react";
import { type DashboardSession, type DashboardStats, type OrchestratorLink } from "../api";
import { isConfigured, type ServerConfig } from "../config";
import { resolveActiveConfig, runtimeResolveDeps } from "../resolveConfig";
import { type ConnectOptions } from "../connectRuntime";
import type { Endpoint } from "../endpoints";
import { activeHost, loadHosts, sameHostConnections, type Host } from "../hosts";
import { isPairingRevoked, PAIRING_REMOVED_MESSAGE } from "../revokedPairing";
import { sameServerConfig } from "../sameConfig";
import { shouldClearAuthError } from "../staleAuthError";
import { IncompatibleHostVersionError } from "../race";
import { InsecureTransportError } from "../secureTransport";
import { type ArchiveSummary } from "../archive/archiveSummary";
import { ConnStatus } from "../store";

export function useReloadConfig({
	mountedRef,
	configResolution,
	setSelectedHostName,
	setSelectedHostId,
	cfgRef,
	heldHostIdRef,
	releaseHeld,
	primaryLoadRef,
	setConfig,
	setConfigResolved,
	setSessions,
	setArchive,
	setOrchestrators,
	setSessionMachine,
	setOrchestratorId,
	setStats,
	setNotificationsUnread,
	lastSyncAtRef,
	setPairedHosts,
	setError,
	setErrorStatus,
	setErrorCode,
	setConnection,
	errorStatusRef,
	setActiveEndpoints,
}: {
	mountedRef: React.RefObject<boolean>;
	configResolution: React.RefObject<number>;
	setSelectedHostName: React.Dispatch<React.SetStateAction<string | null>>;
	setSelectedHostId: React.Dispatch<React.SetStateAction<string | null>>;
	cfgRef: React.RefObject<ServerConfig | null>;
	heldHostIdRef: React.RefObject<string | null>;
	releaseHeld: () => void;
	primaryLoadRef: React.RefObject<{ config: ServerConfig; controller: AbortController; promise: Promise<boolean>; queued?: Promise<boolean> | undefined; } | null>;
	setConfig: React.Dispatch<React.SetStateAction<ServerConfig | null>>;
	setConfigResolved: React.Dispatch<React.SetStateAction<boolean>>;
	setSessions: React.Dispatch<React.SetStateAction<DashboardSession[]>>;
	setArchive: React.Dispatch<React.SetStateAction<ArchiveSummary | null>>;
	setOrchestrators: React.Dispatch<React.SetStateAction<OrchestratorLink[]>>;
	setSessionMachine: React.Dispatch<React.SetStateAction<string>>;
	setOrchestratorId: React.Dispatch<React.SetStateAction<string | null>>;
	setStats: React.Dispatch<React.SetStateAction<DashboardStats>>;
	setNotificationsUnread: React.Dispatch<React.SetStateAction<number>>;
	lastSyncAtRef: React.RefObject<number>;
	setPairedHosts: React.Dispatch<React.SetStateAction<Host[]>>;
	setError: React.Dispatch<React.SetStateAction<string | null>>;
	setErrorStatus: React.Dispatch<React.SetStateAction<number | null>>;
	setErrorCode: React.Dispatch<React.SetStateAction<string | null>>;
	setConnection: React.Dispatch<React.SetStateAction<ConnStatus>>;
	errorStatusRef: React.RefObject<number | null>;
	setActiveEndpoints: React.Dispatch<React.SetStateAction<Endpoint[]>>;
}) {

	// Tracks a run of failed polls so a dead endpoint can trigger another race.
	const failStreak = useRef(0);
	const lastReRaceAt = useRef(0);
	// Set when the app returns to the foreground, consumed by the upgrade check.
	const resumedRef = useRef(false);

	const reloadConfig = useCallback(async (options?: ConnectOptions): Promise<ServerConfig | null> => {
		if (!mountedRef.current) return null;
		const resolution = ++configResolution.current;
		// Races the active machine's endpoints rather than reading one stored
		// address, so the app lands on LAN at home and the tunnel from anywhere
		// else without the user choosing. Failed identity probes leave the
		// selected machine offline instead of reusing an unverified address.
		// Marked resolved whatever happens below. An unhandled failure here would
		// otherwise leave the loader up forever, which is a worse failure than
		// the blank screen this flag exists to prevent.
		try {
			const selected = await activeHost();
			if (resolution !== configResolution.current) return null;
			setSelectedHostName(selected?.name ?? null);
			setSelectedHostId(selected?.id ?? null);
			const currentHostId = cfgRef.current ? cfgRef.current.hostId : heldHostIdRef.current;
			if ((selected?.id ?? "") !== (currentHostId ?? "")) {
				releaseHeld();
				// Hide the previous machine's board and stop its in-flight polls
				// before waiting for the newly selected machine to answer.
				cfgRef.current = null;
				primaryLoadRef.current?.controller.abort();
				setConfig(null);
				setConfigResolved(false);
				setSessions([]);
				setArchive(null);
				setOrchestrators([]);
				setSessionMachine("");
				setOrchestratorId(null);
				setStats({});
				setNotificationsUnread(0);
				lastSyncAtRef.current = 0;
			}
			// Let every other paired machine connect while the selected one's
			// endpoint race is still waiting for an unreachable LAN or tunnel.
			const hosts = await loadHosts().catch(() => null);
			if (resolution !== configResolution.current) return null;
			if (hosts) setPairedHosts((previous) => sameHostConnections(previous, hosts) ? previous : hosts);
			const c = await resolveActiveConfig(runtimeResolveDeps(options));
			if (resolution !== configResolution.current) return null;
			if (!c && selected && (selected.pairingRemoved || isPairingRevoked(selected.token))) {
				// Resolution sent nothing: say why, so the board offers a scan.
				setError(PAIRING_REMOVED_MESSAGE);
				setErrorStatus(401);
				setErrorCode(null);
				setConnection("closed");
			}
		// Keep the previous object when the endpoint has not actually changed.
		// Resolution builds a fresh one every time, and the live conversation
		// stream, the poll loop and the terminal mux all key on this value's
		// identity — handing them a new object for the same endpoint tears them
		// down and rebuilds them, which showed up as chat replies arriving only
		// on the next poll instead of streaming in.
		// Stamped here so every race counts towards the cooldown, however it was
		// triggered — otherwise a failure race and an upgrade race can fire back
		// to back and thrash the connection.
			lastReRaceAt.current = Date.now();
			const prev = cfgRef.current;
			const next = sameServerConfig(prev, c) ? (prev as typeof c) : c;
			if (next !== prev) primaryLoadRef.current?.controller.abort();
			cfgRef.current = next;
			setConfig(next);
			// Connected again: the live config names the machine from here on.
			if (next && isConfigured(next)) releaseHeld();
			// A re-pair after a revocation must not keep showing the old 401
			// ("password rotated") until the first poll lands.
			if (shouldClearAuthError(!!next && isConfigured(next), next !== prev, errorStatusRef.current)) {
				setError(null);
				setErrorStatus(null);
				setErrorCode(null);
				setConnection("connecting");
			}
			// Read alongside the config so a failure can be explained: a stored
			// tunnel that no longer answers is a rotated hostname, not a machine
			// that is merely out of range.
			const active = await activeHost().catch(() => selected);
			if (resolution !== configResolution.current) return null;
			setActiveEndpoints(active?.endpoints ?? []);
			setSelectedHostId(active?.id ?? null);
			return next;
		} catch (cause) {
			if (!(cause instanceof IncompatibleHostVersionError) && !(cause instanceof InsecureTransportError)) throw cause;
			if (resolution !== configResolution.current) return null;
			cfgRef.current = null;
			releaseHeld();
			setConfig(null);
			setError(cause.message);
			setErrorStatus(426);
			// HTTPS_REQUIRED reads as "Re-pair for a secure connection"; no code as an incompatible version.
			setErrorCode(cause instanceof InsecureTransportError ? cause.code : null);
			setConnection("closed");
			return null;
		} finally {
			if (resolution === configResolution.current) {
				setConfigResolved(true);
				// An unreachable selected host must not hide other paired hosts.
				const hosts = await loadHosts().catch(() => null);
				if (resolution === configResolution.current && hosts) {
					setPairedHosts((previous) => sameHostConnections(previous, hosts) ? previous : hosts);
				}
			}
		}
	}, [releaseHeld]);

	return { failStreak, lastReRaceAt, resumedRef, reloadConfig };
}
