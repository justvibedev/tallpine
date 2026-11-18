import { ChevronDown, CircleUserRound, LoaderCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { agentAccountName } from "../hooks/useAgentAccounts";
import type { WorkspaceSession } from "../types/workspace";
import { TopbarButton } from "./TopbarButton";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Spinner } from "./ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { useSessionAccountMove } from "./useSessionAccountMove";

/** The short form of an account name for the top bar: an email's local part. */
export function topbarAccountLabel(name: string): string {
	const at = name.indexOf("@");
	return at > 0 ? name.slice(0, at) : name;
}

/** The session's AI account in the top bar, and the move to another one: the
 * same move (and Stop and move confirmation) as the inspector Summary's
 * Account select. Hidden when the harness has no saved accounts. */
export function TopbarAccountMenu({ session, style }: { session: WorkspaceSession; style?: React.CSSProperties }) {
	const { t } = useTranslation();
	const { available, options, currentId, account, moving, status, move, confirmDialog } = useSessionAccountMove(session);
	if (!available || options.length === 0) return null;
	const name = account ? agentAccountName(account, t) : t("settings.agentAccounts.sessionAccount");
	const tooltip = status || t("shell.aiAccount", { name });

	return (
		<>
			<DropdownMenu modal={false}>
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex" style={style}>
							<DropdownMenuTrigger asChild>
								<TopbarButton
									aria-label={t("shell.aiAccount", { name })}
									// The menu is anchored here: pressing must not scale the trigger.
									className="topbar-control--labeled topbar-control--static-press"
									data-priority="secondary"
									data-testid="topbar-account-menu"
									disabled={moving}
									variant="secondary"
								>
									{moving
										? <Spinner icon={LoaderCircle} className="size-icon-md" />
										: <CircleUserRound aria-hidden="true" className="size-icon-md" />}
									<span className="max-w-28 truncate" data-compact-label>{topbarAccountLabel(name)}</span>
									<ChevronDown aria-hidden="true" className="size-icon-sm opacity-60" data-compact-label />
								</TopbarButton>
							</DropdownMenuTrigger>
						</span>
					</TooltipTrigger>
					<TooltipContent side="bottom">{tooltip}</TooltipContent>
				</Tooltip>
				<DropdownMenuContent align="end" className="min-w-56">
					<DropdownMenuLabel>{t("settings.agentAccounts.sessionAccount")}</DropdownMenuLabel>
					<DropdownMenuRadioGroup value={currentId ?? ""} onValueChange={(value) => void move(value)}>
						{options.map((option) => (
							<DropdownMenuRadioItem key={option.id} value={option.id} disabled={!option.signedIn}>
								<span className="truncate">{agentAccountName(option, t)}</span>
							</DropdownMenuRadioItem>
						))}
					</DropdownMenuRadioGroup>
				</DropdownMenuContent>
			</DropdownMenu>
			{confirmDialog}
		</>
	);
}
