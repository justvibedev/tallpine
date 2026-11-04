import { TooltipProvider } from "@/components/ui/tooltip";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PanelSwitcher, type PanelSwitcherItem } from "./PanelSwitcher";

const items: PanelSwitcherItem[] = [
	{ id: "browser", label: "Browser", icon: <svg /> },
	{ id: "plan", label: "Plan", icon: <svg /> },
	{ id: "artifacts", label: "Artifacts", icon: <svg />, badge: 0 },
	{ id: "files", label: "Files", icon: <svg />, shortcut: "⌘⇧Y" },
];

const renderSwitcher = (props: Partial<React.ComponentProps<typeof PanelSwitcher>> = {}) => {
	const onSelect = vi.fn();
	const onClose = vi.fn();
	const view = render(
		<TooltipProvider>
			<PanelSwitcher activeView="browser" items={items} onClose={onClose} onSelect={onSelect} open={false} {...props} />
		</TooltipProvider>,
	);
	return { onClose, onSelect, ...view };
};

describe("PanelSwitcher", () => {
	it("opens the panel on the clicked view while closed", () => {
		const { onClose, onSelect } = renderSwitcher({ open: false });
		fireEvent.click(screen.getByRole("button", { name: "Artifacts" }));
		expect(onSelect).toHaveBeenCalledWith("artifacts");
		expect(onClose).not.toHaveBeenCalled();
	});

	it("switches views without closing while another view is open", () => {
		const { onClose, onSelect } = renderSwitcher({ activeView: "browser", open: true });
		fireEvent.click(screen.getByRole("button", { name: "Files" }));
		expect(onSelect).toHaveBeenCalledWith("files");
		expect(onClose).not.toHaveBeenCalled();
	});

	it("closes the panel when the active icon is clicked", () => {
		const { onClose, onSelect } = renderSwitcher({ activeView: "plan", open: true });
		fireEvent.click(screen.getByRole("button", { name: "Plan" }));
		expect(onClose).toHaveBeenCalledTimes(1);
		expect(onSelect).not.toHaveBeenCalled();
	});

	it("marks only the active icon of an open panel as selected", () => {
		const { rerender } = renderSwitcher({ activeView: "browser", open: true });
		expect(screen.getByRole("button", { name: "Browser" })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("button", { name: "Plan" })).toHaveAttribute("aria-pressed", "false");
		rerender(
			<TooltipProvider>
				<PanelSwitcher activeView="browser" items={items} onClose={vi.fn()} onSelect={vi.fn()} open={false} />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "Browser" })).toHaveAttribute("aria-pressed", "false");
	});

	it("renders only the offered views, in order", () => {
		renderSwitcher({ items: [items[0], items[1]] });
		expect(screen.getAllByRole("button").map((button) => button.getAttribute("aria-label"))).toEqual(["Browser", "Plan"]);
		expect(screen.queryByRole("button", { name: "Files" })).toBeNull();
	});

	it("draws the unread count on the bell, capped at 99+, and nowhere else", () => {
		const withBadge = (badge: number) => items.map((item) => (item.id === "artifacts" ? { ...item, badge } : item));
		const { rerender } = renderSwitcher({ items: withBadge(3) });
		expect(screen.getByTestId("panel-switcher-artifacts-badge")).toHaveTextContent("3");
		expect(screen.getByRole("button", { name: "Artifacts" })).toContainElement(screen.getByTestId("panel-switcher-artifacts-badge"));
		expect(screen.getAllByTestId(/-badge$/)).toHaveLength(1);
		rerender(
			<TooltipProvider>
				<PanelSwitcher activeView="browser" items={withBadge(250)} onClose={vi.fn()} onSelect={vi.fn()} open />
			</TooltipProvider>,
		);
		expect(screen.getByTestId("panel-switcher-artifacts-badge")).toHaveTextContent("99+");
	});

	it("announces the unread count through the bell's accessible name", () => {
		const withLabel = items.map((item) => (item.id === "artifacts" ? { ...item, badge: 3, ariaLabel: "Artifacts, 3 unread artifacts" } : item));
		renderSwitcher({ items: withLabel });
		expect(screen.getByRole("button", { name: "Artifacts, 3 unread artifacts" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Browser" })).toBeInTheDocument();
	});

	it("shows no badge at zero and a browser dot only while that view is not active", () => {
		const withDot = items.map((item) => (item.id === "browser" ? { ...item, dot: true } : item));
		const { rerender } = renderSwitcher({ items: withDot, open: false });
		expect(screen.queryByTestId("panel-switcher-artifacts-badge")).toBeNull();
		expect(screen.getByTestId("panel-switcher-browser-dot")).toBeInTheDocument();
		rerender(
			<TooltipProvider>
				<PanelSwitcher activeView="browser" items={withDot} onClose={vi.fn()} onSelect={vi.fn()} open />
			</TooltipProvider>,
		);
		expect(screen.queryByTestId("panel-switcher-browser-dot")).toBeNull();
	});

	it("names each icon in its tooltip, with the shortcut where one exists", async () => {
		renderSwitcher();
		fireEvent.focus(screen.getByRole("button", { name: "Files" }));
		await waitFor(() => expect(screen.getAllByText("Files · ⌘⇧Y").length).toBeGreaterThan(0));
	});
});
