import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { type WorkspaceSession } from "../../types/workspace";
import { endLane } from "../../lib/end-lane";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { invalidateArchivedSessions } from "../../hooks/useArchivedSessions";
import { ConfirmDialog } from "../ConfirmDialog";

/**
 * Confirm for "End lane…". Opening it asks the daemon for a dry run, so the
 * live-worker count is real and an orchestrator whose worktree holds unsaved
 * work is refused here, before the user confirms.
 */
export function EndLaneDialog({
	open,
	onOpenChange,
	session,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [workers, setWorkers] = useState<number | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);

	useEffect(() => {
		if (!open) return;
		let cancelled = false;
		setWorkers(null);
		setError(null);
		endLane(session.id, { dryRun: true, hostId: session.hostId }).then(
			(result) => !cancelled && setWorkers(result.workers.length),
			(err: unknown) => !cancelled && setError(err instanceof Error ? err.message : String(err)),
		);
		return () => {
			cancelled = true;
		};
	}, [open, session.id, session.hostId]);

	const confirm = async () => {
		setBusy(true);
		try {
			await endLane(session.id, { hostId: session.hostId });
			onOpenChange(false);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) });
			void invalidateArchivedSessions(queryClient, session.hostId);
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(false);
		}
	};

	const lane = session.lane ?? "";
	return (
		<ConfirmDialog
			busy={busy}
			confirmAriaLabel={t("endLane.confirmAria", { lane })}
			confirmDisabled={workers === null}
			confirmLabel={t("endLane.confirm")}
			description={workers === null && !error ? t("endLane.checking") : t("endLane.body", { count: workers ?? 0, lane })}
			destructive
			error={error}
			onConfirm={() => void confirm()}
			onOpenChange={onOpenChange}
			open={open}
			title={t("endLane.title", { lane })}
		/>
	);
}
