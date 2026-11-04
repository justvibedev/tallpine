import type { SessionProvisionStep, WorkspaceSession } from "../../types/workspace";
import type { ConversationSnapshot } from "../../types/conversation";
import { useMemo, useRef } from "react";
import { sessionBranchPresentation } from "../../lib/session-branch";
import { chatDraftScopeKey, type ChatDraftScope } from "../../lib/chat-drafts";
import { createTerminalMux, muxUrlFromApiBase } from "../../lib/terminal-mux";
import { agentLabel } from "../../lib/agent-options";
import { continuableTurnId } from "./ChatWorkspaceProps";
import { activeTurn } from "../../types/conversation";

/**
 * Startup, provisioning and continue-turn state derived from the controller and session.
 */
export function useChatStartup({
	draftScope,
	remoteHostId,
	session,
	assetBaseUrl,
	snapshot,
	onContinueTurn,
	newWorkDisabled,
	resumingAgent,
	startingSteps,
	onResumeAgent,
	resumeError,
}: {
	draftScope: ChatDraftScope;
	remoteHostId: string | undefined;
	session: WorkspaceSession | undefined;
	assetBaseUrl: string | undefined;
	snapshot: ConversationSnapshot;
	onContinueTurn: (() => Promise<unknown> | void) | undefined;
	newWorkDisabled: boolean;
	resumingAgent: boolean | undefined;
	startingSteps: readonly SessionProvisionStep[] | undefined;
	onResumeAgent: (() => void | Promise<unknown>) | undefined;
	resumeError: string | undefined;
}) {
	const draftScopeKey = chatDraftScopeKey(draftScope);

	const uiSessionId = draftScope.sessionId;

	const activeRemoteHostId = remoteHostId ?? session?.hostId;

	const remoteCreateMux = useMemo(
		() => assetBaseUrl ? () => createTerminalMux(muxUrlFromApiBase(assetBaseUrl)) : undefined,
		[assetBaseUrl],
	);

	const turn = activeTurn(snapshot);

	const canContinue = Boolean(onContinueTurn) && !turn && !newWorkDisabled && Boolean(continuableTurnId(snapshot));

	const latestSettledTurn = snapshot.turns.findLast((entry) =>
		!entry.rolledBack && entry.state !== "queued" && entry.state !== "cancelled",
	);

	const limitedTurn = latestSettledTurn?.state === "failed" && latestSettledTurn.usageLimit &&
		latestSettledTurn.providerTurnId && !latestSettledTurn.hasRetryAttempt &&
		!snapshot.turns.some((entry) => entry.retryOfTurnId === latestSettledTurn.id) &&
		latestSettledTurn.hasHumanPrompt
		? latestSettledTurn : undefined;

	// The primary Chat view wakes a sleeping provider in the background. Its
	// marker clears before the new controller is ready, so an intermediate
	// "stopped" snapshot is still part of that wake, not a crashed agent.
	const wakingFromHibernate = useRef(snapshot.controller.state === "hibernated");

	if (snapshot.controller.state === "hibernated") wakingFromHibernate.current = true;

	if (snapshot.controller.state === "ready" || snapshot.controller.state === "busy") wakingFromHibernate.current = false;

	if (snapshot.controller.state === "stopped" && !resumingAgent) wakingFromHibernate.current = false;

	const suppressStopped = wakingFromHibernate.current && snapshot.controller.state === "stopped";

	// After a restart the daemon is still verifying this session and reconnects
	// its surviving agent itself. Until it settles, "stopped" is not a state to act on.
	const startupReconnecting = snapshot.controller.state === "stopped" && !resumingAgent &&
		session?.statusReadiness === "checking";

	const startupState =
		session?.provisionState === "provisioning" || session?.provisionState === "failed"
			? session.provisionState
			: undefined;

	// While a session starts, its opening brief reads as sent: the setup checklist
	// under it explains why nothing has answered yet. Later messages still queue.
	const provisionSteps = session?.provisionSteps ?? startingSteps;

	// Ready can arrive before the first turn does. Keep its setup slot through
	// that gap; selecting the first turn also never promotes a queued follow-up.
	const hasStartup = Boolean(
		startupState || (provisionSteps?.length &&
			(snapshot.turns[0]?.state === "queued" || snapshot.turns[0]?.state === "running")),
	);

	const openingTurnId = hasStartup
		? snapshot.turns[0]?.id
		: undefined;

	const provisionError = session?.provisionError;

	const { branch: sessionBranch, naming: namingBranch } = sessionBranchPresentation(session);

	const startup = useMemo(
		() =>
			hasStartup
				? {
						failed: startupState === "failed",
						steps: provisionSteps ?? [],
						error: provisionError,
						agentName: agentLabel(snapshot.harness),
						branch: sessionBranch,
						namingBranch,
						openingTurnId,
						onRetry: newWorkDisabled ? undefined : onResumeAgent,
						retrying: resumingAgent,
						retryError: resumeError,
					}
				: undefined,
		[
			hasStartup,
			namingBranch,
			newWorkDisabled,
			onResumeAgent,
			openingTurnId,
			provisionError,
			provisionSteps,
			resumeError,
			resumingAgent,
			sessionBranch,
			snapshot.harness,
			startupState,
		],
	);

	return {
		draftScopeKey,
		uiSessionId,
		activeRemoteHostId,
		remoteCreateMux,
		turn,
		canContinue,
		limitedTurn,
		suppressStopped,
		startupReconnecting,
		startupState,
		provisionSteps,
		hasStartup,
		openingTurnId,
		startup,
	};
}
