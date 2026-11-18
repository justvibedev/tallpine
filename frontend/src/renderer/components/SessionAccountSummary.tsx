import { LoaderCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { agentAccountName } from "../hooks/useAgentAccounts";
import type { WorkspaceSession } from "../types/workspace";
import { AccountLimits } from "./settings/AgentAccountsSection";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Spinner } from "./ui/spinner";
import { useSessionAccountMove } from "./useSessionAccountMove";

/** The account the session's agent runs under, its limits, and the move to
 * another account (see useSessionAccountMove; the top bar offers the same move). */
export function SessionAccountSummary({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const { available, options, currentId, account, moving, status, move, confirmDialog } = useSessionAccountMove(session);
	if (!available) return null;

	return <section className="mt-3 border-t border-(--color-border-settings-input) pt-3" data-testid="session-account-summary">
		<div className="flex items-center justify-between gap-3">
			<p className="text-xs text-muted-foreground">{t("settings.agentAccounts.sessionAccount")}</p>
			<div className="flex min-w-0 items-center gap-2">
				{moving ? <Spinner icon={LoaderCircle} className="size-3.5 text-muted-foreground" /> : null}
				<Select value={currentId ?? ""} disabled={moving || options.length < 2} onValueChange={(value) => void move(value)}>
					<SelectTrigger size="sm" className="max-w-56" aria-label={t("settings.agentAccounts.sessionAccount")}><SelectValue /></SelectTrigger>
					<SelectContent align="end">
						{options.map((option) => <SelectItem key={option.id} value={option.id} disabled={!option.signedIn}>{agentAccountName(option, t)}</SelectItem>)}
					</SelectContent>
				</Select>
			</div>
		</div>
		{status ? <p className="mt-2 text-xs text-muted-foreground" role="status">{status}</p> : null}
		{account ? <AccountLimits account={account} /> : null}
		{confirmDialog}
	</section>;
}
