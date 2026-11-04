import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Sidebar, SidebarProvider } from "./sidebar";

function renderSidebar({ open, overlay }: { open: boolean; overlay?: boolean }) {
	render(
		<SidebarProvider open={open}>
			<Sidebar overlay={overlay}>
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

	it("floats as an overlay without reserving layout, with elevation only while open", () => {
		const open = renderSidebar({ open: true, overlay: true });
		expect(open.gap.style.width).toBe("var(--tallpine-rail-w, 0px)");
		expect(open.container).toHaveClass("shadow-lg");
		expect(open.container.closest("[data-overlay]")).toHaveAttribute("data-overlay", "true");
	});
});
