// Shared mock setup for the ChatWorkspace*.test.tsx files. Importing this module
// registers the vi.mock calls below for the importing test file.
import { vi } from "vitest";
import { setApiBaseUrl } from "../../lib/api-client";
import { appI18n } from "../../i18n";
import { useUiStore } from "../../stores/ui-store";

const renameSessionMock = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
export const writeText = vi.fn(async (_text: string) => undefined);
export const menuAction = vi.fn(async (_action: string) => undefined);
export const previousTabListeners = new Set<() => void>();
export const nextTabListeners = new Set<() => void>();
export const closeShellTerminalListeners = new Set<() => void>();
export const closeShellTerminalShortcutStates: boolean[] = [];
const terminalPaneState = vi.hoisted(() => ({
	props: undefined as TerminalPaneTestProps | undefined,
}));

export type TerminalPaneTestProps = {
	fontSize?: number;
	focusRequested?: boolean;
	isFullscreen?: boolean;
	onChangeFontSize?: (delta: number) => void;
	onToggleFullscreen?: () => Promise<void> | void;
};

vi.mock("../../lib/rename-session", () => ({ renameSession: renameSessionMock }));

vi.mock("../../lib/bridge", () => ({
	tallpineBridge: {
		app: {
			onPreviousTabShortcut: (listener: () => void) => {
				previousTabListeners.add(listener);
				return () => previousTabListeners.delete(listener);
			},
			onNextTabShortcut: (listener: () => void) => {
				nextTabListeners.add(listener);
				return () => nextTabListeners.delete(listener);
			},
			onCloseShellTerminalShortcut: (listener: () => void) => {
				closeShellTerminalListeners.add(listener);
				return () => closeShellTerminalListeners.delete(listener);
			},
			setCloseShellTerminalShortcutEnabled: (enabled: boolean) => {
				closeShellTerminalShortcutStates.push(enabled);
			},
		},
		clipboard: { writeText: (text: string) => writeText(text) },
		menu: { action: (action: string) => menuAction(action) },
	},
}));

vi.mock("../TerminalPane", () => ({
	TerminalPane: (props: NonNullable<typeof terminalPaneState.props>) => {
		terminalPaneState.props = props;
		return <div data-testid="terminal-pane" />;
	},
}));

vi.mock("../../lib/platform", () => ({
	isMacPlatform: () => true,
	isLinuxPlatform: () => false,
	isWindowsPlatform: () => false,
}));

export { renameSessionMock, terminalPaneState };

export function resetChatWorkspaceMocks() {
	writeText.mockClear();
	menuAction.mockClear();
	previousTabListeners.clear();
	nextTabListeners.clear();
	closeShellTerminalListeners.clear();
	closeShellTerminalShortcutStates.length = 0;
	terminalPaneState.props = undefined;
	renameSessionMock.mockReset().mockResolvedValue(undefined);
	window.localStorage.clear();
	setApiBaseUrl("http://127.0.0.1:3101");
	useUiStore.setState({ isSidebarOpen: true, inspectorSessions: {} });
}

export async function cleanupChatWorkspaceTest() {
	setApiBaseUrl(null);
	await appI18n.changeLanguage("en");
}
