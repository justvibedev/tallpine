import { TooltipProvider } from "@/components/ui/tooltip";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SidebarToggleButton } from "./SidebarToggleButton";

const renderToggle = (props: Partial<React.ComponentProps<typeof SidebarToggleButton>> = {}) =>
	render(
		<TooltipProvider>
			<SidebarToggleButton onClick={() => undefined} open={false} {...props} />
		</TooltipProvider>,
	);

describe("SidebarToggleButton", () => {
	it("is one pressable toggle that reports its state", () => {
		const onClick = vi.fn();
		renderToggle({ onClick, open: true });
		const toggle = screen.getByRole("button", { name: "Toggle sidebar" });
		expect(toggle).toHaveAttribute("aria-pressed", "true");
		fireEvent.click(toggle);
		expect(onClick).toHaveBeenCalledTimes(1);
	});

	it("carries no notification badge; the count lives on the bell", () => {
		renderToggle();
		expect(screen.getByRole("button", { name: "Toggle sidebar" })).toBeInTheDocument();
	});

	it("shows the shortcut in the tooltip while the aria-label stays plain", async () => {
		const { unmount } = renderToggle({ open: false });
		const toggle = screen.getByRole("button", { name: "Toggle sidebar" });
		fireEvent.focus(toggle);
		await waitFor(() => expect(screen.getAllByText("Open inspector · ⌘⇧B").length).toBeGreaterThan(0));
		unmount();
		renderToggle({ open: true });
		fireEvent.focus(screen.getByRole("button", { name: "Toggle sidebar" }));
		await waitFor(() => expect(screen.getAllByText("Close inspector · ⌘⇧B").length).toBeGreaterThan(0));
	});
});
