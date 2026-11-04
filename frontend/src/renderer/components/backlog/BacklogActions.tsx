import type { MessageKey } from "../../i18n/messages";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { BacklogItem, BacklogState } from "../../lib/backlog-api";
import { useSetBacklogState } from "../../hooks/useSetBacklogState";
import { useUpdateBacklogItem } from "../../hooks/useUpdateBacklogItem";
import { discussText, prefillChatDraft, sendText } from "../../lib/backlog-chat-draft";
import { useUiStore } from "../../stores/ui-store";
import { findProjectOrchestrator, type WorkspaceSummary } from "../../types/workspace";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { BacklogHoldDialog } from "./BacklogHoldDialog";
import { BacklogReasonButton } from "./BacklogReasonButton";

/**
 * The item's state actions. Closing and accepting are reversible, so they act at
 * once and offer Undo in a toast rather than asking first.
 */
export function BacklogActions({ item, workspaces, onOpenSession, children }: { item: BacklogItem; workspaces: readonly WorkspaceSummary[]; onOpenSession: (sessionId: string) => void; children?: React.ReactNode }) {
	const { t } = useTranslation();
	const setState = useSetBacklogState(item.projectId);
	const update = useUpdateBacklogItem(item.projectId);
	const showToast = useUiStore((state) => state.showGlobalToast);
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const [holdOpen, setHoldOpen] = useState(false);
	const [confirmStart, setConfirmStart] = useState(false);
	const orchestrator = findProjectOrchestrator([...workspaces], item.projectId);
	const noOrchestrator = orchestrator ? undefined : t("backlog.reason.noOrchestrator");
	const startWorker = (force: boolean) => requestNewTask(item.projectId, undefined, { ref: item.ref, title: item.title, ...(force ? { force: true } : {}) });
	// Both chat actions only fill the orchestrator's draft; the user reads it and sends it.
	const toChat = (text: string) => {
		if (!orchestrator) return;
		if (!prefillChatDraft(orchestrator, text)) {
			showToast(t("backlog.toast.draftError"), undefined, "error");
			return;
		}
		onOpenSession(orchestrator.id);
	};

	const moveTo = async (next: BacklogState, message: "done" | "dropped" | "accepted" | "reopened") => {
		const previous = item.state as BacklogState;
		try {
			await setState.mutateAsync({ ref: item.ref, state: next });
		} catch (failure) {
			showToast(t("backlog.toast.error", { ref: item.ref }), failure instanceof Error ? failure.message : String(failure), "error");
			return;
		}
		showToast(t(`backlog.toast.${message}` as MessageKey, { ref: item.ref }), undefined, {
			durationMs: 8_000,
			action: {
				label: t("backlog.toast.undo"),
				onClick: () => {
					setState.mutateAsync({ ref: item.ref, state: previous }).catch((failure: unknown) =>
						showToast(t("backlog.toast.error", { ref: item.ref }), failure instanceof Error ? failure.message : String(failure), "error"),
					);
				},
			},
		});
	};
	const setHold = (hold: string) => update.mutateAsync({ ref: item.ref, body: { hold } });

	return (
		<div className="flex flex-wrap items-center gap-2">
			{item.state === "inbox" ? (
				<Button type="button" size="sm" onClick={() => void moveTo("open", "accepted")}>{t("backlog.action.accept")}</Button>
			) : null}
			{item.state === "open" ? (
				<BacklogReasonButton
					type="button"
					size="sm"
					disabledReason={item.childCount > 0 ? t("backlog.reason.parent") : undefined}
					onClick={() => (item.hold !== "" ? setConfirmStart(true) : startWorker(false))}
				>
					{t("backlog.action.start")}
				</BacklogReasonButton>
			) : null}
			{item.state === "open" ? (
				<Button type="button" size="sm" variant="secondary" onClick={() => void moveTo("done", "done")}>{t("backlog.action.done")}</Button>
			) : null}
			{item.state === "open" && item.hold === "" ? (
				<Button type="button" size="sm" variant="ghost" onClick={() => setHoldOpen(true)}>{t("backlog.action.hold")}</Button>
			) : null}
			{item.state === "open" && item.hold !== "" ? (
				<Button type="button" size="sm" variant="ghost" onClick={() => void setHold("")}>{t("backlog.action.unhold")}</Button>
			) : null}
			{item.state === "inbox" || item.state === "open" ? (
				<Button type="button" size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => void moveTo("dropped", "dropped")}>
					{t("backlog.action.drop")}
				</Button>
			) : null}
			{item.state === "inbox" || item.state === "open" ? (
				<BacklogReasonButton type="button" size="sm" variant="ghost" disabledReason={noOrchestrator} onClick={() => toChat(discussText(item))}>
					{t("backlog.action.discuss")}
				</BacklogReasonButton>
			) : null}
			{item.state === "open" ? (
				<BacklogReasonButton type="button" size="sm" variant="ghost" disabledReason={noOrchestrator} onClick={() => toChat(sendText(item))}>
					{t("backlog.action.send")}
				</BacklogReasonButton>
			) : null}
			{item.state === "done" || item.state === "dropped" ? (
				<Button type="button" size="sm" variant="secondary" onClick={() => void moveTo("open", "reopened")}>{t("backlog.action.reopen")}</Button>
			) : null}
			{children}
			<ConfirmDialog
				open={confirmStart}
				title={t("backlog.startHeld.title", { ref: item.ref })}
				description={t("backlog.startHeld.description", { reason: item.hold })}
				confirmLabel={t("backlog.action.start")}
				onConfirm={() => {
					setConfirmStart(false);
					startWorker(true);
				}}
				onOpenChange={setConfirmStart}
			/>
			<BacklogHoldDialog open={holdOpen} itemRef={item.ref} onOpenChange={setHoldOpen} onHold={setHold} />
		</div>
	);
}
