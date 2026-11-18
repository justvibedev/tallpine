// The shared setup's module mocks must load before SessionView.
import { inspectorOpen, render } from "./SessionView.test.setup";
import { resetSessionViewState } from "./SessionView.test.reset";
import { act, fireEvent, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { SessionView } from "./SessionView";

// The session's one sidebar toggle, pinned at the top bar's right edge.
describe("SessionView sidebar toggle", () => {
	beforeEach(() => {
		resetSessionViewState();
	});

	it("keeps one sidebar toggle at the far right of the top bar, open or closed, after the panel switcher", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);
		const pinned = screen.getByTestId("session-pinned-actions");
		expect(screen.getAllByTestId("sidebar-toggle")).toHaveLength(1);
		const toggle = within(pinned).getByTestId("sidebar-toggle");
		expect(toggle).toHaveAttribute("aria-pressed", "true");
		// The panel header no longer carries a toggle; the switcher slot precedes the toggle.
		expect(within(screen.getByTestId("panel-inspector")).queryByTestId("sidebar-toggle")).toBeNull();
		expect(pinned.lastElementChild).toBe(toggle);

		fireEvent.click(toggle);

		expect(inspectorOpen("sess-1")).toBe(false);
		const closed = within(screen.getByTestId("session-pinned-actions")).getByTestId("sidebar-toggle");
		expect(closed).toHaveAttribute("aria-pressed", "false");
	});

	it("puts the unseen-browser dot on the sidebar toggle only while the panel is closed (B797)", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		render(<SessionView sessionId="sess-1" />);
		expect(screen.queryByTestId("sidebar-toggle-browser-dot")).not.toBeInTheDocument();
		// Browser activity arrives while the panel is closed.
		act(() => useUiStore.getState().setBrowserUnseen("sess-1", true));
		expect(screen.getByTestId("sidebar-toggle-browser-dot")).toBeInTheDocument();

		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		expect(screen.queryByTestId("sidebar-toggle-browser-dot")).not.toBeInTheDocument();
	});
});
