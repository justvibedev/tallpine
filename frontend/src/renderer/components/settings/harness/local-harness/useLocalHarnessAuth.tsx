import { useCallback, useEffect } from "react";
import { agentAccountProviderFor, agentAccountsQueryKey, fetchAgentAccounts } from "../../../../hooks/useAgentAccounts";
import { type AgentId } from "../../../../lib/agent-options";
import { type HarnessDetails } from "../describe-harness";
import { HarnessAccountTab } from "../HarnessAccountTab";
import { closeAuthTerminal, type AuthTerminalWorkflow } from "../HarnessAuthTerminalPanel";
import type { LocalHarnessContentProps } from "./local-harness-types";
import { useLocalHarnessState } from "./useLocalHarnessState";
import { useLocalHarnessEffects } from "./useLocalHarnessEffects";
import { useLocalHarnessOperations } from "./useLocalHarnessOperations";
import { AgentAuthProbeResult, AUTH_VERIFY_ATTEMPTS, AUTH_VERIFY_RETRY_MS, AUTH_TERMINAL_LIFETIME_MS } from "./local-harness-types";

/** Login, logout and detail description for the local harness settings content. */
export function useLocalHarnessAuth(props: LocalHarnessContentProps, state: ReturnType<typeof useLocalHarnessState>, effects: ReturnType<typeof useLocalHarnessEffects>, ops: ReturnType<typeof useLocalHarnessOperations>) {
	const {
		hostId,
	} = props;
	const {
		authWorkflow,
		authWorkflowRef,
		copiedAgent,
		expandedDiagnostics,
		mountedRef,
		queryClient,
		readinessAgents,
		selectedAgentId,
		setAuthWorkflow,
		setExpandedDiagnostics,
		setInUseSessions,
		setLogoutRequest,
		setSelectedMethods,
		shellKey,
		t,
		describe,
	} = state;
	const {
		cliLoginOnOwnRow,
	} = effects;
	const {
		checkAuth,
		copyDiagnostics,
		copyText,
		requestUpdate,
		startAgentOperation,
		startAuth,
		verifyInstall,
	} = ops;
	const refreshCliLogin = useCallback(async (agentId: AgentId): Promise<boolean | undefined> => {
		const provider = hostId ? null : agentAccountProviderFor(agentId);
		if (!provider) return undefined;
		const accounts = await fetchAgentAccounts(true).catch(() => undefined);
		if (!accounts) return undefined;
		queryClient.setQueryData(agentAccountsQueryKey, accounts);
		return accounts.find((account) => account.provider === provider && account.device)?.signedIn;
	}, [hostId, queryClient]);

	const finishAuth = useCallback(async (workflow: AuthTerminalWorkflow) => {
		if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId || authWorkflowRef.current.phase !== "running") return;
		authWorkflowRef.current = { ...workflow, phase: "verifying", reason: undefined };
		setAuthWorkflow(authWorkflowRef.current);
		const signingIn = workflow.action !== "logout";
		const ownRow = cliLoginOnOwnRow(workflow.agentId);
		let cliSignedIn: boolean | undefined;
		const completed = async (candidate: AgentAuthProbeResult | undefined) => {
			if (ownRow) {
				cliSignedIn = await refreshCliLogin(workflow.agentId);
				return cliSignedIn === signingIn;
			}
						return !signingIn
				? candidate?.agent?.authStatus === "unauthorized"
				: candidate?.agent?.authStatus === "authorized";
		};
		let result = await checkAuth(workflow.agentId, { fresh: true });
		let done = await completed(result);
		for (let attempt = 1; attempt < AUTH_VERIFY_ATTEMPTS && !done; attempt++) {
			await new Promise((resolve) => window.setTimeout(resolve, AUTH_VERIFY_RETRY_MS));
			if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return;
			result = await checkAuth(workflow.agentId, { fresh: true });
			done = await completed(result);
		}
		if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return;
		const signedOut = ownRow ? cliSignedIn === false : result?.agent?.authStatus === "unauthorized";
		if (done) {
			if (!ownRow) void refreshCliLogin(workflow.agentId);
			try {
				await closeAuthTerminal(workflow.terminal.handleId, hostId);
			} catch (error) {
				setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? { ...current, phase: "cleanup_failed", reason: error instanceof Error ? error.message : t("settings.harness.authFailed") } : current);
				return;
			}
			authWorkflowRef.current = null;
			setAuthWorkflow(null);
			void queryClient.invalidateQueries({ queryKey: shellKey });
			return;
		}
		setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? {
			...current,
			phase: signedOut ? "unauthorized" : "unverified",
			reason: !signingIn ? t("settings.harness.logoutUnverified") : signedOut ? t("settings.harness.notLoggedIn") : t("settings.harness.loginUnknown"),
		} : current);
	}, [checkAuth, cliLoginOnOwnRow, hostId, queryClient, refreshCliLogin, shellKey, t]);

	const closeAuth = useCallback(async (workflow: AuthTerminalWorkflow): Promise<boolean> => {
		if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return false;
		setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? { ...current, phase: "closing", reason: undefined } : current);
		try {
			await closeAuthTerminal(workflow.terminal.handleId, hostId);
			void queryClient.invalidateQueries({ queryKey: shellKey });
			await checkAuth(workflow.agentId, { fresh: true });
			if (authWorkflowRef.current?.terminal.handleId === workflow.terminal.handleId) {
				authWorkflowRef.current = null;
				setAuthWorkflow(null);
			}
			return true;
		} catch (error) {
			setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? { ...current, phase: "cleanup_failed", reason: error instanceof Error ? error.message : t("settings.harness.authFailed") } : current);
			return false;
		}
	}, [checkAuth, hostId, queryClient, shellKey, t]);

	// A login the panel could not confirm may still have taken: the daemon keeps
	// re-checking readiness. Once the harness reads as logged in, close the panel
	// instead of leaving a stale "signed out" terminal on screen.
	useEffect(() => {
		if (!authWorkflow || (authWorkflow.phase !== "unauthorized" && authWorkflow.phase !== "unverified")) return;
		if (cliLoginOnOwnRow(authWorkflow.agentId)) return;
		const expected = authWorkflow.action === "logout" ? "unauthorized" : "authorized";
		if (readinessAgents.get(authWorkflow.agentId)?.authentication.state === expected) void closeAuth(authWorkflow);
	}, [authWorkflow, readinessAgents, closeAuth, cliLoginOnOwnRow]);

	useEffect(() => {
		if (!authWorkflow || authWorkflow.phase !== "running") return;
		const handleId = authWorkflow.terminal.handleId;
		const remaining = Math.max(0, AUTH_TERMINAL_LIFETIME_MS - (Date.now() - authWorkflow.startedAt));
		const timeout = window.setTimeout(async () => {
			if (authWorkflowRef.current?.terminal.handleId !== handleId) return;
			setAuthWorkflow((current) => current?.terminal.handleId === handleId ? { ...current, phase: "closing", reason: undefined } : current);
			try {
				await closeAuthTerminal(handleId, hostId);
				setAuthWorkflow((current) => current?.terminal.handleId === handleId ? { ...current, phase: "timed_out", reason: t("settings.harness.authTimedOut") } : current);
				void queryClient.invalidateQueries({ queryKey: shellKey });
				await checkAuth(authWorkflow.agentId, { fresh: true });
			} catch (error) {
				setAuthWorkflow((current) => current?.terminal.handleId === handleId ? { ...current, phase: "cleanup_failed", reason: error instanceof Error ? error.message : t("settings.harness.authFailed") } : current);
			}
		}, remaining);
		return () => window.clearTimeout(timeout);
	}, [authWorkflow, checkAuth, hostId, queryClient, shellKey, t]);

	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
			const workflow = authWorkflowRef.current;
			if (workflow) void closeAuthTerminal(workflow.terminal.handleId, hostId).catch(() => undefined);
		};
	}, [hostId]);


	const accountTab = (details: HarnessDetails) => {
		const { agentId, rowAuthWorkflow } = details;
		return <HarnessAccountTab
			details={details}
			hostId={hostId}
			authWorkflowActive={Boolean(authWorkflow)}
			copied={copiedAgent === agentId}
			diagnosticsExpanded={expandedDiagnostics[agentId] === true}
			onToggleDiagnostics={() => setExpandedDiagnostics((current) => ({ ...current, [agentId]: !current[agentId] }))}
			onCopyDiagnostics={(job) => void copyDiagnostics(agentId, job)}
			onInstall={(methodId) => void startAgentOperation(agentId, methodId, "install")}
			onSelectMethod={(methodId) => setSelectedMethods((current) => ({ ...current, [agentId]: methodId }))}
			onCopyCommand={(command) => void copyText(agentId, command)}
			onUpdate={() => requestUpdate(agentId)}
			onViewSessions={(waiting) => setInUseSessions({ agentId, inUse: waiting })}
			onLogin={() => void startAuth(agentId)}
			onLogout={() => setLogoutRequest(agentId)}
			onVerify={() => void verifyInstall(agentId)}
			onCloseAuth={() => { if (rowAuthWorkflow) void closeAuth(rowAuthWorkflow); }}
			onRetryAuth={() => {
				if (!rowAuthWorkflow) return;
				void closeAuth(rowAuthWorkflow).then((closed) => {
					if (!closed) return;
					if (rowAuthWorkflow.action === "logout") setLogoutRequest(agentId); else void startAuth(agentId);
				});
			}}
			onTerminalState={(state) => {
				if (rowAuthWorkflow && state === "exited" && authWorkflowRef.current?.phase === "running") void finishAuth(rowAuthWorkflow);
			}}
		/>;
	};

	const selectedDetails = selectedAgentId ? describe(selectedAgentId) : null;
	const detailsOpen = Boolean(selectedDetails);
	return {
		accountTab,
		detailsOpen,
		selectedDetails,
	};
}
