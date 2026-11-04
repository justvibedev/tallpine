import { useCallback, useEffect, useRef } from "react";
import { agentAccountProviderFor, followsSavedAccount } from "../../../../hooks/useAgentAccounts";
import {
	cacheAgentReadiness,
	ensureAgentReadiness,
	type AgentReadiness,
} from "../../../../hooks/useAgentReadinessQuery";
import {
	isActiveInstallJob,
	isFailedInstallJob,
	upsertInstallJob,
	type InstallJob,
} from "../../../../hooks/useHarnessUpdates";
import { type AgentId } from "../../../../lib/agent-options";
import { LOCAL_HOST } from "../../../../lib/hosts";
import { FOCUS_HIGHLIGHT_MS, useWideLayout } from "../harness-ui";
import type { LocalHarnessContentProps } from "./local-harness-types";
import { useLocalHarnessState } from "./useLocalHarnessState";

/** Auth probing, focus handling and auto-login effects for the local harness settings content. */
export function useLocalHarnessEffects(props: LocalHarnessContentProps, state: ReturnType<typeof useLocalHarnessState>) {
	const {
		hostId,
		onDetailChange,
		startLogin = false,
		showDiagnostics,
	} = props;
	const {
		activeInstallJobs,
		agentAuthPlans,
		agents,
		authPlans,
		authPlansKey,
		authWorkflowRef,
		client,
		detailRef,
		expandedDiagnostics,
		expandedFailures,
		focusDiagnosticsRef,
		focusHandledRef,
		focusPrimaryActionRef,
		highlightTimerRef,
		installed,
		installerKey,
		installers,
		jobMap,
		jobs,
		jobsKey,
		openDetail,
		operationAttempts,
		pendingActions,
		queryClient,
		readinessKey,
		refreshInstalledAgent,
		revealAgent,
		rows,
		selectedAgentId,
		selectedAgentRef,
		setActionErrors,
		setExpandedDiagnostics,
		setHighlightedAgentId,
		setLastUpdatedAt,
		setNoticeError,
		setPendingAgentIds,
		targetAgentId,
		describe,
	} = state;
	useEffect(() => {
		let active = true;
		let retryTimer: ReturnType<typeof setTimeout> | undefined;
		const invalidateHarnessQueries = () => Promise.all([
			queryClient.invalidateQueries({ queryKey: readinessKey }),
			queryClient.invalidateQueries({ queryKey: installerKey }),
			queryClient.invalidateQueries({ queryKey: jobsKey }),
			queryClient.invalidateQueries({ queryKey: authPlansKey }),
		]);
		// Page-open refresh stays silent, but a failed refresh must not leave
		// stale or unknown readiness in place: fall back to ensure, and re-fetch
		// the readiness snapshot if that fails too.
		const recoverReadiness = async () => {
			try {
				const readiness = await ensureAgentReadiness([], "display", hostId);
				if (active) cacheAgentReadiness(queryClient, readiness, hostId);
				return true;
			} catch {
				if (active) await queryClient.invalidateQueries({ queryKey: readinessKey });
				return false;
			}
		};
		const refresh = async () => {
			let complete = false;
			try {
				const { error } = await client.POST("/api/v1/agents/refresh");
				if (!active) return;
				complete = error ? await recoverReadiness() : true;
				if (complete) await invalidateHarnessQueries();
			} catch {
				if (active) complete = await recoverReadiness();
			}
			if (!active) return;
			if (complete) {
				// Revalidate after readiness without clearing the previous result.
				void queryClient.invalidateQueries({ queryKey: ["agent-update-advisory", hostId ?? LOCAL_HOST] });
			} else retryTimer = setTimeout(() => void refresh(), 30_000);
		};
		void refresh();
		return () => { active = false; clearTimeout(retryTimer); };
	}, [authPlansKey, client, hostId, installerKey, jobsKey, queryClient, readinessKey]);
	useEffect(() => {
		if (focusHandledRef.current || !targetAgentId) return;
		if (agents.isPending || installers.isPending || jobs.isPending || authPlans.isPending) return;
		focusHandledRef.current = true;
		// A failed update notice lands here: open that attempt's job diagnostics
		// ready to copy. A failure with no job of its own, or one whose job has
		// since been replaced, opens the Health report with the error it showed.
		const targetJob = jobMap.get(targetAgentId);
		if (showDiagnostics?.jobStartedAt && targetJob?.startedAt === showDiagnostics.jobStartedAt && describe(targetAgentId).hasDiagnostics) {
			focusDiagnosticsRef.current = true;
			setExpandedDiagnostics((current) => ({ ...current, [targetAgentId]: true }));
			openDetail(targetAgentId);
		} else {
			focusPrimaryActionRef.current = true;
			if (showDiagnostics?.error) setNoticeError({ agentId: targetAgentId, error: showDiagnostics.error, jobStartedAt: targetJob?.startedAt ?? undefined });
			openDetail(targetAgentId, showDiagnostics ? "health" : "account");
		}
		setHighlightedAgentId(targetAgentId);
		highlightTimerRef.current = window.setTimeout(() => setHighlightedAgentId(null), FOCUS_HIGHLIGHT_MS);
	}, [agents.isPending, authPlans.isPending, installers.isPending, jobs.isPending, showDiagnostics, targetAgentId]);

	useEffect(() => {
		if (!focusPrimaryActionRef.current || !selectedAgentId) return;
		const detail = detailRef.current;
		if (!detail) return;
		focusPrimaryActionRef.current = false;
		// No scrollIntoView: the open page always fills the dialog, whose body does not
		// scroll, and scrolling it anyway shifts the page up with no way back.
		const primaryAction = detail.querySelector<HTMLElement>("[data-harness-primary-action]:not(:disabled)");
		(primaryAction ?? detail.querySelector<HTMLElement>("[role=tab][data-state=active]"))?.focus({ preventScroll: true });
	}, [selectedAgentId]);

	// Keyed on the expansion too: a wide layout may already show this harness,
	// so opening it again does not change the selection.
	useEffect(() => {
		if (!focusDiagnosticsRef.current) return;
		const diagnostics = detailRef.current?.querySelector<HTMLElement>("[data-harness-diagnostics]");
		if (!diagnostics) return;
		focusDiagnosticsRef.current = false;
		diagnostics.querySelector<HTMLElement>("[data-harness-copy-diagnostics]")?.focus({ preventScroll: true });
		// Scroll only the tab's own scroller, for the same reason as above.
		const scroller = diagnostics.closest<HTMLElement>("[role=tabpanel]");
		if (!scroller) return;
		const reveal = () => { scroller.scrollTop += diagnostics.getBoundingClientRect().top - scroller.getBoundingClientRect().top; };
		reveal();
		// The page can still switch to the side-by-side layout after this runs,
		// which resizes the scroller and resets its position; keep revealing briefly.
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(reveal);
		observer.observe(scroller);
		const stop = window.setTimeout(() => observer.disconnect(), 1_000);
		return () => { window.clearTimeout(stop); observer.disconnect(); };
	}, [expandedDiagnostics, selectedAgentId]);

	// Wide layouts keep the list beside the open harness; narrow ones swap between them.
	const layoutRef = useRef<HTMLDivElement>(null);
	const split = useWideLayout(layoutRef);
	useEffect(() => {
		if (!split || selectedAgentRef.current !== null || rows.length === 0) return;
		openDetail(rows[0]);
	});
	useEffect(() => {
		onDetailChange?.(selectedAgentId !== null && !split);
	}, [onDetailChange, selectedAgentId, split]);
	useEffect(() => () => onDetailChange?.(false), [onDetailChange]);

	useEffect(() => () => {
		if (highlightTimerRef.current !== null) window.clearTimeout(highlightTimerRef.current);
	}, []);

	const cliLoginOnOwnRow = useCallback((agentId: AgentId) => !hostId && agentAccountProviderFor(agentId) !== null
		&& followsSavedAccount(queryClient.getQueryData<AgentReadiness>(readinessKey)?.agents.find((agent) => agent.id === agentId)), [hostId, queryClient, readinessKey]);

	// A "Log in" shortcut elsewhere (the task composer's model error) lands here
	// and starts the same login flow the row's own button would, exactly once.
	const startAuthRef = useRef<(agentId: AgentId) => Promise<boolean>>(async () => false);
	const autoLoginHandledRef = useRef(false);
	useEffect(() => {
		if (!startLogin || autoLoginHandledRef.current || !targetAgentId) return;
		if (agents.isPending || authPlans.isPending) return;
		autoLoginHandledRef.current = true;
		const plan = agentAuthPlans.get(targetAgentId);
		if (!plan || !plan.available || plan.action === "instructions") return;
		// New tasks start on a saved account: the CLI's own login would not fix
		// them, so the page opens on the accounts list instead.
		if (cliLoginOnOwnRow(targetAgentId)) return;
		void startAuthRef.current(targetAgentId);
	}, [agentAuthPlans, agents.isPending, authPlans.isPending, cliLoginOnOwnRow, startLogin, targetAgentId]);

	useEffect(() => {
		for (const job of jobs.data ?? []) {
			const agentId = job.target as AgentId;
			if (isActiveInstallJob(job)) {
				activeInstallJobs.current.add(agentId);
				continue;
			}
			const completedWhileMounted = activeInstallJobs.current.delete(agentId);
			if (job.status !== "succeeded" || !completedWhileMounted) continue;
			refreshInstalledAgent(agentId);
		}
	}, [jobs.data, refreshInstalledAgent]);

	useEffect(() => {
		for (const job of jobs.data ?? []) {
			const agentId = job.target as AgentId;
			const attempt = operationAttempts[agentId];
			if (!attempt?.startedAt || attempt.startedAt !== job.startedAt) continue;
			if (isActiveInstallJob(job)) expandedFailures.current.delete(agentId);
			if (isFailedInstallJob(job) && expandedFailures.current.get(agentId) !== attempt.startedAt) {
				expandedFailures.current.set(agentId, attempt.startedAt);
				if (!authWorkflowRef.current || authWorkflowRef.current.agentId === agentId) revealAgent(agentId);
			}
			if (job.status !== "succeeded") continue;
			if (attempt.operation === "update") {
				if (job.finishedAt && Number.isFinite(Date.parse(job.finishedAt))) {
					setLastUpdatedAt((current) => current[agentId] === job.finishedAt ? current : { ...current, [agentId]: job.finishedAt! });
				}
			} else {
				setLastUpdatedAt((current) => current[agentId] === undefined ? current : { ...current, [agentId]: undefined });
			}
		}
	}, [jobs.data, operationAttempts]);

	// Collapse a failed install's diagnostics once the harness becomes installed.
	// A harness that was already installed keeps them open: its failure was an update.
	const observedInstalled = useRef<Set<AgentId> | null>(null);
	useEffect(() => {
		if (!agents.data) return;
		const previous = observedInstalled.current;
		observedInstalled.current = installed;
		if (!previous) return;
		setExpandedDiagnostics((current) => {
			let changed = false;
			const next = { ...current };
			for (const agentId of installed) {
				if (!previous.has(agentId) && next[agentId]) {
					delete next[agentId];
					changed = true;
				}
			}
			return changed ? next : current;
		});
	}, [agents.data, installed]);

	const updateJob = (job: InstallJob) => {
		setActionErrors((current) => ({ ...current, [job.target as AgentId]: undefined }));
		queryClient.setQueryData<InstallJob[]>(jobsKey, (current) => upsertInstallJob(current, job));
	};

	const beginAction = (agentId: AgentId): boolean => {
		if (pendingActions.current.has(agentId)) return false;
		pendingActions.current.add(agentId);
		setPendingAgentIds(new Set(pendingActions.current));
		setNoticeError((current) => current?.agentId === agentId ? null : current);
		return true;
	};

	const endAction = (agentId: AgentId) => {
		pendingActions.current.delete(agentId);
		setPendingAgentIds(new Set(pendingActions.current));
	};

	return {
		beginAction,
		cliLoginOnOwnRow,
		endAction,
		layoutRef,
		split,
		startAuthRef,
		updateJob,
	};
}
