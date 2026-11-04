/** Change badges, panel messages and status marks for the workspace diff view. */
import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { type LucideIcon } from "lucide-react";
import { cn } from "../../lib/utils";
import { statusLabel, statusTone } from "../../lib/workspace-file-status";
import type { DiffRun } from "../../lib/diff-highlight";
import { Button } from "../ui/button";
import { WorkspaceFileStatus } from "./diff-annotation-types";

export function renderDiffRuns(runs: DiffRun[], add: boolean): ReactNode {
	return runs.map((run, index) =>
		run.changed ? (
			<span className={cn(run.className, "rounded-sm", add ? "bg-success/35" : "bg-error/35")} key={index}>
				{run.text}
			</span>
		) : run.className ? (
			<span className={run.className} key={index}>
				{run.text}
			</span>
		) : (
			run.text
		),
	);
}

export function ChangeBadges({ additions, deletions }: { additions: number; deletions: number }) {
	return (
		<span className="flex shrink-0 items-center gap-1 font-mono text-caption font-medium">
			{additions > 0 ? <span className="px-0.5 text-success">+{additions}</span> : null}
			{deletions > 0 ? <span className="px-0.5 text-error">-{deletions}</span> : null}
		</span>
	);
}

export function PanelMessage({ action, children, compact = false }: { action?: ReactNode; children: ReactNode; compact?: boolean }) {
	return (
		<div
			className={cn(
				"grid place-items-center text-center text-xs text-muted-foreground",
				compact ? "min-h-16 p-3" : "min-h-[180px] p-6",
			)}
		>
			<div className="flex max-w-sm flex-col items-center gap-3">
				<p>{children}</p>
				{action ?? null}
			</div>
		</div>
	);
}

/**
 * A pane with nothing to show: an icon over a short heading and an optional
 * way out, a little above the middle of the space it fills (where the eye
 * lands), so it reads as a state of the pane rather than a stray line at its top.
 */
export function EmptyMessage({ icon: Icon, title, action }: { icon: LucideIcon; title: ReactNode; action?: ReactNode }) {
	return (
		<div className="flex min-h-[180px] flex-1 flex-col items-center justify-center gap-3 p-6 pb-[15vh] text-center" data-testid="empty-message">
			<span aria-hidden="true" className="flex size-10 items-center justify-center rounded-lg bg-interactive-hover text-muted-foreground">
				<Icon className="size-5" />
			</span>
			<p className="max-w-xs text-heading-sm font-medium text-foreground">{title}</p>
			{action ?? null}
		</div>
	);
}

export function RetryButton({ onClick }: { onClick: () => void }) {
	const { t } = useTranslation();
	return (
		<Button onClick={onClick} size="sm" type="button" variant="outline">
			{t("files.retry")}
		</Button>
	);
}

export function StatusMark({ status }: { status: WorkspaceFileStatus }) {
	const { t } = useTranslation();
	const label = statusLabel[status];
	return (
		<span
			className={cn(
				"inline-flex w-5 shrink-0 items-center justify-center font-mono text-caption font-medium",
				statusTone[status],
			)}
			title={t(`files.status.${status}`)}
		>
			{label}
		</span>
	);
}
