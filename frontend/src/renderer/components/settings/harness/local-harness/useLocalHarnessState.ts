import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../../../api/schema";
import { agentAuthPlansQueryKeyForHost, useAgentAuthPlans, useStartAgentAuth } from "../../../../hooks/useAgentAuth";
import { agentModelsQueryOptions, invalidateAgentModelCatalogs, refreshAgentModels } from "../../../../hooks/useAgentModelsQuery";
import {
	agentReadinessQueryKeyForHost,
	useAgentReadinessQuery,
	} from "../../../../hooks/useAgentReadinessQuery";
import {
	installedAgentIds,
	installerQueryKeyForHost,
	installersQueryOptions,
	installJobsQueryKeyForHost,
	installJobsQueryOptions,
	maintenanceMethodId,
	needsHarnessLogin,
	updateAdvisoryQueryKey,
	useHarnessUpdates,
	useStartHarnessUpdate,
	type AgentUpdateAdvisory,
	type HarnessInUse,
	} from "../../../../hooks/useHarnessUpdates";
import { shellTerminalsQueryKeyForHost } from "../../../../hooks/useShellTerminals";
import { agentLabel, AGENT_OPTIONS, type AgentId } from "../../../../lib/agent-options";
import { clientForSessionHost } from "../../../../lib/host-clients";
import { LOCAL_HOST } from "../../../../lib/hosts";
import { type AgentAuthState, type OperationAttempt } from "../describe-harness";
import { type AuthTerminalWorkflow } from "../HarnessAuthTerminalPanel";
import { type HarnessDetailTab } from "../harness-ui";
import { MULTI_PROVIDER_HARNESSES } from "../HarnessModelsPanel";
import type { LocalHarnessContentProps } from "./local-harness-types";
import { describeHarness, type HarnessDetails } from "../describe-harness";
import { healthText } from "../harness-diagnostics";
import { AgentAuthStates, AgentAuthProbeResult, AUTH_STATE_RANK, INSTALL_STATE_RANK } from "./local-harness-types";

/** Queries, keys and local state for the local harness settings content. */
export function useLocalHarnessState(props: LocalHarnessContentProps) {
	const {
		focusAgentId,
		hostId,
		search,
	} = props;
	const { i18n, t } = useTranslation();
	const queryClient = useQueryClient();
	const client = clientForSessionHost(hostId);
	const readinessKey = useMemo(() => agentReadinessQueryKeyForHost(hostId), [hostId]);
	const installerKey = useMemo(() => installerQueryKeyForHost(hostId), [hostId]);
	const jobsKey = useMemo(() => installJobsQueryKeyForHost(hostId), [hostId]);
	const authPlansKey = useMemo(() => agentAuthPlansQueryKeyForHost(hostId), [hostId]);
	const shellKey = useMemo(() => shellTerminalsQueryKeyForHost(hostId), [hostId]);
	const agents = useAgentReadinessQuery(true, hostId);
	const installers = useQuery(installersQueryOptions(hostId));
	const jobs = useQuery(installJobsQueryOptions(hostId));
	const startHarnessUpdate = useStartHarnessUpdate(hostId);
	const authPlans = useAgentAuthPlans(hostId);
	const startAgentAuth = useStartAgentAuth(hostId);
	const startAgentLogout = useStartAgentAuth(hostId, "logout");
	const [logoutRequest, setLogoutRequest] = useState<AgentId | null>(null);
	// An update that cannot run alongside open sessions lists the ones it waits for.
	const [inUseSessions, setInUseSessions] = useState<{ agentId: AgentId; inUse: HarnessInUse } | null>(null);
	const [authStates, setAuthStates] = useState<AgentAuthStates>({});
	const [checkingAuth, setCheckingAuth] = useState<AgentId[]>([]);
	const [authCheckErrors, setAuthCheckErrors] = useState<Partial<Record<AgentId, string>>>({});
	const [actionErrors, setActionErrors] = useState<Partial<Record<AgentId, string>>>({});
	const [selectedMethods, setSelectedMethods] = useState<Partial<Record<AgentId, string>>>({});
	const [expandedDiagnostics, setExpandedDiagnostics] = useState<Partial<Record<AgentId, boolean>>>({});
	const [copiedAgent, setCopiedAgent] = useState<AgentId | null>(null);
	const [authWorkflow, setAuthWorkflow] = useState<AuthTerminalWorkflow | null>(null);
	const authWorkflowRef = useRef<AuthTerminalWorkflow | null>(null);
	const authStartPendingRef = useRef(false);
	const mountedRef = useRef(true);
	authWorkflowRef.current = authWorkflow;
	const activeInstallJobs = useRef(new Set<AgentId>());
	const expandedFailures = useRef(new Map<AgentId, string>());
	const pendingActions = useRef(new Set<AgentId>());
	const authChecksInFlight = useRef(new Map<AgentId, Promise<AgentAuthProbeResult | undefined>>());
	const [pendingAgentIds, setPendingAgentIds] = useState<Set<AgentId>>(new Set());
	const detailRef = useRef<HTMLDivElement>(null);
	const focusHandledRef = useRef(false);
	const focusPrimaryActionRef = useRef(false);
	const focusDiagnosticsRef = useRef(false);
	// The failure a notice shortcut reported, kept for the Health report until
	// a later attempt supersedes it: one started here, or a new job in the shared query.
	const [noticeError, setNoticeError] = useState<{ agentId: AgentId; error: string; jobStartedAt?: string } | null>(null);
	const currentNoticeError = (agentId: AgentId) => noticeError?.agentId === agentId
		&& (jobMap.get(agentId)?.startedAt ?? undefined) === noticeError.jobStartedAt ? noticeError.error : undefined;
	const highlightTimerRef = useRef<number | null>(null);
	const [highlightedAgentId, setHighlightedAgentId] = useState<AgentId | null>(null);
	// The list opens one harness at a time; its page has Account, Models and Health tabs.
	const [selectedAgentId, setSelectedAgentId] = useState<AgentId | null>(null);
	const [detailTab, setDetailTab] = useState<HarnessDetailTab>("account");
	const selectedAgentRef = useRef<AgentId | null>(null);
	selectedAgentRef.current = selectedAgentId;
	// Opening a page refreshes its models once readiness says it is installed.
	const modelsRefreshPending = useRef<AgentId | null>(null);
	const openDetail = (agentId: AgentId, tab: HarnessDetailTab = "account") => {
		selectedAgentRef.current = agentId;
		setSelectedAgentId(agentId);
		setDetailTab(tab);
		modelsRefreshPending.current = agentId;
	};
	const closeDetail = () => {
		selectedAgentRef.current = null;
		setSelectedAgentId(null);
	};
	// Errors and login terminals for a harness live on its Account tab. Show
	// them unless the user is looking at a different harness.
	const revealAgent = (agentId: AgentId) => {
		if (selectedAgentRef.current !== null && selectedAgentRef.current !== agentId) return;
		openDetail(agentId);
	};
	// Operation identity is local to jobs started here; restored jobs use neutral copy.
	const [operationAttempts, setOperationAttempts] = useState<Partial<Record<AgentId, OperationAttempt>>>({});
	// The API has no durable update history. Only record verified updates we
	// actually observed here; a restored job/check timestamp is not an update date.
	const [lastUpdatedAt, setLastUpdatedAt] = useState<Partial<Record<AgentId, string>>>({});

	const plans = useMemo(() => new Map(installers.data?.map((plan) => [plan.agentId, plan]) ?? []), [installers.data]);
	const jobMap = useMemo(() => new Map(jobs.data?.map((job) => [job.target, job]) ?? []), [jobs.data]);
	const agentAuthPlans = useMemo(() => new Map(authPlans.data?.map((plan) => [plan.agentId, plan]) ?? []), [authPlans.data]);
	const readinessAgents = useMemo(() => new Map(agents.data?.agents.map((agent) => [agent.id, agent]) ?? []), [agents.data]);
	const installed = useMemo(() => installedAgentIds(agents.data), [agents.data]);
	const refreshModelsOnOpen = (agentId: AgentId) => {
		if (!installed.has(agentId) || (needsAuthentication(agentId) && !MULTI_PROVIDER_HARNESSES.has(agentId))) return;
		const options = agentModelsQueryOptions(agentId, "", hostId, true);
		// Read the persisted cache first, then refresh without replacing the
		// visible list with a loading state. Concurrent opens share one request.
		void queryClient.fetchQuery({
			queryKey: ["agent-model-revalidation", hostId ?? LOCAL_HOST, agentId, "settings-open"],
			staleTime: 0,
			retry: false,
			queryFn: async () => {
				await queryClient.prefetchQuery(options);
				const catalog = await refreshAgentModels(agentId, "", hostId, true);
				if (catalog) queryClient.setQueryData(options.queryKey, catalog);
				return true;
			},
		}).catch((error: unknown) => {
			queryClient.setQueryData<components["schemas"]["AgentModelsResponse"]>(options.queryKey, (cached) => cached ? {
				...cached,
				refreshError: error instanceof Error ? error.message : t("settings.harness.models.loadFailed"),
			} : cached);
		});
	};
	useEffect(() => {
		const agentId = modelsRefreshPending.current;
		if (!agentId || agentId !== selectedAgentId || !installed.has(agentId)) return;
		modelsRefreshPending.current = null;
		refreshModelsOnOpen(agentId);
	});
	// The page uses the initial installed snapshot immediately, like Install
	// and Login. A full-catalog background refresh must not delay its first
	// version lookup. The app-wide notification has its own readiness gate.
	const advisoryMap = useHarnessUpdates(installed, hostId);
	const operationMethodId = (agentId: AgentId): string => {
		const advisory = queryClient.getQueryData<AgentUpdateAdvisory>(updateAdvisoryQueryKey(agentId, hostId));
		return maintenanceMethodId(advisory);
	};
	const needsAuthentication = (agentId: AgentId): boolean => needsHarnessLogin(agentAuthPlans.get(agentId), readinessAgents.get(agentId)?.authentication.state);
	const normalizedSearch = search.trim().toLowerCase();
	const targetAgentId = AGENT_OPTIONS.find((agentId) => agentId === focusAgentId) ?? null;
	const rows = AGENT_OPTIONS
		.filter((agentId) => agentId === targetAgentId || agentId === authWorkflow?.agentId || agentLabel(agentId).toLowerCase().includes(normalizedSearch))
		.sort((left, right) => {
			const leftAgent = readinessAgents.get(left);
			const rightAgent = readinessAgents.get(right);
			const authOrder = AUTH_STATE_RANK[leftAgent?.authentication.state ?? "unknown"]
				- AUTH_STATE_RANK[rightAgent?.authentication.state ?? "unknown"];
			if (authOrder !== 0) return authOrder;
			return INSTALL_STATE_RANK[leftAgent?.installation.state ?? "unknown"]
				- INSTALL_STATE_RANK[rightAgent?.installation.state ?? "unknown"];
		});
	const updateAuthState = useCallback((agentId: AgentId, patch: Partial<AgentAuthState>) => {
		setAuthStates((current) => ({
			...current,
			[agentId]: { pending: false, error: null, ...current[agentId], ...patch },
		}));
	}, []);
	const checkAuthRef = useRef<(agentId: AgentId) => Promise<unknown>>(async () => undefined);
	const refreshInstalledAgent = useCallback((agentId: AgentId) => {
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		void checkAuthRef.current(agentId).finally(async () => {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: updateAdvisoryQueryKey(agentId, hostId) }),
				queryClient.invalidateQueries({ queryKey: installerKey }),
				queryClient.invalidateQueries({ queryKey: authPlansKey }),
				invalidateAgentModelCatalogs(queryClient, agentId, hostId),
			]);
		});
	}, [authPlansKey, hostId, installerKey, queryClient]);

	const describe = (agentId: AgentId) => describeHarness({
		agentId,
		t,
		language: i18n.resolvedLanguage ?? "en",
		plan: plans.get(agentId),
		job: jobMap.get(agentId),
		installed: installed.has(agentId),
		selectedMethodId: selectedMethods[agentId],
		advisoryQuery: advisoryMap.get(agentId),
		pending: pendingAgentIds.has(agentId),
		actionError: actionErrors[agentId],
		attempt: operationAttempts[agentId],
		readinessAgent: readinessAgents.get(agentId),
		readinessPending: agents.isPending,
		readinessFailed: Boolean(agents.error),
		authPlan: agentAuthPlans.get(agentId),
		authPlansPending: authPlans.isPending,
		authPlansFailed: authPlans.isError,
		authState: authStates[agentId],
		authWorkflow: authWorkflow?.agentId === agentId ? authWorkflow : null,
		updatedAt: lastUpdatedAt[agentId],
	});
	const healthReport = (details: HarnessDetails) => healthText(details, { hostId, authCheckError: authCheckErrors[details.agentId], t });

	return {
		describe,
		healthReport,
		actionErrors,
		activeInstallJobs,
		advisoryMap,
		agentAuthPlans,
		agents,
		authCheckErrors,
		authChecksInFlight,
		authPlans,
		authPlansKey,
		authStartPendingRef,
		authStates,
		authWorkflow,
		authWorkflowRef,
		checkAuthRef,
		checkingAuth,
		client,
		closeDetail,
		copiedAgent,
		currentNoticeError,
		detailRef,
		detailTab,
		expandedDiagnostics,
		expandedFailures,
		focusDiagnosticsRef,
		focusHandledRef,
		focusPrimaryActionRef,
		highlightTimerRef,
		highlightedAgentId,
		i18n,
		inUseSessions,
		installed,
		installerKey,
		installers,
		jobMap,
		jobs,
		jobsKey,
		lastUpdatedAt,
		logoutRequest,
		mountedRef,
		needsAuthentication,
		openDetail,
		operationAttempts,
		operationMethodId,
		pendingActions,
		pendingAgentIds,
		plans,
		queryClient,
		readinessAgents,
		readinessKey,
		refreshInstalledAgent,
		revealAgent,
		rows,
		selectedAgentId,
		selectedAgentRef,
		selectedMethods,
		setActionErrors,
		setAuthCheckErrors,
		setAuthWorkflow,
		setCheckingAuth,
		setCopiedAgent,
		setDetailTab,
		setExpandedDiagnostics,
		setHighlightedAgentId,
		setInUseSessions,
		setLastUpdatedAt,
		setLogoutRequest,
		setNoticeError,
		setOperationAttempts,
		setPendingAgentIds,
		setSelectedMethods,
		shellKey,
		startAgentAuth,
		startAgentLogout,
		startHarnessUpdate,
		t,
		targetAgentId,
		updateAuthState,
	};
}
