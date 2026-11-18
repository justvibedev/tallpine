import { useNavigate } from "@tanstack/react-router";
import { SquareTerminal } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { selectedShellHandle, shellTerminalsQueryOptions, useOpenShellTerminal, useShellTerminals, type ShellTerminal } from "../hooks/useShellTerminals";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { TopbarButton } from "./TopbarButton";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * The project's standalone shells: no agent session behind them, rooted in the
 * project. Without a project in scope, every standalone shell counts.
 */
export function projectTerminals(shells: ShellTerminal[], projectId: string | undefined): ShellTerminal[] {
	return shells.filter((shell) => !shell.sessionId && (!projectId || shell.projectId === projectId));
}

/**
 * Opens the terminals view for a project. With no shell in the project yet it
 * creates one in the project root; otherwise it shows the existing tabs, keeping
 * the selected tab when it already belongs to the project.
 */
export function useOpenProjectTerminal(projectId: string | undefined) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const shells = useShellTerminals().data;
	const openShellTerminal = useOpenShellTerminal();
	const setActiveShellTerminal = useUiStore((state) => state.setActiveShellTerminal);
	const scope = projectId === STANDALONE_WORKSPACE_ID ? undefined : projectId;
	return async () => {
		// Before the list first loads, ask the daemon rather than create a duplicate.
		const loaded = shells ?? (await queryClient.fetchQuery(shellTerminalsQueryOptions).catch(() => []));
		const existing = projectTerminals(loaded, scope);
		const activeId = useUiStore.getState().activeShellTerminalHandleId;
		const selectedId = selectedShellHandle(activeId);
		const target = existing.find((shell) => shell.handleId === selectedId) ?? existing[0] ?? openShellTerminal.open({ projectId: scope });
		setActiveShellTerminal(target.handleId);
		void navigate({ to: "/terminals" });
	};
}

/** Top-bar entry to the project's terminals (local daemon only). */
export function TopbarTerminalButton({ projectId, style }: { projectId: string | undefined; style?: CSSProperties }) {
	const { t } = useTranslation();
	const openTerminal = useOpenProjectTerminal(projectId);
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex" style={style}>
					<TopbarButton aria-label={t("inspector.openTerminal")} data-testid="topbar-open-terminal" onClick={() => void openTerminal()} variant="icon">
						<SquareTerminal className="size-icon-md" aria-hidden="true" />
					</TopbarButton>
				</span>
			</TooltipTrigger>
			<TooltipContent side="bottom">{t("inspector.openTerminal")}</TooltipContent>
		</Tooltip>
	);
}
