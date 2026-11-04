import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { NarrowPanelBackdrop } from "./NarrowPanelBackdrop";
import { useNarrowShell } from "./useNarrowShell";

const { location } = vi.hoisted(() => ({ location: { pathname: "/sessions" } }));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) => select({ location }),
}));

type Listener = () => void;
const media = { matches: false, listeners: new Set<Listener>() };

function Harness() {
	useNarrowShell();
	return <NarrowPanelBackdrop />;
}

beforeEach(() => {
	media.matches = false;
	media.listeners.clear();
	location.pathname = "/sessions";
	window.matchMedia = ((query: string) => ({
		get matches() {
			return media.matches;
		},
		media: query,
		addEventListener: (_: string, listener: Listener) => media.listeners.add(listener),
		removeEventListener: (_: string, listener: Listener) => media.listeners.delete(listener),
	})) as unknown as typeof window.matchMedia;
	useUiStore.setState({ isSidebarOpen: true, isNarrowShell: false, isNarrowPanelOpen: false });
});

describe("useNarrowShell", () => {
	it("tracks the 900px breakpoint", () => {
		media.matches = true;
		render(<Harness />);
		expect(useUiStore.getState().isNarrowShell).toBe(true);

		act(() => {
			media.matches = false;
			media.listeners.forEach((listener) => listener());
		});
		expect(useUiStore.getState().isNarrowShell).toBe(false);
	});

	it("shows a click-away backdrop only while the narrow panel is open, and a click closes it", async () => {
		media.matches = true;
		render(<Harness />);
		expect(screen.queryByTestId("narrow-panel-backdrop")).toBeNull();

		act(() => useUiStore.getState().toggleSidebar());
		await userEvent.setup().click(screen.getByTestId("narrow-panel-backdrop"));
		expect(useUiStore.getState().isNarrowPanelOpen).toBe(false);
		expect(screen.queryByTestId("narrow-panel-backdrop")).toBeNull();
	});

	it("closes the floating panel when the route changes", () => {
		media.matches = true;
		const { rerender } = render(<Harness />);
		act(() => useUiStore.getState().toggleSidebar());
		expect(useUiStore.getState().isNarrowPanelOpen).toBe(true);

		location.pathname = "/projects/p1";
		rerender(<Harness />);
		expect(useUiStore.getState().isNarrowPanelOpen).toBe(false);
	});

	it("never shows the backdrop in a wide window", () => {
		render(<Harness />);
		act(() => useUiStore.setState({ isNarrowPanelOpen: true }));
		expect(screen.queryByTestId("narrow-panel-backdrop")).toBeNull();
	});
});
