import { useCallback, useLayoutEffect, useMemo, useState } from "react";
import { type DashboardSession, type DashboardStats, type OrchestratorLink, type ProjectInfo } from "../api";
import { type ServerConfig } from "../config";
import type { Endpoint } from "../endpoints";
import { type Host } from "../hosts";
import { PAIRING_REMOVED_MESSAGE } from "../revokedPairing";
import { hostRouteMatches } from "../hostRoute";
import { emptyHostSnapshot, type HostSnapshot } from "../otherHosts";
import { type ArchiveSummary } from "../archive/archiveSummary";
import { createHostedRowsCache } from "../hostedRows";
import { machineAppearance } from "../machineAppearance";
import { ConnStatus } from "../store";
import { VerifiedHostSnapshot } from "../otherHosts";

export function useHostedState({
	pairedHosts,
	selectedHostId,
	otherHosts,
	activeEndpoints,
	config,
	connection,
	loading,
	error,
	errorStatus,
	errorCode,
	projects,
	projectsKnown,
	visibleSessions,
	visibleArchive,
	visibleOrchestrators,
	orchestratorId,
	stats,
	notificationsUnread,
	lastSyncAtRef,
	primarySnapshotRef,
	cfgRef,
	mountedRef,
	fetchAll,
}: {
	pairedHosts: Host[];
	selectedHostId: string | null;
	otherHosts: { snapshots: Record<string, HostSnapshot>; refreshHost: (hostId: string) => Promise<void>; configForHost: (hostId: string) => ServerConfig | null; claimPrimary: (hostId: string) => VerifiedHostSnapshot | null; adoptPrimary: (snapshot: HostSnapshot) => boolean; };
	activeEndpoints: Endpoint[];
	config: ServerConfig | null;
	connection: ConnStatus;
	loading: boolean;
	error: string | null;
	errorStatus: number | null;
	errorCode: string | null;
	projects: ProjectInfo[];
	projectsKnown: boolean;
	visibleSessions: DashboardSession[];
	visibleArchive: ArchiveSummary | null;
	visibleOrchestrators: OrchestratorLink[];
	orchestratorId: string | null;
	stats: DashboardStats;
	notificationsUnread: number;
	lastSyncAtRef: React.RefObject<number>;
	primarySnapshotRef: React.RefObject<HostSnapshot | null>;
	cfgRef: React.RefObject<ServerConfig | null>;
	mountedRef: React.RefObject<boolean>;
	fetchAll: (afterCurrent?: boolean) => Promise<boolean>;
}) {
	const hostStates = useMemo<HostSnapshot[]>(() => pairedHosts.map((host) => {
		if (host.id !== selectedHostId) return otherHosts.snapshots[host.id] ?? emptyHostSnapshot(host);
		if (host.pairingRemoved) {
			return { ...emptyHostSnapshot(host), connection: "closed", loading: false, error: PAIRING_REMOVED_MESSAGE, errorStatus: 401, errorCode: null };
		}
		return {
			hostId: host.id,
			name: host.name,
			appearance: machineAppearance(host),
			endpoints: activeEndpoints,
			config,
			connection,
			loading,
			error,
			errorStatus,
			errorCode,
			projects,
			projectsKnown,
			sessions: visibleSessions,
			archive: visibleArchive,
			orchestrators: visibleOrchestrators,
			orchestratorId,
			stats,
			notificationsUnread,
			lastSyncAt: lastSyncAtRef.current,
		};
	}), [pairedHosts, selectedHostId, otherHosts.snapshots, activeEndpoints, config, connection, loading, error, errorStatus, errorCode, projects, projectsKnown, visibleSessions, visibleArchive, visibleOrchestrators, orchestratorId, stats, notificationsUnread]);
	// Published after commit, never during render: an interrupted or replayed
	// render must not hand switchHost a snapshot that never reached the screen.
	// A layout effect runs before any user event can call switchHost.
	useLayoutEffect(() => {
		primarySnapshotRef.current = selectedHostId
			? hostStates.find((host) => host.hostId === selectedHostId) ?? null
			: null;
	}, [hostStates, selectedHostId]);
	const [sessionRows] = useState(() => createHostedRowsCache<DashboardSession>());
	const [projectRows] = useState(() => createHostedRowsCache<ProjectInfo>());
	const [orchestratorRows] = useState(() => createHostedRowsCache<OrchestratorLink>());
	const allSessions = useMemo(() => sessionRows(hostStates.map((host) => ({ ...host, rows: host.sessions }))), [hostStates, sessionRows]);
	const allProjects = useMemo(() => projectRows(hostStates.map((host) => ({ ...host, rows: host.projects }))), [hostStates, projectRows]);
	const allOrchestrators = useMemo(() => orchestratorRows(hostStates.map((host) => ({ ...host, rows: host.orchestrators }))), [hostStates, orchestratorRows]);
	const configForHost = useCallback((hostId: string): ServerConfig | null => {
		if (hostId === selectedHostId) return cfgRef.current;
		return otherHosts.configForHost(hostId);
	}, [selectedHostId, otherHosts.configForHost]);
	const requireConfig = useCallback((hostId?: string): ServerConfig => {
		const c = hostId === undefined ? cfgRef.current : configForHost(hostId);
		if (!c || (hostId !== undefined && !hostRouteMatches(hostId, c.hostId))) throw new Error("Host is not connected");
		return c;
	}, [configForHost]);
	const refreshHost = useCallback(async (hostId: string) => {
		if (!mountedRef.current) return;
		if (hostId === selectedHostId) await fetchAll(true);
		else await otherHosts.refreshHost(hostId);
	}, [selectedHostId, fetchAll, otherHosts.refreshHost]);
	const refreshAll = useCallback(async () => {
		await Promise.all(hostStates.map((host) => refreshHost(host.hostId)));
	}, [hostStates, refreshHost]);

	return { hostStates, allSessions, allProjects, allOrchestrators, configForHost, requireConfig, refreshHost, refreshAll };
}
