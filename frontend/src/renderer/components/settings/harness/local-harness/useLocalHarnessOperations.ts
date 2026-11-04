import { useCallback } from "react";
import { probeAgentAuth } from "../../../../hooks/useAgentAuth";
import { invalidateAgentModelCatalogs } from "../../../../hooks/useAgentModelsQuery";
import {
	cacheAgentReadiness,
	ensureAgentReadiness,
	} from "../../../../hooks/useAgentReadinessQuery";
import {
	advisoryInUse,
	isActiveInstallJob,
	updateAdvisoryQueryKey,
	type AgentUpdateAdvisory,
	type InstallJob,
} from "../../../../hooks/useHarnessUpdates";
import { type ShellTerminal } from "../../../../hooks/useShellTerminals";
import { AgentCheckError } from "../../../../lib/agent-check-error";
import { agentLabel, type AgentId } from "../../../../lib/agent-options";
import { apiErrorMessage } from "../../../../lib/api-client";
import { tallpineBridge } from "../../../../lib/bridge";
import { supportsUpdate, type AgentOperation } from "../describe-harness";
import { closeAuthTerminal, type AuthTerminalWorkflow } from "../HarnessAuthTerminalPanel";
import { diagnosticsText, machineText } from "../harness-diagnostics";
import type { LocalHarnessContentProps } from "./local-harness-types";
import { useLocalHarnessState } from "./useLocalHarnessState";
import { useLocalHarnessEffects } from "./useLocalHarnessEffects";
import { AgentAuthProbeResult } from "./local-harness-types";

/** Install, update and diagnostics operations for the local harness settings content. */
export function useLocalHarnessOperations(props: LocalHarnessContentProps, state: ReturnType<typeof useLocalHarnessState>, effects: ReturnType<typeof useLocalHarnessEffects>) {
	const {
		hostId,
	} = props;
	const {
		agentAuthPlans,
		authChecksInFlight,
		authPlans,
		authStartPendingRef,
		authWorkflowRef,
		checkAuthRef,
		client,
		installed,
		jobMap,
		jobsKey,
		mountedRef,
		needsAuthentication,
		operationMethodId,
		pendingActions,
		plans,
		queryClient,
		refreshInstalledAgent,
		revealAgent,
		setActionErrors,
		setAuthCheckErrors,
		setAuthWorkflow,
		setCheckingAuth,
		setCopiedAgent,
		setInUseSessions,
		setOperationAttempts,
		shellKey,
		startAgentAuth,
		startAgentLogout,
		startHarnessUpdate,
		t,
		updateAuthState,
		describe,
		healthReport,
	} = state;
	const {
		beginAction,
		endAction,
		startAuthRef,
		updateJob,
	} = effects;
	const startAgentOperation = async (agentId: AgentId, method: string, operation: AgentOperation): Promise<boolean> => {
		if (isActiveInstallJob(queryClient.getQueryData<InstallJob[]>(jobsKey)?.find((job) => job.target === agentId))
			|| authWorkflowRef.current?.agentId === agentId || authStartPendingRef.current) return false;
		if (operation === "update" && (!installed.has(agentId)
			|| operationMethodId(agentId) !== method
			|| !supportsUpdate(plans.get(agentId)?.methods.find((candidate) => candidate.id === method)))) return false;
		if (operation === "update" && (needsAuthentication(agentId) || authPlans.isPending || authPlans.isError)) return false;
		if (!beginAction(agentId)) return false;
		setOperationAttempts((current) => ({ ...current, [agentId]: { operation } }));
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		const fail = (message: string) => {
			setActionErrors((current) => ({ ...current, [agentId]: message }));
			revealAgent(agentId);
			return false;
		};
		try {
			let data: InstallJob;
			if (operation === "update") {
				const result = await startHarnessUpdate({
					agentId,
					methodId: method,
					canStart: () => mountedRef.current,
				});
				// Sessions are not a failure to retry: show what to end.
				if (result.inUse) {
					setInUseSessions({ agentId, inUse: result.inUse });
					return false;
				}
				if (result.error) return fail(result.error);
				if (!result.job) return false;
				data = result.job;
			} else {
				const response = await client.POST("/api/v1/agents/{agent}/install", {
					params: { path: { agent: agentId } },
					body: { method, operation },
				});
				if (response.error || !response.data) return fail(apiErrorMessage(response.error, t("settings.harness.startFailed")));
				data = response.data;
			}
			setOperationAttempts((current) => ({ ...current, [agentId]: { operation, startedAt: data.startedAt } }));
			updateJob(data);
			if (data.status === "succeeded") refreshInstalledAgent(agentId);
			return true;
		} catch (error) {
			return fail(error instanceof Error ? error.message : t("settings.harness.startFailed"));
		} finally {
			endAction(agentId);
		}
	};

	const requestUpdate = (agentId: AgentId) => {
		if (!installed.has(agentId) || pendingActions.current.has(agentId) || isActiveInstallJob(jobMap.get(agentId))
			|| authWorkflowRef.current?.agentId === agentId || authStartPendingRef.current) return;
		const methods = plans.get(agentId)?.methods ?? [];
		const method = operationMethodId(agentId);
		if (!method || !supportsUpdate(methods.find((candidate) => candidate.id === method))) return;
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		const inUse = advisoryInUse(queryClient.getQueryData<AgentUpdateAdvisory>(updateAdvisoryQueryKey(agentId, hostId)));
		if (inUse) { setInUseSessions({ agentId, inUse }); return; }
		void startAgentOperation(agentId, method, "update");
	};

	const verifyInstall = async (agentId: AgentId) => {
		if (!beginAction(agentId)) return;
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		try {
			const { data, error } = await client.POST("/api/v1/agents/{agent}/verify", {
				params: { path: { agent: agentId } },
			});
			if (error || !data) {
				setActionErrors((current) => ({ ...current, [agentId]: apiErrorMessage(error, t("settings.harness.verifyFailed", { agent: agentLabel(agentId) })) }));
				return;
			}
			updateJob(data);
			if (data.status === "succeeded") refreshInstalledAgent(agentId);
		} finally {
			endAction(agentId);
		}
	};

	/** Diagnostics plus the machine they were taken on. */
	const copyDiagnostics = async (agentId: AgentId, job: InstallJob) => {
		const machine = await machineText(queryClient, hostId ? client : undefined);
		await copyText(agentId, [diagnosticsText(agentId, job), healthReport(describe(agentId)), machine].filter(Boolean).join("\n\n"));
	};

	const copyText = async (agentId: AgentId, text: string) => {
		await tallpineBridge.clipboard.writeText(text);
		setCopiedAgent(agentId);
		window.setTimeout(() => setCopiedAgent((current) => (current === agentId ? null : current)), 1_500);
	};

	const startAuth = async (agentId: AgentId, action: "login" | "logout" = "login"): Promise<boolean> => {
		if (authWorkflowRef.current || authStartPendingRef.current || pendingActions.current.has(agentId) || isActiveInstallJob(jobMap.get(agentId))) return false;
		if (action === "logout" && !agentAuthPlans.get(agentId)?.logoutCommand) return false;
		authStartPendingRef.current = true;
		updateAuthState(agentId, { pending: true, error: null });
		try {
			const plan = agentAuthPlans.get(agentId);
			if (action === "login" && plan?.launchMode === "documentation") {
				await tallpineBridge.app.openExternal(plan.documentationUrl);
				return true;
			}
			const result = await (action === "logout" ? startAgentLogout : startAgentAuth).mutateAsync(agentId);
			if (!mountedRef.current) {
				await closeAuthTerminal(result.terminal.handleId, hostId);
				queryClient.setQueryData<ShellTerminal[]>(shellKey, (current) => current?.filter((terminal) => terminal.handleId !== result.terminal.handleId));
				void queryClient.invalidateQueries({ queryKey: shellKey });
				return false;
			}
			const workflow: AuthTerminalWorkflow = {
				agentId,
				action: result.action,
				terminal: result.terminal,
				guidance: result.guidance ?? "",
				terminalInput: result.terminalInput,
				phase: "running",
				startedAt: Date.now(),
			};
			authWorkflowRef.current = workflow;
			setAuthWorkflow(workflow);
			revealAgent(agentId);
			void queryClient.invalidateQueries({ queryKey: shellKey });
			return true;
		} catch (error) {
			if (mountedRef.current) updateAuthState(agentId, { error: error instanceof Error ? error.message : t("settings.harness.authFailed") });
			return false;
		} finally {
			authStartPendingRef.current = false;
			if (mountedRef.current) updateAuthState(agentId, { pending: false });
		}
	};

	startAuthRef.current = startAuth;

	const checkAuth = useCallback(async (
		agentId: AgentId,
		{ fresh = false }: { fresh?: boolean } = {},
	): Promise<AgentAuthProbeResult | undefined> => {
		const existing = authChecksInFlight.current.get(agentId);
		if (existing && !fresh) return existing;
		setCheckingAuth((current) => current.includes(agentId) ? current : [...current, agentId]);
		const check = (async () => {
			if (existing) await existing;
			try {
				const result = await probeAgentAuth(agentId, hostId);
				// The probe also makes the daemon rediscover this agent's model
				// catalogs; drop the renderer's copies so a stale login error in
				// an open composer or picker clears without a manual refresh.
				void invalidateAgentModelCatalogs(queryClient, agentId, hostId);
				const readiness = await ensureAgentReadiness([agentId], "display", hostId);
				cacheAgentReadiness(queryClient, readiness, hostId);
				setAuthCheckErrors((current) => ({ ...current, [agentId]: undefined }));
				return result;
			} catch (error) {
				const reason = error instanceof AgentCheckError ? error.message : "Authentication verification request could not complete. Check the selected host connection and try again.";
				setAuthCheckErrors((current) => ({ ...current, [agentId]: `${new Date().toISOString()} · ${reason}` }));
				return undefined;
			}
		})();
		authChecksInFlight.current.set(agentId, check);
		const finishCheck = () => {
			if (authChecksInFlight.current.get(agentId) === check) {
				authChecksInFlight.current.delete(agentId);
				setCheckingAuth((current) => current.filter((id) => id !== agentId));
			}
		};
		void check.then(finishCheck, finishCheck);
		return check;
	}, [hostId, queryClient]);

	checkAuthRef.current = (agentId) => checkAuth(agentId, { fresh: true });

	// Reads the accounts again, so the CLI login's row shows a sign-in that just
	// changed, and returns whether that login is signed in.
	return {
		checkAuth,
		copyDiagnostics,
		copyText,
		requestUpdate,
		startAgentOperation,
		startAuth,
		verifyInstall,
	};
}
