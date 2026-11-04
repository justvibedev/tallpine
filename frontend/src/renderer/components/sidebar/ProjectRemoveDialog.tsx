import { useTranslation } from "react-i18next";
import { openPRs, type WorkspaceSummary } from "../../types/workspace";
import { ConfirmDialog } from "../ConfirmDialog";

export function ProjectRemoveDialog({ workspace, open, onOpenChange, onConfirm }: { workspace: WorkspaceSummary; open: boolean; onOpenChange: (open: boolean) => void; onConfirm: () => void | Promise<void> }) {
	const { t } = useTranslation();
	const openPullRequestCount = new Set(
		workspace.sessions.flatMap((session) => openPRs(session).map((pr) => pr.url)),
	).size;

	return (
		<ConfirmDialog
			open={open}
			onOpenChange={onOpenChange}
			title={t("shell.removeProjectTitle")}
			description={
				<>
					<p className="text-sm font-medium text-foreground">{t("shell.removeProjectLead", { name: workspace.name })}</p>
					<p className="mt-1 text-xs text-muted-foreground">
						{t("shell.removeProjectBody")}
					</p>
					{openPullRequestCount > 0 ? (
						<p className="mt-2 text-xs font-medium text-error">
							{t("shell.removeProjectOpenPrWarning", { count: openPullRequestCount })}
						</p>
					) : null}
				</>
			}
			confirmLabel={t("shell.remove")}
			destructive
			onConfirm={onConfirm}
		/>
	);
}
