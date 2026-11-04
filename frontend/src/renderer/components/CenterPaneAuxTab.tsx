import { useTranslation } from "react-i18next";
import { AgentAvatar } from "./AgentAvatar";
import { ShellTerminalTab } from "./ShellTerminalTab";
import type { TerminalTarget } from "../types/terminal";
import type { CenterPaneProps } from "./CenterPane";
import { SessionPaneTab, type AuxiliaryTab } from "./CenterPaneTabs";

type AuxiliaryTabItemProps = Pick<
	CenterPaneProps,
	"onSelectReviewerTerminal" | "onSelectReviewerChat" | "onSelectShellTerminal" | "onCloseShellTerminal" | "onRenameShellTerminal" | "workspaceActiveTabKey"
> & {
	tab: AuxiliaryTab;
	target: TerminalTarget;
	reviewerChatSelected: boolean;
};

// One reviewer, reviewer-chat, shell or workspace tab in the strip.
export function AuxiliaryTabItem({ tab, target, workspaceActiveTabKey, reviewerChatSelected, onSelectReviewerTerminal, onSelectReviewerChat, onSelectShellTerminal, onCloseShellTerminal, onRenameShellTerminal }: AuxiliaryTabItemProps) {
	const { t } = useTranslation();
	return (
												tab.kind === "reviewer" || tab.kind === "reviewer-chat" ? (
													<SessionPaneTab
														appearance="connected"
														icon={
															<AgentAvatar
																provider={tab.terminal.harness}
																className="size-terminal-agent-icon"
																decorative
															/>
														}
																		isActive={tab.kind === "reviewer" ? target.kind === "reviewer" && !workspaceActiveTabKey : reviewerChatSelected && !workspaceActiveTabKey}
														label={t("terminal.reviewer")}
														onSelect={() =>
															tab.kind === "reviewer"
																? onSelectReviewerTerminal?.(tab.terminal)
																: onSelectReviewerChat?.(tab.terminal)
														}
														title={tab.terminal.harness}
													/>
												) : tab.kind === "shell" ? (
													<ShellTerminalTab
														appearance="connected"
														isActive={target.kind === "shell" && target.handleId === tab.terminal.handleId && !workspaceActiveTabKey}
														onClose={() => onCloseShellTerminal?.(tab.terminal.handleId)}
														onRename={
															onRenameShellTerminal
																? (title) => onRenameShellTerminal(tab.terminal.handleId, title)
																: undefined
														}
														onSelect={() => onSelectShellTerminal?.(tab.terminal.handleId)}
														shell={tab.terminal}
													/>
												) : (
													tab.tab.content
												)
	);
}
