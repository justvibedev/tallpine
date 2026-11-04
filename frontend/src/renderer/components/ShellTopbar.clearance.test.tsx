import { readFileSync } from "node:fs";
import path from "node:path";
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";

const platform = vi.hoisted(() => ({ mac: false, linux: false, fullScreen: false, narrow: false }));
vi.mock("@/hooks/use-mobile", () => ({ useIsMobile: () => platform.narrow }));
vi.mock("../lib/platform", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/platform")>()),
	isMacPlatform: () => platform.mac,
	isLinuxPlatform: () => platform.linux,
}));
vi.mock("../hooks/useWindowFullScreen", () => ({ useWindowFullScreen: () => platform.fullScreen }));

const { useTopbarPaddingLeft } = await import("./ShellTopbar");

const tokens = readFileSync(path.resolve(process.cwd(), "src/styles/tokens.css"), "utf8");
const px = (name: string) => {
	const match = tokens.match(new RegExp(`${name}:\\s*(?:calc\\()?\\s*(\\d+)px`));
	if (!match) throw new Error(`token ${name} is not a plain px value`);
	return Number(match[1]);
};

// The JS twin of the CSS titlebar clearances: they must agree with the tokens and the rail.
const RAIL = px("--size-sidebar-icon");
const RAIL_EXPANDED = px("--size-rail-expanded");
const CLUSTER = 2 * px("--size-control-md") + 4;
const GAP = px("--size-titlebar-content-gap");

describe("useTopbarPaddingLeft clearances", () => {
	beforeEach(() => {
		platform.mac = false;
		platform.linux = false;
		platform.fullScreen = false;
		platform.narrow = false;
		useUiStore.setState({ isSidebarOpen: false, isNarrowShell: false, isRailExpanded: false });
	});

	it("derives the cluster width from two arrows, not three buttons", () => {
		expect(tokens).toContain("--size-titlebar-cluster-width: calc(2 * var(--size-control-md) + 4px);");
		expect(CLUSTER).toBe(60);
	});

	it("macOS: cluster left + cluster + gap - mac panel inset - rail", () => {
		platform.mac = true;
		const expected = px("--size-titlebar-cluster-left") + CLUSTER + GAP - px("--size-center-panel-inset-mac") - RAIL;
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(expected);
	});

	it("macOS fullscreen: space-2 + cluster + gap - rail", () => {
		platform.mac = true;
		platform.fullScreen = true;
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(8 + CLUSTER + GAP - RAIL);
	});

	it("Linux: cluster left + cluster + gap - panel inline inset - rail", () => {
		platform.linux = true;
		const expected = px("--size-titlebar-cluster-left-linux") + CLUSTER + GAP - px("--size-center-panel-inline-inset") - RAIL;
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(expected);
	});

	it("follows the labelled rail: the wider rail leaves less to clear, never below the default gutter", () => {
		useUiStore.setState({ isRailExpanded: true });
		platform.mac = true;
		const mac = px("--size-titlebar-cluster-left") + CLUSTER + GAP - px("--size-center-panel-inset-mac");
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(Math.max(18, mac - RAIL_EXPANDED));
		platform.fullScreen = true;
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(Math.max(18, 8 + CLUSTER + GAP - RAIL_EXPANDED));
		platform.narrow = true;
		platform.fullScreen = false;
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(mac);
	});

	it("keeps the default padding while the Sessions panel occupies layout", () => {
		platform.mac = true;
		useUiStore.setState({ isSidebarOpen: true });
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(18);
	});

	it("under 768px the rail is hidden, so the full reserve applies (no rail subtracted)", () => {
		platform.narrow = true;
		platform.mac = true;
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(98 + RAIL);
		platform.fullScreen = true;
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(32 + RAIL);
		platform.mac = false;
		platform.linux = true;
		expect(renderHook(() => useTopbarPaddingLeft()).result.current).toBe(14 + RAIL);
	});
});
