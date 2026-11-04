import { memo, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { Reorder, useDragControls } from "motion/react";
import { useTranslation } from "react-i18next";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import type { TerminalTarget } from "../../types/terminal";
import { ShellTerminalTab } from "../ShellTerminalTab";


export type ReviewerTerminalTarget = Extract<TerminalTarget, { kind: "reviewer" }>;

export type ShellTerminalTarget = Extract<TerminalTarget, { kind: "shell" }>;

export type WorkspaceTab = { key: string; content: ReactNode; onSelect: () => void; onClose?: () => void };

export type ChatAuxiliaryTab =
	| { key: string; kind: "reviewer"; terminal: { handleId: string; harness: string } }
	| { key: string; kind: "reviewer-chat"; terminal: { reviewId: string; harness: string } }
	| { key: string; kind: "shell"; terminal: ShellTerminal }
	| { key: string; kind: "workspace"; tab: WorkspaceTab };

export function DraggableChatTab({ children, value }: { children: ReactNode; value: string }) {
	const dragControls = useDragControls();
	return (
		<Reorder.Item
			as="div"
			className="flex shrink-0 self-stretch touch-pan-y"
			data-terminal-tab-key={value}
			drag="x"
			dragControls={dragControls}
			dragListener={false}
			onPointerDown={(event: ReactPointerEvent<HTMLDivElement>) => {
				if ((event.target as HTMLElement).closest("[data-terminal-tab-action],input,a")) return;
				dragControls.start(event);
			}}
			// Reorder needs layout measurement, but tabs should snap into place
			// when a sibling opens or closes rather than slide.
			transition={{ layout: { duration: 0 } }}
			value={value}
		>
			{children}
		</Reorder.Item>
	);
}

// The tab strip re-renders with every session view update. A shell tab only
// changes with its shell or selection, so it skips the rest; with a dozen
// tabs this is most of the strip's render work.
export const ShellTerminalTabEntry = memo(function ShellTerminalTabEntry({
	tabKey,
	shell,
	isActive,
	onSelect,
	onClose,
	onRename,
}: {
	tabKey: string;
	shell: ShellTerminal;
	isActive: boolean;
	onSelect: (handleId: string) => void;
	onClose: (handleId: string) => void;
	onRename?: (handleId: string, title: string) => void;
}) {
	return (
		<DraggableChatTab value={tabKey}>
			<ShellTerminalTab
				appearance="connected"
				isActive={isActive}
				onClose={() => onClose(shell.handleId)}
				onRename={onRename ? (title) => onRename(shell.handleId, title) : undefined}
				onSelect={() => onSelect(shell.handleId)}
				shell={shell}
			/>
		</DraggableChatTab>
	);
});

export function OfflineRemoteTerminal() {
	const { t } = useTranslation();
	return <div className="flex h-full items-center justify-center text-sm text-muted-foreground" role="status">{t("remote.hostOffline")}</div>;
}
