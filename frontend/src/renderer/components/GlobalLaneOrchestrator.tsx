import * as Dialog from "@radix-ui/react-dialog";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { apiErrorMessage } from "../lib/api-client";
import { backlogListKey, createBacklogItem, listBacklog, setBacklogState, type BacklogItem } from "../lib/backlog-api";
import { isArchivedSession } from "../lib/standalone-archive";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import type { WorkspaceSession } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
import { settingsDialogBodyClass, settingsDialogContentClass, settingsDialogFooterClass, settingsDialogHeaderClass } from "./ui/dialog";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

// Radix Select forbids an empty item value, so "no lane" gets a sentinel.
const NO_LANE = "__no_lane__";
const NEW_LANE = "__new_lane__";

/** The open top-level backlog items: the topics an orchestrator lane can own. */
export function laneChoices(items: readonly BacklogItem[]): BacklogItem[] {
	return items.filter((item) => !item.parentId);
}

/** Lane refs that already have a live orchestrator in the project. */
export function runningLaneRefs(workspaces: ReadonlyArray<{ id: string; sessions: ReadonlyArray<WorkspaceSession> }>, projectId: string | null): Set<string> {
	const refs = new Set<string>();
	for (const workspace of workspaces) {
		if (workspace.id !== projectId) continue;
		for (const session of workspace.sessions) {
			if (session.kind === "orchestrator" && session.lane && !isArchivedSession(session)) refs.add(session.lane);
		}
	}
	return refs;
}

/**
 * App-level "New orchestrator" with an optional lane. Always mounted in the
 * shell; the palette's "New orchestrator in a lane…" opens it. Leaving the lane
 * on "No lane" starts or opens the project's default orchestrator, as before.
 */
export function GlobalLaneOrchestrator() {
	const requested = useUiStore((state) => state.laneOrchestratorRequest !== null);
	// Mounted on first use and kept after: its query and mutation hooks stay out
	// of every route until someone asks for a lane orchestrator.
	const [everRequested, setEverRequested] = useState(requested);
	useEffect(() => {
		if (requested) setEverRequested(true);
	}, [requested]);
	return everRequested ? <LaneOrchestratorDialog /> : null;
}

function LaneOrchestratorDialog() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const request = useUiStore((state) => state.laneOrchestratorRequest);
	// The request that mounted this dialog has not been shown yet.
	const lastNonce = useRef(0);
	const [projectId, setProjectId] = useState<string | null>(null);
	const [lane, setLane] = useState(NO_LANE);
	const [newTitle, setNewTitle] = useState("");
	// The item created for the current title, so a retry after a failed spawn reuses it.
	const createdRef = useRef<{ title: string; ref: string } | null>(null);
	const [pending, setPending] = useState(false);
	const [error, setError] = useState<string | null>(null);

	useEffect(() => {
		if (!request || request.nonce === lastNonce.current) return;
		lastNonce.current = request.nonce;
		if (projectId) return;
		setProjectId(request.projectId);
		setLane(NO_LANE);
		setNewTitle("");
		createdRef.current = null;
		setError(null);
	}, [request, projectId]);

	const lanes = useQuery({
		queryKey: projectId ? backlogListKey(projectId, "open") : ["backlog", "none"],
		queryFn: () => listBacklog(projectId ?? "", "open"),
		enabled: Boolean(projectId),
	});
	const choices = laneChoices(lanes.data ?? []);
	const workspaces = useWorkspaceQuery().data ?? [];
	const running = runningLaneRefs(workspaces, projectId);

	// A lane item this dialog created but never gave an orchestrator would sit in
	// the backlog as an empty lane; close it when the dialog moves on.
	const discardCreated = () => {
		const created = createdRef.current;
		createdRef.current = null;
		if (!created || !projectId) return;
		const id = projectId;
		void setBacklogState(id, created.ref, "dropped", "lane orchestrator was not started")
			.catch(() => undefined)
			.finally(() => void queryClient.invalidateQueries({ queryKey: backlogListKey(id, "open") }));
	};

	const start = async () => {
		if (!projectId || pending) return;
		setPending(true);
		setError(null);
		try {
			let laneRef = choices.find((item) => item.ref === lane)?.ref;
			if (lane === NEW_LANE) {
				const title = newTitle.trim();
				if (!title) return;
				// A new top-level backlog item is the lane.
				if (createdRef.current && createdRef.current.title !== title) discardCreated();
				if (createdRef.current?.title !== title) createdRef.current = { title, ref: (await createBacklogItem(projectId, { title })).ref };
				laneRef = createdRef.current.ref;
				void queryClient.invalidateQueries({ queryKey: backlogListKey(projectId, "open") });
			}
			// A retry that targets another lane leaves the item from the failed try empty.
			if (createdRef.current && createdRef.current.ref !== laneRef) discardCreated();
			const sessionId = await spawnOrchestrator(projectId, "lane_dialog", false, undefined, undefined, undefined, laneRef);
			createdRef.current = null;
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(undefined) });
			setProjectId(null);
			void navigate(sessionNavigateTarget(projectId, sessionId));
		} catch (err) {
			setError(apiErrorMessage(err, err instanceof Error ? err.message : String(err)));
		} finally {
			setPending(false);
		}
	};

	return (
		<Dialog.Root open={projectId !== null} onOpenChange={(open) => { if (!open && !pending) { discardCreated(); setProjectId(null); } }}>
			<Dialog.Portal>
				<Dialog.Overlay className="dialog-overlay data-[state=open]:animate-overlay-in" />
				<Dialog.Content
					aria-busy={pending}
					aria-describedby={undefined}
					className={`${settingsDialogContentClass} fixed left-1/2 top-1/2 w-dialog-orchestrator -translate-x-1/2 -translate-y-1/2 data-[state=open]:animate-modal-in`}
				>
					<Dialog.Close asChild>
						<button type="button" className="settings-dialog-close-button settings-close-button" disabled={pending} aria-label={t("laneOrchestrator.close")}>
							<X className="size-5" aria-hidden="true" />
						</button>
					</Dialog.Close>
					<div className={settingsDialogHeaderClass}>
						<Dialog.Title className="settings-dialog-title">{t("laneOrchestrator.title")}</Dialog.Title>
					</div>
					<div className={settingsDialogBodyClass}>
						<div className="flex flex-col gap-1.5">
							<Label htmlFor="lane-orchestrator-lane">{t("laneOrchestrator.laneField")}</Label>
							<Select value={lane} onValueChange={setLane} disabled={pending}>
								<SelectTrigger id="lane-orchestrator-lane" size="sm" className="w-full text-control">
									<SelectValue />
								</SelectTrigger>
								<SelectContent position="popper">
									<SelectItem value={NO_LANE}>{t("laneOrchestrator.noLane")}</SelectItem>
									<SelectItem value={NEW_LANE}>{t("laneOrchestrator.newLane")}</SelectItem>
									{choices.map((item) => (
										<SelectItem key={item.id} value={item.ref}>{running.has(item.ref) ? t("laneOrchestrator.running", { label: `${item.ref} ${item.title}` }) : `${item.ref} ${item.title}`}</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						{lane === NEW_LANE ? (
							<div className="flex flex-col gap-1.5">
								<Label htmlFor="lane-orchestrator-title">{t("laneOrchestrator.newTitleField")}</Label>
								<Input id="lane-orchestrator-title" autoFocus value={newTitle} disabled={pending} onChange={(event) => setNewTitle(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void start(); }} />
							</div>
						) : null}
						{lanes.isError ? <p role="alert" className="text-caption text-destructive">{t("laneOrchestrator.loadFailed")}</p> : null}
						{error ? <p role="alert" className="text-caption text-destructive">{error}</p> : null}
					</div>
					<div className={settingsDialogFooterClass}>
						<Button type="button" variant="footer-primary" disabled={pending || (lane === NEW_LANE && !newTitle.trim())} onClick={() => void start()}>
							{pending ? t("laneOrchestrator.starting") : t("laneOrchestrator.start")}
						</Button>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
