import { render, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Sidebar, SidebarProvider, SidebarRail } from "./sidebar";

function renderSidebar({ open, overlay }: { open: boolean; overlay?: boolean }) {
	render(
		<SidebarProvider open={open}>
			<Sidebar overlay={overlay}>
				<SidebarRail />
				<p>panel</p>
			</Sidebar>
		</SidebarProvider>,
	);
	return {
		gap: document.querySelector<HTMLElement>('[data-slot="sidebar-gap"]')!,
		container: document.querySelector<HTMLElement>('[data-slot="sidebar-container"]')!,
	};
}

describe("Sidebar beside the shell icon rail", () => {
	it("reserves the rail plus the panel when open, and sits to the right of the rail", () => {
		const { gap, container } = renderSidebar({ open: true });
		expect(gap.style.width).toBe("calc(var(--tallpine-rail-w, 0px) + var(--tallpine-sidebar-w, var(--sidebar-width)))");
		expect(container).toHaveClass("left-(--tallpine-rail-w,0px)");
	});

	it("reserves only the rail once the panel is closed", () => {
		const { gap } = renderSidebar({ open: false });
		expect(gap.style.width).toBe("var(--tallpine-rail-w, 0px)");
	});

	it("hides the closed panel once it has slid away, keeps the reopen strip live, and restores on expand (B610)", async () => {
		const tree = (open: boolean) => (
			<SidebarProvider open={open}>
				<Sidebar>
					<SidebarRail />
					<p>panel</p>
				</Sidebar>
			</SidebarProvider>
		);
		const { rerender } = render(tree(false));
		const container = document.querySelector<HTMLElement>('[data-slot="sidebar-container"]')!;
		const strip = document.querySelector<HTMLElement>('[data-slot="sidebar-rail"]')!;
		// Closed: the part of the panel left under the rail is hidden; the strip opts back in to stay clickable.
		expect(container.className).toContain("group-data-[collapsible=offcanvas]:invisible");
		expect(strip.className).toContain("group-data-[collapsible=offcanvas]:visible");
		expect(container.closest("[data-slot=sidebar]")).toHaveAttribute("data-collapsible", "offcanvas");
		rerender(tree(true));
		expect(container.closest("[data-slot=sidebar]")).toHaveAttribute("data-collapsible", "");
		await waitFor(() => expect(container.style.transform).not.toContain("-100%"));
		expect(container.style.transform).not.toContain("--tallpine-rail-w");
		rerender(tree(false));
		await waitFor(() => expect(container.style.transform).toContain("-100%"));
		expect(container.style.transform).not.toContain("--tallpine-rail-w");
	});

	it("floats as an overlay without reserving layout, with elevation only while open", () => {
		const open = renderSidebar({ open: true, overlay: true });
		expect(open.gap.style.width).toBe("var(--tallpine-rail-w, 0px)");
		expect(open.container).toHaveClass("shadow-lg");
		expect(open.container.closest("[data-overlay]")).toHaveAttribute("data-overlay", "true");
	});
});
