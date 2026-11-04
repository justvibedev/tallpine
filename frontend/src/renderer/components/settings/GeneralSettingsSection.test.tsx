import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { GeneralSettingsSection } from "./GeneralSettingsSection";
import { TooltipProvider } from "../ui/tooltip";

vi.mock("../../hooks/useManagedLocks", () => ({ useManagedLocks: () => new Set<string>() }));

describe("global General settings", () => {
	it("has no review rows: auto review, end on merge and the risky reviewer are per project", () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><TooltipProvider><GeneralSettingsSection /></TooltipProvider></QueryClientProvider>);
		for (const label of [/Auto review/i, /End workers when their PRs merge/i, /stronger review/i, /Risky paths/i, /Reviewer model for risky/i]) {
			expect(screen.queryByText(label)).not.toBeInTheDocument();
		}
	});
});
