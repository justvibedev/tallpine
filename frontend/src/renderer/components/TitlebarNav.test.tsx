import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { TitlebarNav } from "./TitlebarNav";
import { TooltipProvider } from "./ui/tooltip";
import { renderWithTooltip as render } from "../test/render-with-tooltip";

const { history } = vi.hoisted(() => ({
	history: {
		back: vi.fn(),
		forward: vi.fn(),
		location: { state: { __TSR_index: 0 } },
		subscribe: vi.fn(() => () => undefined),
	},
}));

vi.mock("@tanstack/react-router", () => ({
	useCanGoBack: () => false,
	useNavigate: () => vi.fn(),
	useRouter: () => ({ history }),
}));

vi.mock("../lib/platform", () => ({
	isLinuxPlatform: () => false,
	isMacPlatform: () => true,
}));

describe("TitlebarNav", () => {
	it("keeps the same header row in and out of fullscreen", () => {
		const { container, rerender } = render(<TitlebarNav />);
		const nav = container.querySelector('[data-slot="titlebar-nav"]');
		expect(nav).toHaveClass("top-0", "h-traffic-light-clearance", "left-titlebar-cluster-left");
		rerender(<TooltipProvider><TitlebarNav isFullScreen /></TooltipProvider>);
		expect(nav).toHaveClass("top-0", "h-traffic-light-clearance", "left-titlebar-cluster-left-fullscreen");
	});

	it.each([false, true])("is only the history arrows: no sidebar toggle or brand, sidebar open=%s", (open) => {
		useUiStore.setState({ isSidebarOpen: open });
		const { container } = render(<TitlebarNav />);
		expect(screen.getByRole("button", { name: "Go back" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Go forward" })).toBeDisabled();
		expect(screen.queryByRole("button", { name: /sidebar/i })).toBeNull();
		expect(container.querySelector('[data-slot="titlebar-brand"]')).toBeNull();
		expect(container.textContent).not.toContain("Orchestrator");
	});
});
