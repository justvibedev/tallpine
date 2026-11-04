import type { MessageKey } from "../../i18n/messages";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useStartBacklogRun } from "../../hooks/useStartBacklogRun";
import type { BacklogRunScope, BacklogRunSelection } from "../../lib/backlog-run-api";
import { ConfirmDialog } from "../ConfirmDialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

const SCOPES = ["local", "pr", "pr-merge"] as const satisfies readonly BacklogRunScope[];

type BacklogRunDialogProps = {
	open: boolean;
	projectId: string;
	selection: BacklogRunSelection;
	onOpenChange: (open: boolean) => void;
};

/**
 * The one decision before a run: how far each item goes. Starting sends one
 * request, and the daemon posts one message to the project's orchestrator.
 */
export function BacklogRunDialog({ open, projectId, selection, onOpenChange }: BacklogRunDialogProps) {
	const { t } = useTranslation();
	const start = useStartBacklogRun(projectId);
	const [scope, setScope] = useState<BacklogRunScope>("pr");
	const [error, setError] = useState<string | undefined>();
	// A ref, not the mutation's pending flag: two clicks in one tick both see the same render.
	const inFlight = useRef(false);
	const target =
		selection.kind === "all"
			? t("backlog.run.target.all")
			: selection.kind === "parent"
				? t("backlog.run.target.parent", { ref: selection.ref })
				: selection.refs.join(", ");
	const submit = async () => {
		// A second click while the request is in flight must not start a second run.
		if (inFlight.current) return;
		inFlight.current = true;
		setError(undefined);
		try {
			await start.mutateAsync({ scope, selection });
			onOpenChange(false);
		} catch (failure) {
			setError(t("backlog.run.dialog.error", { message: failure instanceof Error ? failure.message : String(failure) }));
		} finally {
			inFlight.current = false;
		}
	};
	return (
		<ConfirmDialog
			open={open}
			title={t("backlog.run.dialog.title", { target })}
			description={t("backlog.run.dialog.description")}
			confirmLabel={t("backlog.run.dialog.start")}
			busy={start.isPending}
			error={error}
			onConfirm={() => void submit()}
			onOpenChange={onOpenChange}
		>
			<div className="flex items-center justify-between gap-3">
				<span id="backlog-run-scope-label" className="text-xs font-medium text-muted-foreground">{t("backlog.run.dialog.scope")}</span>
				<Select value={scope} onValueChange={(value) => setScope(value as BacklogRunScope)}>
					<SelectTrigger size="sm" aria-labelledby="backlog-run-scope-label" className="min-w-36">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{SCOPES.map((value) => <SelectItem key={value} value={value}>{t(`backlog.scope.${value}` as MessageKey)}</SelectItem>)}
					</SelectContent>
				</Select>
			</div>
		</ConfirmDialog>
	);
}
