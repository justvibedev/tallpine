import { splitArtifacts, splitHeadline } from "@tallpine/shared/chat/automation/worker-reports";
import { GitPullRequest, LoaderCircle, Split, ChevronRight } from "lucide-react";
import { useContext, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { needsInputView } from "./needsInputAudienceView";
import { cn } from "../../lib/utils";
import type { ConversationItem } from "../../types/conversation";
import type { PullRequestFacts } from "../../types/workspace";
import { SessionLabelLink, useChatLinkWorkspaces } from "./ChatMarkdown";
import { sessionHref, useWorkerSession, WorkerUpdateIndexContext } from "./WorkerUpdates";
import { Spinner } from "../ui/spinner";

const readyToMerge = (pr: PullRequestFacts | undefined) =>
	pr?.state === "open" && pr.ci === "passing" && pr.review === "approved" && pr.mergeability !== "conflicting";

// What `tallpine spawn` prints: `spawned session <id> "<name>" (<status>)`.
// ponytail: reads command output; #6507's session.spawned rows replace this once merged.
const SPAWNED = /\bspawned\s+session\s+([\w.-]+)/i;

export type SpawnedSession = { id: string; at: string };

export const spawnedSessionIds = (items: readonly ConversationItem[]): string[] => spawnedSessions(items).map((spawn) => spawn.id);

/** Each spawned worker with the time its spawn command ran. */
export function spawnedSessions(items: readonly ConversationItem[]): SpawnedSession[] {
	const ids: SpawnedSession[] = [];
	for (const item of items) {
		if (item.kind !== "activity" || item.activityKind !== "command") continue;
		if (!/\b(?:tallpine|tp)'?\s+spawn\b/.test(item.detail?.command ?? item.summary)) continue;
		const match = SPAWNED.exec(item.detail?.output ?? "");
		if (match) ids.push({ id: match[1]!, at: item.createdAt });
	}
	return ids;
}

/** The workers a turn started, each showing where it is now. */
export function SpawnedAgents({ spawns }: { spawns: readonly SpawnedSession[] }) {
	const [open, setOpen] = useState(true);
	return (
		<div className="min-w-0 text-xs">
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((current) => !current)}
				className="inline-flex items-center gap-1.5 text-muted-foreground transition-colors hover:text-foreground"
			>
				<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
				<Split aria-hidden="true" className="size-3.5" />
				{spawns.length === 1 ? "Spawned 1 agent" : `Spawned ${spawns.length} agents`}
			</button>
			{open ? (
				<div className="mt-1 flex flex-col">
					{spawns.map(({ id, at }) => (
						<SpawnedAgentLine key={id} sessionId={id} since={at} />
					))}
				</div>
			) : null}
		</div>
	);
}

// A just-spawned worker can take a moment to reach the session list.
const LIST_GRACE_MS = 60_000;

function SpawnedAgentLine({ sessionId, since }: { sessionId: string; since?: string }) {
	const { t } = useTranslation();
	const session = useWorkerSession(sessionId);
	const loaded = useChatLinkWorkspaces().length > 0 && !!since && Date.now() - Date.parse(since) > LIST_GRACE_MS;
	const latest = useContext(WorkerUpdateIndexContext)?.latest.get(sessionId);
	const pr = session?.prs[0];
	const name = session?.title || latest?.name || sessionId;
	const href = session?.workspaceId ? sessionHref({ sessionId, projectId: session.workspaceId }) : latest ? sessionHref(latest) : undefined;
	let lead: ReactNode;
	let status: ReactNode;
	if (pr) {
		const ready = readyToMerge(pr);
		lead = <GitPullRequest aria-hidden="true" className={cn("size-3.5 shrink-0", ready ? "text-status-ready" : "text-muted-foreground")} />;
		status = ready ? `#${pr.number} · ready to merge` : `#${pr.number}`;
	} else if (latest?.state === "done") {
		lead = <span aria-hidden="true" className="mx-[3.5px] size-[7px] shrink-0 rounded-full bg-status-ready" />;
		status = "Done";
	} else if (session?.status === "terminated" || session?.status === "exited") {
		// A gone worker cannot still need you or be stuck, whatever its last report said.
		lead = <span aria-hidden="true" className="mx-[3.5px] size-[7px] shrink-0 rounded-full bg-muted-foreground" />;
		status = session.status === "terminated" ? "Terminated" : "Exited";
	} else if (!session && loaded) {
		// A worker that left the session list (killed, pruned) is gone, whatever its last report said.
		lead = <span aria-hidden="true" className="mx-[3.5px] size-[7px] shrink-0 rounded-full bg-muted-foreground" />;
		status = "Ended";
	} else if (latest?.state === "needs_input" && !needsInputView(session, t).attention) {
		lead = <span aria-hidden="true" className="mx-[3.5px] size-[7px] shrink-0 rounded-full bg-muted-foreground" />;
		status = needsInputView(session, t).status;
	} else if (latest?.state === "needs_input" || latest?.state === "stuck") {
		lead = <span aria-hidden="true" className="mx-[3.5px] size-[7px] shrink-0 rounded-full bg-status-needs-you-dot" />;
		status = <span className="text-status-needs-you">{latest.state === "stuck" ? "Stuck" : "Needs input"}</span>;
	} else if (session?.status === "idle") {
		// An idle worker with no attention-state report is not working.
		lead = <span aria-hidden="true" className="mx-[3.5px] size-[7px] shrink-0 rounded-full bg-muted-foreground" />;
		status = "Idle";
	} else {
		lead = <Spinner icon={LoaderCircle} className="size-3.5 shrink-0 text-status-working" />;
		status = latest ? splitHeadline(splitArtifacts(latest.body).text).headline : "Working";
	}
	return (
		<div className="flex min-w-0 items-center gap-2 py-0.5 pl-5">
			{lead}
			<span className="min-w-0 shrink truncate text-foreground">
				{href ? <SessionLabelLink href={href} className="decoration-foreground/40 underline-offset-2 hover:underline">{name}</SessionLabelLink> : name}
			</span>
			<span className="ml-auto max-w-[48%] shrink-0 truncate text-muted-foreground" title={typeof status === "string" ? status : undefined}>{status}</span>
		</div>
	);
}
