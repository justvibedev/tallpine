import { TooltipProvider } from "@/components/ui/tooltip";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PanelToggleButton } from "./PanelToggleButton";

const renderButton = (side?: "left" | "right", open = true) =>
	render(
		<TooltipProvider>
			<PanelToggleButton onClick={() => undefined} open={open} side={side} />
		</TooltipProvider>,
	);

describe("PanelToggleButton", () => {
	it("is the right panel's control by default", () => {
		renderButton();
		expect(screen.getByRole("button", { name: "Toggle sidebar" })).toHaveAttribute("aria-pressed", "true");
	});

	it("labels the left (Sessions) panel's control and reflects closed state", () => {
		renderButton("left", false);
		expect(screen.getByRole("button", { name: "Toggle sessions panel" })).toHaveAttribute("aria-pressed", "false");
	});
});
