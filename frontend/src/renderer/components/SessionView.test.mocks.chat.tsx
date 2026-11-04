import { type ReactNode } from "react";
import { vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";


const chatSurfaceRenders = vi.hoisted(() => [] as string[]);

const chatSurfaceTransitionRenders = vi.hoisted(() => [] as boolean[]);

const chatSurfaceWorkState = vi.hoisted(() => ({
	controllerBusy: false,
	hasRunningTurn: false,
	queuedTurnCount: 0,
}));

vi.mock("./chat/SessionChatSurface", async () => {
	const { memo } = await vi.importActual<typeof import("react")>("react");
	return { SessionChatSurface: memo(({
		session,
		onOpenShell,
		onOpenFile,
		headerActions,
		sessionTabAction,
		interfaceSwitch,
		tabStripAction,
		reviewerTerminal,
		onOpenReviewerTerminal,
		reviewerTarget,
		onSelectChat,
		controllerTransitioning,
		agentResuming,
		shellTerminals = [],
		shellTarget,
		onSelectShellTerminal,
		onCloseShellTerminal,
		workspaceTabs,
		workspaceTabActions,
		newWorkDisabled,
		onConversationWorkChange,
		auxiliaryTabOrder,
		onAuxiliaryTabOrderChange,
	}: {
		session: WorkspaceSession;
		onOpenShell?: () => void;
		onOpenFile?: (path: string, line?: number) => void;
		headerActions?: ReactNode;
		sessionTabAction?: ReactNode;
		interfaceSwitch?: { control: ReactNode; errorNotice?: ReactNode };
		tabStripAction?: ReactNode;
		reviewerTerminal?: { handleId: string; harness: string };
		onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
		reviewerTarget?: { kind: "reviewer"; handleId: string; harness: string; sessionId: string };
		onSelectChat?: () => void;
		agentResuming?: boolean;
		controllerTransitioning?: boolean;
		shellTerminals?: Array<{ handleId: string; title: string }>;
		shellTarget?: { kind: "shell"; handleId: string };
		onSelectShellTerminal?: (handleId: string) => void;
		onCloseShellTerminal?: (handleId: string) => void;
		workspaceTabs?: Array<{ key: string; content: ReactNode; onSelect: () => void; onClose?: () => void }>;
		workspaceTabActions?: ReactNode;
		newWorkDisabled?: boolean;
		onConversationWorkChange?: (state: typeof chatSurfaceWorkState) => void;
		auxiliaryTabOrder?: string[];
		onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	}) => {
		chatSurfaceRenders.push(session.id);
		chatSurfaceTransitionRenders.push(Boolean(controllerTransitioning || agentResuming));
		return (
		<div
			data-testid="chat-surface"
			data-transitioning={controllerTransitioning || agentResuming ? "true" : "false"}
			data-new-work-disabled={newWorkDisabled ? "true" : "false"}
		>
			chat surface
			<div data-testid={`auxiliary-tab-order-${session.id}`}>
				{auxiliaryTabOrder?.join("|") ?? ""}
			</div>
			<button
				type="button"
				onClick={() => onAuxiliaryTabOrderChange?.(["sh-a", "file:src/panel.tsx"])}
			>
				reorder auxiliary tabs
			</button>
			<button
				type="button"
				onClick={() =>
					onAuxiliaryTabOrderChange?.(["sh-a", "reviewer:review-sess-1", "file:src/panel.tsx"])
				}
			>
				reorder reviewer tab
			</button>
			<button
				type="button"
				onClick={() => onAuxiliaryTabOrderChange?.(["file:src/panel.tsx", "sh-a"])}
			>
				reorder visible tabs
			</button>
			{headerActions}
			{sessionTabAction}
			{interfaceSwitch?.errorNotice}
			<div role="tablist">
				{workspaceTabs?.map((tab) => (
					<div key={tab.key}>
						{tab.content}
						{tab.onClose ? <button onClick={tab.onClose} type="button">{`close ${tab.key}`}</button> : null}
					</div>
				))}
				{workspaceTabActions}
				{tabStripAction}
			</div>
			{onOpenFile ? (
				<>
					<button type="button" onClick={() => onOpenFile("notes.txt", 120)}>
						open chat basename
					</button>
					<button type="button" onClick={() => onOpenFile("/Users/me/worktrees/s/src/App.tsx", 7)}>
						open chat absolute path
					</button>
				</>
			) : null}
			{reviewerTerminal ? (
				<button type="button" onClick={() => onOpenReviewerTerminal?.(reviewerTerminal)}>
					Reviewer
				</button>
			) : null}
			{reviewerTarget ? (
				<div data-testid="terminal-target">reviewer</div>
			) : null}
			{reviewerTarget ? (
				<button type="button" onClick={onSelectChat}>
					select chat tab
				</button>
			) : null}
			<div data-testid="shell-tabs">
				{shellTerminals.map((shell) => (
					<div key={shell.handleId}>
						<button onClick={() => onSelectShellTerminal?.(shell.handleId)} type="button">
							{shell.title}
						</button>
						<button onClick={() => onCloseShellTerminal?.(shell.handleId)} type="button">
							close {shell.title}
						</button>
					</div>
				))}
			</div>
			{shellTarget ? <div data-testid="terminal-target">shell</div> : null}
			{shellTarget ? (
				<button type="button" onClick={onSelectChat}>
					select chat tab
				</button>
			) : null}
			<button type="button" onClick={onOpenShell}>
				open shell from chat
			</button>
			<button type="button" onClick={() => onConversationWorkChange?.({ ...chatSurfaceWorkState })}>
				report chat work
			</button>
		</div>
		);
	}),
	};
});


// The session tab's menu is stood in for by the switch control it wraps.

export { chatSurfaceRenders, chatSurfaceTransitionRenders, chatSurfaceWorkState };
