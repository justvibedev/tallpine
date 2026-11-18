import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "./ConfirmDialog";
import type { WorkspaceSession } from "../types/workspace";
import { agentAccountProviderFor, useAgentAccountActions, useAgentAccountLabel, useAgentAccounts, useSessionAgentAccounts } from "../hooks/useAgentAccounts";
import { interfaceTransitionIsActive, useSessionInterfaceTransitionStatus } from "../hooks/useSessionInterfaceTransition";

/** The account a session's agent runs under and the move to another one,
 * shared by the inspector Summary and the session top bar. A move is the
 * session's durable controller handoff: the agent restarts in the same
 * conversation on the new account, or stays on the old one if that fails.
 * Moving a working agent stops its turn after asking (`confirmDialog`).
 * `available` is false when the harness has no accounts or the session ended. */
export function useSessionAccountMove(session: WorkspaceSession) {
	const { t } = useTranslation();
	const provider = agentAccountProviderFor(session.provider);
	const accounts = useAgentAccounts(Boolean(provider));
	const current = useSessionAgentAccounts(provider ? session.id : undefined);
	const actions = useAgentAccountActions();
	const label = useAgentAccountLabel(Boolean(provider));
	const { transition } = useSessionInterfaceTransitionStatus(provider ? session.id : undefined);
	const [message, setMessage] = useState("");
	const [confirming, setConfirming] = useState<string | null>(null);
	const accountMove = transition?.targetAccountId ? transition : undefined;
	const moving = interfaceTransitionIsActive(accountMove);
	const settledPhase = accountMove && !moving ? accountMove.phase : undefined;

	// The binding commits at the end of the move; reread it once the move settles.
	useEffect(() => {
		if (settledPhase) void actions.refreshSession(session.id);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [settledPhase, accountMove?.id, session.id]);

	const options = provider ? (accounts.data ?? []).filter((account) => account.provider === provider) : [];
	const currentId = provider ? current.data?.[provider] : undefined;
	const account = options.find((option) => option.id === currentId);
	const working = session.activity?.state === "active" || session.activity?.state === "blocked";
	// "Moved from … to …" answers the move; the next message makes it history.
	const messagedSinceMove = Boolean(accountMove && session.lastUserMessageAt &&
		Date.parse(session.lastUserMessageAt) > Date.parse(accountMove.updatedAt));

	const move = async (accountId: string, stopTurn = false) => {
		if (accountId === currentId) return;
		// A working agent may only be waiting on the old account's limit; moving
		// stops that turn, so ask first.
		if (working && !stopTurn) {
			setConfirming(accountId);
			return;
		}
		setConfirming(null);
		setMessage("");
		try {
			await actions.switchSession(session.id, accountId, stopTurn);
		} catch (err) {
			setMessage(err instanceof Error ? err.message : String(err));
		}
	};

	const status = moving
		? t("settings.agentAccounts.switching", { email: label(accountMove?.targetAccountId) })
		: settledPhase === "completed" && accountMove?.targetAccountId === currentId && !messagedSinceMove
			? t("settings.agentAccounts.moved", { from: label(accountMove?.sourceAccountId), to: label(currentId) })
			: settledPhase === "failed" || settledPhase === "recovery_required"
				? accountMove?.errorDetail || t("settings.agentAccounts.switchFailed")
				: message;

	const confirmDialog = confirming ? <ConfirmDialog open
		title={t("settings.agentAccounts.stopAndMoveTitle")}
		description={session.activity?.state === "blocked"
			? t("settings.agentAccounts.stopAndMoveDeciding", { email: label(confirming) })
			: t("settings.agentAccounts.stopAndMoveWorking", { email: label(confirming) })}
		confirmLabel={t("settings.agentAccounts.stopAndMove")}
		onConfirm={() => void move(confirming, true)}
		onOpenChange={(open) => { if (!open) setConfirming(null); }}
	/> : null;

	return {
		available: Boolean(provider) && !session.isTerminated,
		options,
		currentId,
		account,
		moving,
		status,
		move,
		confirmDialog,
	};
}
