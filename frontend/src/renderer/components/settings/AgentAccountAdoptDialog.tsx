import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { agentAccountName, type AgentAccount } from "../../hooks/useAgentAccounts";
import { useAdoptAgentAccount, useAdoptionPreview, type AccountAdoption, type AccountAdoptionPolicy } from "../../hooks/useAgentAccountAdopt";
import { cn } from "@/lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle, settingsDialogBodyClass, settingsDialogContentClass, settingsDialogFooterClass, settingsDialogHeaderClass } from "../ui/dialog";
import { AgentAccountAdoptProgress } from "./AgentAccountAdoptProgress";

/** "Use for all sessions": confirms which sessions move, starts the switch once,
 * then shows each session's outcome. */
export function AgentAccountAdoptDialog({ account, onClose }: { account: AgentAccount; onClose: () => void }) {
	const { t } = useTranslation();
	const preview = useAdoptionPreview(account.id, true);
	const adopt = useAdoptAgentAccount();
	// One key per opened dialog: a double click or a retry replays the same batch.
	const requestKey = useRef(crypto.randomUUID());
	const [policy, setPolicy] = useState<AccountAdoptionPolicy>("wait");
	const [submitting, setSubmitting] = useState(false);
	// A ref, not state: two clicks in one render pass must start one request.
	const inFlight = useRef(false);
	const [error, setError] = useState("");
	const [batch, setBatch] = useState<AccountAdoption | null>(null);
	const sessions = preview.data ?? [];
	const name = agentAccountName(account, t);
	const titles = Object.fromEntries(sessions.map((session) => [session.sessionId, session.title]));

	const confirm = async () => {
		if (inFlight.current) return;
		inFlight.current = true;
		setSubmitting(true);
		setError("");
		try {
			setBatch(await adopt(account.id, policy, requestKey.current));
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			inFlight.current = false;
			setSubmitting(false);
		}
	};

	if (batch) {
		return <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
			<DialogContent showCloseButton={false} className={cn(settingsDialogContentClass, "w-[min(460px,calc(100vw-24px))]")} onClick={(event) => event.stopPropagation()}>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("settings.agentAccounts.adopt.progressTitle", { account: name })}</DialogTitle>
					<DialogDescription className="sr-only">{t("settings.agentAccounts.adopt.progressTitle", { account: name })}</DialogDescription>
				</div>
				<div className={cn(settingsDialogBodyClass, "px-5 pt-0 pb-3")}>
					<AgentAccountAdoptProgress accountId={account.id} batch={batch} titles={titles} />
				</div>
				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					<Button type="button" variant="footer-primary" className="h-8 rounded-[10px] px-4 text-sm" onClick={onClose}>{t("settings.agentAccounts.adopt.close")}</Button>
				</div>
			</DialogContent>
		</Dialog>;
	}

	return <ConfirmDialog open
		title={t("settings.agentAccounts.adopt.title", { account: name })}
		description={t("settings.agentAccounts.adopt.description")}
		confirmLabel={t("settings.agentAccounts.adopt.confirm")}
		busy={submitting}
		confirmDisabled={preview.isLoading || preview.isError}
		error={error || (preview.isError ? preview.error.message : undefined)}
		onConfirm={() => void confirm()}
		onOpenChange={(open) => { if (!open) onClose(); }}
	>
		{preview.isLoading ? <p className="text-xs text-muted-foreground">{t("settings.agentAccounts.adopt.loading")}</p> : null}
		{preview.isSuccess && sessions.length === 0 ? <p className="text-xs text-muted-foreground">{t("settings.agentAccounts.adopt.none")}</p> : null}
		{sessions.length > 0 ? <ul className="max-h-48 divide-y divide-border overflow-y-auto rounded-md bg-muted/40" aria-label={t("settings.agentAccounts.adopt.title", { account: name })}>
			{sessions.map((session) => <li key={session.sessionId} className="px-3 py-1.5" data-session-id={session.sessionId}>
				<p className="truncate text-sm text-foreground">{session.title}</p>
				{session.isOrchestrator || !session.running ? <p className="text-xs text-muted-foreground">
					{[session.isOrchestrator ? t("settings.agentAccounts.adopt.last") : null, session.running ? null : t("settings.agentAccounts.adopt.stopped")].filter(Boolean).join(" · ")}
				</p> : null}
			</li>)}
		</ul> : null}
		{sessions.length > 0 ? <fieldset className="mt-3">
			<legend className="text-xs font-medium text-foreground">{t("settings.agentAccounts.adopt.policy")}</legend>
			{(["wait", "interrupt"] as const).map((value) => <label key={value} className="mt-1 flex items-center gap-2 text-sm text-foreground">
				<input type="radio" name="adopt-policy" value={value} checked={policy === value} onChange={() => setPolicy(value)} />
				{value === "wait" ? t("settings.agentAccounts.adopt.policyWait") : t("settings.agentAccounts.adopt.policyInterrupt")}
			</label>)}
		</fieldset> : null}
		<p className="mt-3 text-xs text-muted-foreground">{t("settings.agentAccounts.adopt.cacheNote")}</p>
	</ConfirmDialog>;
}
