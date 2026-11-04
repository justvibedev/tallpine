import type { TFunction } from "i18next";
import type { ShellTerminal } from "../hooks/useShellTerminals";
import type { TerminalTarget } from "../types/terminal";
import { isOrchestratorSession } from "../types/workspace";
import type { CenterPaneProps } from "./CenterPane";

// The primary tab's label and the active terminal's name for the panel's aria-label.
export function centerPaneLabels({ t, session, target, reviewerChat, reviewerChatSelected, shellTerminals }: {
	t: TFunction;
	session: CenterPaneProps["session"];
	target: TerminalTarget;
	reviewerChat: CenterPaneProps["reviewerChat"];
	reviewerChatSelected: boolean;
	shellTerminals: ShellTerminal[];
}) {
	const sessionTabLabel = session
		? isOrchestratorSession(session)
			? t("shell.orchestrator")
			: session.title
		: t("terminal.noSession");
	const activeTerminalLabel =
		reviewerChatSelected && reviewerChat
			? `${t("terminal.reviewer")} · ${reviewerChat.harness}`
			: target.kind === "shell"
			? (shellTerminals.find((shell) => shell.handleId === target.handleId)?.title ?? target.title)
			: target.kind === "reviewer"
				? `${t("terminal.reviewer")} · ${target.harness}`
				: (session?.title ?? sessionTabLabel);
	return { sessionTabLabel, activeTerminalLabel };
}
