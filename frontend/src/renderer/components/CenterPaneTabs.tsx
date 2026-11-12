import { Pencil, RotateCcw } from "lucide-react";
import { Reorder, useDragControls } from "motion/react";
import { type PointerEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { MAX_SESSION_DISPLAY_NAME_LEN, useSessionRename } from "../hooks/useSessionRename";
import { useTruncatedText } from "../hooks/useTruncatedText";
import type { ShellTerminal } from "../hooks/useShellTerminals";
import { sessionActivityLabel } from "../lib/session-presentation";
import { agentLabel } from "../lib/agent-options";
import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import { TerminalTabFrame } from "./TerminalTabFrame";
import { useResetOrchestrator } from "./ResetOrchestrator";
import { SessionCopyContextItems } from "./SessionCopyMenuItems";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "./ui/context-menu";
import type { CenterPaneWorkspaceTab } from "./CenterPane";

export type AuxiliaryTab =
	| { key: string; kind: "reviewer"; terminal: { handleId: string; harness: string } }
	| { key: string; kind: "reviewer-chat"; terminal: { reviewId: string; harness: string } }
	| { key: string; kind: "shell"; terminal: ShellTerminal }
	| { key: string; kind: "workspace"; tab: CenterPaneWorkspaceTab };

export function DraggableWorkspaceTab({ children, value }: { children: ReactNode; value: string }) {
	const dragControls = useDragControls();
	const startDrag = (event: PointerEvent<HTMLDivElement>) => {
		if ((event.target as HTMLElement).closest("[data-terminal-tab-action],input,a")) return;
		dragControls.start(event);
	};

	return (
		<Reorder.Item
			as="div"
			className="flex shrink-0 self-stretch touch-pan-y"
			data-terminal-tab-key={value}
			drag="x"
			dragControls={dragControls}
			dragListener={false}
			onPointerDown={startDrag}
			// Reorder needs layout measurement, but tabs should snap into place
			// when a sibling opens or closes rather than slide.
			transition={{ layout: { duration: 0 } }}
			value={value}
		>
			{children}
		</Reorder.Item>
	);
}

type SessionPaneTabProps = {
	label: string;
	isActive: boolean;
	appearance?: "primary" | "connected";
	onSelect?: () => void;
	onRenamed?: () => void | Promise<void>;
	session?: WorkspaceSession;
	icon?: ReactNode;
	title?: string;
	/** Session-scoped controls (interface switch, handoff) beside the tab label. */
	tabAction?: ReactNode;
	/** Only while switching: reserve action width so the spinner does not cover the title. */
	tabActionWide?: boolean;
};

// Shared tab chrome: the open tab is highlighted with the same rounded
// background as the inspector rail tabs (Summary · Reviews · Browser), and
// the full label only becomes the hover tooltip when the tab strip is
// crowded enough to truncate it.
export function SessionPaneTab({
	label,
	isActive,
	appearance = "primary",
	onSelect,
	onRenamed,
	session,
	icon,
	title,
	tabAction,
	tabActionWide = false,
}: SessionPaneTabProps) {
	const { t, i18n } = useTranslation();
	const { ref, isTruncated } = useTruncatedText<HTMLButtonElement>(label);
	const activityLabel = session ? sessionActivityLabel(session, t, i18n.language) : undefined;
	const providerLabel = session ? agentLabel(session.provider) : undefined;
	const tabIcon = session ? <AgentAvatar className="size-terminal-agent-icon" decorative provider={session.provider} /> : icon;
	const connected = appearance === "connected";
	// A session object supplies the tab presentation; refresh wiring explicitly
	// opts the owning surface into rename so shared preview tabs cannot
	// persist a title without updating their query cache. The project
	// orchestrator's tab shows its role, not a name, so it is never renamable.
	const renameSession = onRenamed && session && !isOrchestratorSession(session) ? session : undefined;
	const rename = useSessionRename(renameSession, onRenamed);
	const resettable = session && isOrchestratorSession(session) && !session.isTerminated ? session : undefined;
	const resetOrchestrator = useResetOrchestrator(resettable?.id ?? "", resettable?.hostId);
	const editingContent = renameSession && rename.isEditing ? (
		<div className="flex h-full min-w-0 flex-1 items-center gap-2 px-2">
			{tabIcon}
			<input
				aria-label={t("shell.renameSession", { title: renameSession.title })}
				autoFocus
				className="min-w-0 flex-1 rounded-xs border border-accent bg-background px-1 text-control text-foreground outline-none ring-1 ring-accent"
				maxLength={MAX_SESSION_DISPLAY_NAME_LEN}
				onBlur={() => void rename.commit()}
				onChange={(event) => rename.setDraft(event.target.value)}
				onFocus={(event) => event.currentTarget.select()}
				onKeyDown={(event) => {
					if (event.key === "Enter") {
						event.preventDefault();
						event.currentTarget.blur();
					} else if (event.key === "Escape") {
						event.preventDefault();
						rename.cancel();
					}
				}}
				value={rename.draft}
			/>
		</div>
	) : undefined;
	const tabFrame = (
		<TerminalTabFrame
			action={!rename.isEditing && tabAction ? <span data-testid="session-tab-action">{tabAction}</span> : undefined}
			// Idle tabs keep the overlay ⋮ slot (title looks like before). Only while
			// switching do we reserve width so the spinner cannot cover the label.
			actionLayout={tabActionWide ? "inline" : "overlay"}
			active={isActive}
			buttonProps={{
				"aria-current": isActive,
				"aria-keyshortcuts": renameSession ? "F2" : undefined,
				"aria-label": [label, providerLabel, activityLabel].filter(Boolean).join(" · "),
				"aria-selected": isActive,
				onClick: (event) => {
					if (event.detail > 1) return;
					onSelect?.();
				},
				onDoubleClick: renameSession
					? (event) => {
							event.preventDefault();
							rename.begin();
						}
					: undefined,
				onKeyDown: renameSession
					? (event) => {
							if (event.key !== "F2") return;
							event.preventDefault();
							rename.begin();
						}
					: undefined,
				role: "tab",
				tabIndex: isActive ? 0 : -1,
				title: title ?? (isTruncated ? label : t("terminal.sessionAria")),
				type: "button",
			}}
			buttonRef={ref}
			className="w-shell-tab-connected min-w-shell-tab-min"
			data-terminal-role={connected ? undefined : "primary"}
			editingContent={editingContent}
		>
			{tabIcon}
			<span className="truncate">{label}</span>
		</TerminalTabFrame>
	);
	if (resettable && !rename.isEditing) {
		return (
			<>
				<ContextMenu>
					<ContextMenuTrigger asChild>
						<span className="contents">{tabFrame}</span>
					</ContextMenuTrigger>
					<ContextMenuContent className="min-w-44">
						<ContextMenuItem onSelect={resetOrchestrator.request}>
							<RotateCcw aria-hidden="true" />
							{t("orchestratorHandoff.reset")}
						</ContextMenuItem>
						<SessionCopyContextItems projectId={resettable.workspaceId} sessionId={resettable.id} />
					</ContextMenuContent>
				</ContextMenu>
				{resetOrchestrator.dialog}
			</>
		);
	}
	if (!renameSession || rename.isEditing) return tabFrame;
	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>
				<span className="contents">{tabFrame}</span>
			</ContextMenuTrigger>
			<ContextMenuContent className="min-w-44">
				<ContextMenuItem aria-label={t("shell.renameSession", { title: renameSession.title })} onSelect={rename.begin}>
					<Pencil aria-hidden="true" />
					{t("shell.rename")}
				</ContextMenuItem>
				<SessionCopyContextItems projectId={renameSession.workspaceId} sessionId={renameSession.id} />
			</ContextMenuContent>
		</ContextMenu>
	);
}
