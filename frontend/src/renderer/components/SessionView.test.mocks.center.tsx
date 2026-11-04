import { type ReactNode } from "react";
import { vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";


vi.mock("./TerminalAgentControls", () => ({
	SessionTabMenu:({ switchControl }: { switchControl?: ReactNode }) => (
		<>
			{switchControl}
		</>
	),
}));

vi.mock("./chat/ReviewerChatSurface", () => ({
	ReviewerChatSurface: ({ reviewId }: { reviewId: string }) => (
		<div data-testid="reviewer-chat-surface">{reviewId}</div>
	),
}));

vi.mock("./CenterPane", () => ({
	CenterPane: ({
		agentInputDisabled,
		session,
		terminalGeneration,
		shellTerminals = [],
		onCloseShellTerminal,
		onSelectShellTerminal,
		onSelectSessionTerminal,
		onSelectReviewerTerminal,
		topbarActions,
		sessionTabAction,
		tabStripAction,
		workspaceTabs,
		workspaceTabActions,
		reviewerTerminal,
		reviewerChatContent,
		terminalTarget,
		auxiliaryTabOrder,
	}: {
		agentInputDisabled?: boolean;
		session?: WorkspaceSession;
		terminalGeneration?: string;
		shellTerminals?: Array<{ handleId: string; title: string }>;
		onCloseShellTerminal?: (handleId: string) => void;
		onSelectShellTerminal?: (handleId: string) => void;
		onSelectSessionTerminal?: () => void;
		onSelectReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
		topbarActions?: ReactNode;
		sessionTabAction?: ReactNode;
		tabStripAction?: ReactNode;
		workspaceTabs?: Array<{ key: string; content: ReactNode; onSelect: () => void }>;
		workspaceTabActions?: ReactNode;
		reviewerTerminal?: { handleId: string; harness: string };
		reviewerChatContent?: ReactNode;
		terminalTarget?: { kind: string; handleId?: string };
		auxiliaryTabOrder?: string[];
	}) => {
		return (
		<div data-testid="terminal-center" data-agent-input-disabled={agentInputDisabled ? "true" : "false"} data-terminal-generation={terminalGeneration ?? ""}>

			terminal center
			<div data-testid={`auxiliary-tab-order-tui-${session?.id ?? "none"}`}>
				{auxiliaryTabOrder?.join("|") ?? ""}
			</div>
			{topbarActions}
			{sessionTabAction}
			<div role="tablist">
				{workspaceTabs?.map((tab) => <div key={tab.key}>{tab.content}</div>)}
				{workspaceTabActions}
				{tabStripAction}
			</div>
			<div data-testid="terminal-target">
				{terminalTarget?.kind === "shell" ? terminalTarget.handleId : (terminalTarget?.kind ?? "worker")}
			</div>
			<div data-testid="session-tab">{session?.title ?? ""}</div>
			<div data-testid="reviewer-harness">{reviewerTerminal?.harness ?? ""}</div>
			{reviewerChatContent}
			{reviewerTerminal ? (
				<button type="button" onClick={() => onSelectReviewerTerminal?.(reviewerTerminal)}>
					select reviewer tab
				</button>
			) : null}
			<div data-testid="shell-tabs">{shellTerminals.map((s) => s.title).join(",")}</div>
			{shellTerminals.map((s) => (
				<button key={s.handleId} type="button" onClick={() => onSelectShellTerminal?.(s.handleId)}>
					select {s.title}
				</button>
			))}
			{shellTerminals.map((s) => (
				<button key={`close-${s.handleId}`} type="button" onClick={() => onCloseShellTerminal?.(s.handleId)}>
					close {s.title}
				</button>
			))}
			<button type="button" onClick={() => onSelectSessionTerminal?.()}>
				select agent tab
			</button>
		</div>
		);
	},
}));
