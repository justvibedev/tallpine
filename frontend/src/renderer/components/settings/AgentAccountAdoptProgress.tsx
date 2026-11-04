import { CheckCircle2, CircleAlert, Clock, LoaderCircle, MinusCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAdoptionBatch, type AccountAdoption, type AccountAdoptionItem } from "../../hooks/useAgentAccountAdopt";
import { Spinner } from "../ui/spinner";

type ItemState = AccountAdoptionItem["state"];

function StateGlyph({ state }: { state: ItemState }) {
	if (state === "moved" || state === "rebound") return <CheckCircle2 className="size-4 shrink-0 text-success" aria-hidden="true" />;
	if (state === "failed") return <CircleAlert className="size-4 shrink-0 text-error" aria-hidden="true" />;
	if (state === "skipped") return <MinusCircle className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />;
	if (state === "moving") return <Spinner icon={LoaderCircle} className="size-4 shrink-0 text-muted-foreground" />;
	return <Clock className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />;
}

/** Per-session outcomes of a "Use for all sessions" batch. A busy session shows
 * as waiting and settles by itself; the list refreshes while the batch runs. */
export function AgentAccountAdoptProgress({ accountId, batch, titles }: {
	accountId: string;
	batch: AccountAdoption;
	/** Session names from the confirmation, keyed by session id. */
	titles: Record<string, string>;
}) {
	const { t } = useTranslation();
	const live = useAdoptionBatch(accountId, batch.id, batch).data ?? batch;
	const settled = live.items.filter((item) => item.state === "moved" || item.state === "rebound" || item.state === "failed" || item.state === "skipped").length;
	return <div data-testid="agent-account-adopt-progress">
		<p className="text-xs text-muted-foreground" role="status" aria-live="polite">
			{live.state === "completed" ? t("settings.agentAccounts.adopt.allDone") : t("settings.agentAccounts.adopt.progressSummary", { done: settled, total: live.items.length })}
		</p>
		<ul className="mt-2 max-h-64 divide-y divide-border overflow-y-auto rounded-md bg-muted/40">
			{live.items.map((item) => <li key={item.sessionId} className="flex items-start gap-2 px-3 py-2" data-session-id={item.sessionId} data-state={item.state}>
				<StateGlyph state={item.state} />
				<div className="min-w-0 flex-1">
					<p className="truncate text-sm text-foreground">{titles[item.sessionId] ?? item.sessionId}</p>
					<p className="text-xs text-muted-foreground">{t(`settings.agentAccounts.adopt.state.${item.state}`)}</p>
					{item.state === "failed" && item.errorMessage ? <p className="mt-0.5 break-words text-xs text-error">{item.errorMessage}</p> : null}
				</div>
			</li>)}
		</ul>
		{live.state === "running" ? <p className="mt-2 text-xs text-muted-foreground">{t("settings.agentAccounts.adopt.progressWaitNote")}</p> : null}
	</div>;
}
