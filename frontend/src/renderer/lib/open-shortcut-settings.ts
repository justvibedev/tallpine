import { useUiStore } from "../stores/ui-store";

/** "Customize" in the shortcut help: close the help and open Settings > Keyboard shortcuts. */
export function openShortcutSettings(closeHelp: () => void): void {
	closeHelp();
	useUiStore.getState().openGlobalSettings("shortcuts");
}
