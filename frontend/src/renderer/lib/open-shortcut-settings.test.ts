import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { openShortcutSettings } from "./open-shortcut-settings";

describe("openShortcutSettings", () => {
	beforeEach(() => useUiStore.setState({ settingsModal: null }));

	it("closes the help and opens Settings > Keyboard shortcuts", () => {
		const closeHelp = vi.fn();
		openShortcutSettings(closeHelp);
		expect(closeHelp).toHaveBeenCalledOnce();
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "shortcuts" });
	});
});
