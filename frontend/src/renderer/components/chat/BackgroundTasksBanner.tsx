import { memo, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Square } from "lucide-react";
import { formatDurationStatus } from "@tallpine/shared/format/duration";
import { useStopBackgroundTask } from "../../hooks/useStopBackgroundTask";
import type { SessionBackgroundTask } from "../../types/session-facts";
import { Button } from "../ui/button";

/**
 * The background tasks (commands, monitors, subagents) the agent is running.
 * Commands and monitors have a stop control; a subagent shows its model and
 * how long it has run, since the agent's turn can end or sit waiting while it
 * works. Shown only while there are any.
 */
export const BackgroundTasksBanner = memo(function BackgroundTasksBanner(props: {
	tasks?: SessionBackgroundTask[];
	sessionId: string;
	hostId?: string;
}) {
	// The list owns the stop mutation, so a chat with no tasks needs no query client.
	return props.tasks?.length ? <BackgroundTaskList {...props} tasks={props.tasks} /> : null;
});

function BackgroundTaskList({ tasks, sessionId, hostId }: { tasks: SessionBackgroundTask[]; sessionId: string; hostId?: string }) {
	const { t } = useTranslation();
	const { stop, stoppingId, error } = useStopBackgroundTask(sessionId, hostId);
	return (
		<div className="flex shrink-0 flex-col gap-1 border-b border-border bg-surface px-4 py-2 text-xs" aria-label={t("chat.backgroundTasks.title")}>
			<span className="font-medium text-muted-foreground">{t("chat.backgroundTasks.title")}</span>
			{tasks.map((task) => task.kind === "subagent" ? <SubagentRow key={task.id} task={task} /> : (
				<div key={task.id} className="flex items-center justify-between gap-2">
					<span className="min-w-0 truncate" title={task.name}>{task.name}</span>
					<Button
						type="button"
						size="sm"
						variant="ghost"
						className="h-6 shrink-0 gap-1 px-2 text-caption"
						disabled={stoppingId === task.id}
						aria-label={t("chat.backgroundTasks.stopTask", { name: task.name })}
						onClick={() => stop(task.id)}
					>
						<Square aria-hidden="true" className="size-3" />
						{stoppingId === task.id ? t("chat.backgroundTasks.stopping") : t("chat.backgroundTasks.stop")}
					</Button>
				</div>
			))}
			{error ? <span role="alert" className="text-destructive">{error}</span> : null}
		</div>
	);
}

/** A running background subagent: what it does, its model and its elapsed time. */
function SubagentRow({ task }: { task: SessionBackgroundTask }) {
	const { t } = useTranslation();
	const started = task.startedAt ? Date.parse(task.startedAt) : Number.NaN;
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!Number.isFinite(started)) return;
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, [started]);
	return (
		<div className="flex items-center gap-2">
			<span className="shrink-0 font-medium">{t("chat.backgroundTasks.subagent")}</span>
			<span className="min-w-0 truncate" title={task.name}>{task.name}</span>
			{task.model ? <span className="shrink-0 text-muted-foreground">{task.model}</span> : null}
			{Number.isFinite(started) ? (
				<span className="ml-auto shrink-0 tabular-nums text-muted-foreground">{formatDurationStatus(Math.max(0, now - started))}</span>
			) : null}
		</div>
	);
}
