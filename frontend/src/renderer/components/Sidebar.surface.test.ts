import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import * as sidebarModule from "./Sidebar";

const componentsDir = path.resolve(process.cwd(), "src/renderer/components");
const MAX_LINES = 400;

describe("Sidebar module surface", () => {
	it("keeps the exports other modules import from ./Sidebar", () => {
		expect(typeof sidebarModule.Sidebar).toBe("function");
		expect(typeof sidebarModule.SettingsSidebarNav).toBe("function");
		expect(typeof sidebarModule.resolveNextNavigationAfterSessionKill).toBe("function");
		expect(sidebarModule.sessionDndId("p1")).toBe("sidebar-sessions-p1");
		expect([sidebarModule.SIDEBAR_DEFAULT_WIDTH, sidebarModule.SIDEBAR_MIN_WIDTH, sidebarModule.SIDEBAR_MAX_WIDTH]).toEqual([240, 200, 420]);
	});

	it("keeps Sidebar.tsx and every sidebar/ part within the file-size rule", () => {
		const files = [
			path.join(componentsDir, "Sidebar.tsx"),
			...readdirSync(path.join(componentsDir, "sidebar")).map((name) => path.join(componentsDir, "sidebar", name)),
		];
		const tooLong = files
			.map((file) => ({ file: path.relative(componentsDir, file), lines: readFileSync(file, "utf8").split("\n").length }))
			.filter(({ lines }) => lines > MAX_LINES);
		expect(tooLong).toEqual([]);
	});
});
