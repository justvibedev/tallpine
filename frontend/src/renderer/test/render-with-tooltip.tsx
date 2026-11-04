import { render, type RenderResult } from "@testing-library/react";
import type { ReactNode } from "react";
import { TooltipProvider } from "../components/ui/tooltip";

/** render() inside the TooltipProvider that most components need. */
export function renderWithTooltip(ui: ReactNode): RenderResult {
	return render(<TooltipProvider>{ui}</TooltipProvider>);
}
