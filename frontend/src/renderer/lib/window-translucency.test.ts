import { afterEach, describe, expect, it, vi } from "vitest";
import { tallpineBridge } from "./bridge";
import { isMacPlatform } from "./platform";
import {
	applyWindowTranslucency,
	legacyDeveloperModeStorageKey,
	readStoredWindowTranslucency,
	windowTranslucencyStorageKey,
} from "./window-translucency";

vi.mock("./platform", () => ({ isMacPlatform: vi.fn(() => true) }));

describe("window translucency", () => {
	afterEach(() => {
		window.localStorage.clear();
		delete document.documentElement.dataset.windowTranslucent;
		vi.mocked(isMacPlatform).mockReturnValue(true);
		vi.restoreAllMocks();
	});

	it("restores the Appearance preference independently of the retired Developer mode key on macOS", () => {
		expect(readStoredWindowTranslucency()).toBe(false);
		window.localStorage.setItem(windowTranslucencyStorageKey, "true");
		expect(readStoredWindowTranslucency()).toBe(true);

		window.localStorage.setItem(legacyDeveloperModeStorageKey, "true");
		expect(readStoredWindowTranslucency()).toBe(true);

		window.localStorage.setItem(legacyDeveloperModeStorageKey, "false");
		expect(readStoredWindowTranslucency()).toBe(true);

		window.localStorage.setItem(windowTranslucencyStorageKey, "false");
		expect(readStoredWindowTranslucency()).toBe(false);

		window.localStorage.setItem(windowTranslucencyStorageKey, "true");
		vi.mocked(isMacPlatform).mockReturnValue(false);
		expect(readStoredWindowTranslucency()).toBe(false);
	});

	it("marks the document and asks main for vibrancy every time", () => {
		const setWindowTranslucent = vi.spyOn(tallpineBridge.theme, "setWindowTranslucent");

		applyWindowTranslucency(true);
		applyWindowTranslucency(true);
		expect(document.documentElement.dataset.windowTranslucent).toBe("true");
		expect(setWindowTranslucent).toHaveBeenNthCalledWith(1, true);
		expect(setWindowTranslucent).toHaveBeenNthCalledWith(2, true);

		applyWindowTranslucency(false);
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		expect(setWindowTranslucent).toHaveBeenLastCalledWith(false);
	});
});
