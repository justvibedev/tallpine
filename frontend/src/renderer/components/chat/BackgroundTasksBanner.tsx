import { memo } from "react";
import { useTranslation } from "react-i18next";
import { Square } from "lucide-react";
import { useStopBackgroundTask } from "../../hooks/useStopBackgroundTask";
import type { SessionBackgroundTask } from "../../types/workspace";
import { Button } from "../ui/button";

/**
 * The background tasks (commands, monitors) the agent is running, each with a
 * stop control. Shown only while there are any.
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
			{tasks.map((task) => (
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
