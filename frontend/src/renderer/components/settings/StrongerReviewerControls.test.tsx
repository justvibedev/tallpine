import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { StrongerReviewerControls } from "./StrongerReviewerControls";
import { TooltipProvider } from "../ui/tooltip";

const { catalog } = vi.hoisted(() => ({ catalog: { models: [{ id: "claude-opus-5-5", label: "Opus 5.5", efforts: ["low", "high"] }, { id: "claude-sonnet-5-5", label: "Sonnet 5.5" }], customModelEntry: "none" as "none" | "direct" } }));
vi.mock("../../hooks/useAgentModelsQuery", async (original) => ({
	...(await original<typeof import("../../hooks/useAgentModelsQuery")>()),
	agentModelsQueryOptions: () => ({ queryKey: ["agent-models-test"], queryFn: async () => catalog }),
}));
vi.mock("../../hooks/useOfferedModels", () => ({ useOfferedModels: (_agent: string, models: unknown[] | undefined) => ({ models: models ?? [], narrowed: false }) }));

function setup(values: Parameters<typeof StrongerReviewerControls>[0]["values"], onChange = vi.fn()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = render(<QueryClientProvider client={client}><TooltipProvider>
		<StrongerReviewerControls values={values} onChange={onChange} modelScope={() => ""} renderAgent={({ value }) => <span data-testid="agent">{value}</span>} />
	</TooltipProvider></QueryClientProvider>);
	return Object.assign(onChange, { unmount: view.unmount });
}

describe("StrongerReviewerControls", () => {
	it("shows the built-in Claude Code default when nothing is stored", async () => {
		setup({});
		expect(screen.getByTestId("agent")).toHaveTextContent("claude-code");
		await waitFor(() => expect(screen.getByRole("button", { name: "Model and effort" })).toHaveTextContent("Opus 5.5"));
		expect(screen.getByText("Built-in default until you pick another.")).toBeInTheDocument();
	});

	it("offers no free-text model entry when the catalog allows none, and does when it declares one", async () => {
		catalog.customModelEntry = "none";
		const stored = { reviewRiskHarness: "claude-code", reviewRiskModel: "claude-opus-5-5" };
		const { unmount } = setup(stored);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model and effort" })).toHaveTextContent("Opus 5.5"));
		await userEvent.click(screen.getByRole("button", { name: "Model and effort" }));
		await screen.findByRole("menuitem", { name: "Sonnet 5.5" });
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		unmount();
		catalog.customModelEntry = "direct";
		setup(stored);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model and effort" })).toHaveTextContent("Opus 5.5"));
		await userEvent.click(screen.getByRole("button", { name: "Model and effort" }));
		await userEvent.type(await screen.findByRole("searchbox"), "my-private-model");
		expect(await screen.findByRole("menuitem", { name: /my-private-model/ })).toBeInTheDocument();
		catalog.customModelEntry = "none";
	});

	it("flags a stored model the catalog does not list", async () => {
		setup({ reviewRiskHarness: "claude-code", reviewRiskModel: "claude-retired-1" });
		expect(await screen.findByRole("alert")).toHaveTextContent("claude-retired-1");
	});

	it("does not flag a listed model", async () => {
		setup({ reviewRiskHarness: "claude-code", reviewRiskModel: "claude-sonnet-5-5" });
		await screen.findByRole("button", { name: "Model and effort" });
		await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
	});

	it("pins the whole shown reviewer on the first edit", async () => {
		const onChange = setup({});
		await userEvent.click(await screen.findByRole("button", { name: "Model and effort" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Sonnet 5.5" }));
		expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ reviewRiskHarness: "claude-code", reviewRiskModel: "claude-sonnet-5-5" }));
	});
});
