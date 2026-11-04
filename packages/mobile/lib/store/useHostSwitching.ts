import { useCallback, useEffect } from "react";

// Aliased: this file already declares its own `AppState` type for the
// provider's context value, so the React Native app-lifecycle API imports
// under a different name to avoid colliding with it.
import { AppState as RNAppState } from "react-native";
import { shouldPoll } from "../appStatePoll";
import { type DashboardSession, type DashboardStats, type OrchestratorLink } from "../api";
import { isConfigured, machineIdentity, type ServerConfig } from "../config";
import { type ConnectOptions } from "../connectRuntime";
import type { Endpoint } from "../endpoints";
import { setActiveHost } from "../hosts";
import { type KnownProjects } from "../projectFilter";
import { commitHostSwitch, promotableHostConfig } from "../hostRoute";
import { type HostSnapshot } from "../otherHosts";
import { type ArchiveSummary } from "../archive/archiveSummary";
import { ConnStatus } from "../store";
import { VerifiedHostSnapshot } from "../otherHosts";

export function useHostSwitching({
	pollActiveRef,
	primaryLoadRef,
	configResolution,
	promotedConfigRef,
	cfgRef,
	heldHostIdRef,
	setHeldMachine,
	setConfig,
	setConfigResolved,
	setConnection,
	resumedRef,
	reloadConfig,
	setAppActive,
	otherHosts,
	primarySnapshotRef,
	setSelectedHostId,
	setSelectedHostName,
	setActiveEndpoints,
	setKnownProjects,
	setSessions,
	setArchive,
	setOrchestrators,
	setSessionMachine,
	setOrchestratorId,
	setStats,
	setNotificationsUnread,
	lastSyncAtRef,
	setLoading,
	setError,
	setErrorStatus,
	setErrorCode,
	releaseHeld,
}: {
	pollActiveRef: React.RefObject<boolean>;
	primaryLoadRef: React.RefObject<{ config: ServerConfig; controller: AbortController; promise: Promise<boolean>; queued?: Promise<boolean> | undefined; } | null>;
	configResolution: React.RefObject<number>;
	promotedConfigRef: React.RefObject<ServerConfig | null>;
	cfgRef: React.RefObject<ServerConfig | null>;
	heldHostIdRef: React.RefObject<string | null>;
	setHeldMachine: React.Dispatch<React.SetStateAction<string>>;
	setConfig: React.Dispatch<React.SetStateAction<ServerConfig | null>>;
	setConfigResolved: React.Dispatch<React.SetStateAction<boolean>>;
	setConnection: React.Dispatch<React.SetStateAction<ConnStatus>>;
	resumedRef: React.RefObject<boolean>;
	reloadConfig: (options?: ConnectOptions | undefined) => Promise<ServerConfig | null>;
	setAppActive: React.Dispatch<React.SetStateAction<boolean>>;
	otherHosts: { snapshots: Record<string, HostSnapshot>; refreshHost: (hostId: string) => Promise<void>; configForHost: (hostId: string) => ServerConfig | null; claimPrimary: (hostId: string) => VerifiedHostSnapshot | null; adoptPrimary: (snapshot: HostSnapshot) => boolean; };
	primarySnapshotRef: React.RefObject<HostSnapshot | null>;
	setSelectedHostId: React.Dispatch<React.SetStateAction<string | null>>;
	setSelectedHostName: React.Dispatch<React.SetStateAction<string | null>>;
	setActiveEndpoints: React.Dispatch<React.SetStateAction<Endpoint[]>>;
	setKnownProjects: React.Dispatch<React.SetStateAction<KnownProjects>>;
	setSessions: React.Dispatch<React.SetStateAction<DashboardSession[]>>;
	setArchive: React.Dispatch<React.SetStateAction<ArchiveSummary | null>>;
	setOrchestrators: React.Dispatch<React.SetStateAction<OrchestratorLink[]>>;
	setSessionMachine: React.Dispatch<React.SetStateAction<string>>;
	setOrchestratorId: React.Dispatch<React.SetStateAction<string | null>>;
	setStats: React.Dispatch<React.SetStateAction<DashboardStats>>;
	setNotificationsUnread: React.Dispatch<React.SetStateAction<number>>;
	lastSyncAtRef: React.RefObject<number>;
	setLoading: React.Dispatch<React.SetStateAction<boolean>>;
	setError: React.Dispatch<React.SetStateAction<string | null>>;
	setErrorStatus: React.Dispatch<React.SetStateAction<number | null>>;
	setErrorCode: React.Dispatch<React.SetStateAction<string | null>>;
	releaseHeld: () => void;
}) {

	useEffect(() => {
		const sub = RNAppState.addEventListener("change", (state) => {
			const active = shouldPoll(state);
			if (active === pollActiveRef.current) return;
			pollActiveRef.current = active;
			if (!active) {
				primaryLoadRef.current?.controller.abort();
				// A LAN address can point at a different machine when the phone wakes.
				// Keep display rows, but never reuse its old bearer connection.
				configResolution.current++;
				promotedConfigRef.current = null;
				if (cfgRef.current && isConfigured(cfgRef.current)) {
					heldHostIdRef.current = cfgRef.current.hostId ?? "";
					setHeldMachine(machineIdentity(cfgRef.current));
				}
				cfgRef.current = null;
				setConfig(null);
				setConfigResolved(false);
				setConnection("closed");
			} else {
				resumedRef.current = true;
				void reloadConfig();
			}
			setAppActive(active);
		});
		return () => sub.remove();
	}, [reloadConfig]);

	const switchHost = useCallback(async (id: string) => {
		const snapshot = otherHosts.snapshots[id];
		const promotedConfig = promotableHostConfig(id, snapshot);
		if (snapshot && promotedConfig) {
			// This host already passed the identity probe and its REST poll is open.
			// Persist first so a failed storage write leaves the current primary
			// untouched; then transfer the verified state atomically instead of
			// presenting a healthy machine as offline while racing it again.
			const incoming = await commitHostSwitch(id, {
				persist: setActiveHost,
				claim: otherHosts.claimPrimary,
			});
			if (!incoming) {
				await reloadConfig();
				return;
			}
			configResolution.current++;
			primaryLoadRef.current?.controller.abort();
			cfgRef.current = incoming.config;
			const outgoing = primarySnapshotRef.current;
			if (outgoing) otherHosts.adoptPrimary(outgoing);
			promotedConfigRef.current = incoming.config;
			setSelectedHostId(id);
			setSelectedHostName(incoming.name);
			setActiveEndpoints(incoming.endpoints);
			setConfig(incoming.config);
			setConfigResolved(true);
			setKnownProjects({
				machine: machineIdentity(incoming.config),
				projects: incoming.projectsKnown ? incoming.projects : [],
				known: incoming.projectsKnown,
			});
			setSessions(incoming.sessions);
			setArchive(incoming.archive ?? null);
			setOrchestrators(incoming.orchestrators);
			setSessionMachine(machineIdentity(incoming.config));
			setOrchestratorId(incoming.orchestratorId);
			setStats(incoming.stats);
			setNotificationsUnread(incoming.notificationsUnread);
			lastSyncAtRef.current = incoming.lastSyncAt;
			setConnection("open");
			setLoading(false);
			setError(null);
			setErrorStatus(null);
				setErrorCode(null);
			return;
		}

		// Stop A's in-flight polls before the persisted selection changes. A
		// response that lands after this point must not repopulate B's screen.
		configResolution.current++;
		primaryLoadRef.current?.controller.abort();
		promotedConfigRef.current = null;
		cfgRef.current = null;
		releaseHeld();
		setConfig(null);
		setConfigResolved(false);
		try {
			await setActiveHost(id);
		} catch (error) {
			await reloadConfig();
			throw error;
		}
		await reloadConfig();
	}, [otherHosts.adoptPrimary, otherHosts.claimPrimary, otherHosts.snapshots, reloadConfig, releaseHeld]);

	return { switchHost };
}
