import { ChevronRight, LoaderCircle } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import type { ConversationItem } from "../../types/conversation";
import { useChatLinkWorkspaces } from "./ChatMarkdown";
import { spawnedSessionIds } from "./SpawnedAgents";
import { Spinner } from "../ui/spinner";

/** One quiet line standing for a folded run of orchestration turns. */
export function JustMeRunRow({ count, open, live, workers, headline, onToggle }: {
	count: number;
	workers?: boolean;
	headline?: string;
	open: boolean;
	live: boolean;
	onToggle: () => void;
}) {
	const { t } = useTranslation();
	return (
		<div className="text-xs">
			<button
				type="button"
				aria-expanded={open}
				onClick={onToggle}
				className="inline-flex items-center gap-1.5 text-muted-foreground transition-colors hover:text-foreground"
			>
				<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
				{workers ? t("chat.workerRun.updates", { count }) : t("chat.justMe.hiddenUpdates", { count })}
				{workers && headline ? <span className="min-w-0 truncate text-muted-foreground/70">· {headline}</span> : null}
				{live ? (
					<span className="inline-flex items-center gap-1 text-status-working">
						<Spinner icon={LoaderCircle} className="size-3" />
						{t("chat.justMe.working")}
					</span>
				) : null}
			</button>
		</div>
	);
}

/** While orchestration is folded away, say that workers are still busy. */
export function JustMeLiveLine({ items }: { items: readonly ConversationItem[] }) {
	const { t } = useTranslation();
	const workspaces = useChatLinkWorkspaces();
	const spawned = useMemo(() => new Set(spawnedSessionIds(items)), [items]);
	const running = workspaces.reduce(
		(total, workspace) => total + workspace.sessions.filter((session) => spawned.has(session.id) && session.status === "working").length,
		0,
	);
	if (running === 0) return null;
	return (
		<div role="status" className="flex items-center gap-1.5 text-xs text-muted-foreground">
			<Spinner icon={LoaderCircle} className="size-3 text-status-working" />
			{t("chat.justMe.workersRunning", { count: running })}
		</div>
	);
}
