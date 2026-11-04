import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ app: { isPackaged: true } }));
vi.mock("./renderer-assets", () => ({ windowIconPath: () => "/icon.png" }));
vi.mock("../app-bootstrap", () => ({ APP_DISPLAY_NAME: "Tallpine" }));
vi.mock("../app-runtime-state", () => ({ NATIVE_WINDOW_BACKGROUND_DARK: "#000" }));

import { mainWindowOptions } from "./window-options";

// Characterization of the per-platform window chrome moved out of createWindowInternal (B297, P53).
describe("mainWindowOptions", () => {
	const original = process.platform;
	const setPlatform = (value: NodeJS.Platform) => Object.defineProperty(process, "platform", { value });
	afterEach(() => setPlatform(original));

	it("windows: frameless with a hidden native menu bar", () => {
		setPlatform("win32");
		expect(mainWindowOptions()).toMatchObject({ titleBarStyle: "hidden", autoHideMenuBar: true, show: false, minWidth: 960, minHeight: 640 });
	});

	it("linux: standard frame with an auto-hidden menu bar", () => {
		setPlatform("linux");
		const options = mainWindowOptions();
		expect(options.autoHideMenuBar).toBe(true);
		expect(options.titleBarStyle).toBeUndefined();
	});

	it("macOS: inset traffic lights on a transparent window", () => {
		setPlatform("darwin");
		expect(mainWindowOptions()).toMatchObject({ titleBarStyle: "hiddenInset", transparent: true, visualEffectState: "active", icon: "/icon.png" });
	});
});
