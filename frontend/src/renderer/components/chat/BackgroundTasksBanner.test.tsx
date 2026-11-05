import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { BackgroundTasksBanner } from "./BackgroundTasksBanner";

const post = vi.hoisted(() => vi.fn());
vi.mock("../../lib/host-clients", () => ({ clientForSessionHost: () => ({ POST: post }) }));

function renderBanner(tasks?: { id: string; name: string; kind?: string; model?: string; startedAt?: string }[]) {
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<BackgroundTasksBanner tasks={tasks} sessionId="w-1" />
		</QueryClientProvider>,
	);
}

describe("BackgroundTasksBanner", () => {
	beforeEach(() => post.mockReset().mockResolvedValue({ data: { stopped: true } }));

	it("stops the clicked task through the daemon API", async () => {
		renderBanner([{ id: "bg-7", name: "tail -f log" }, { id: "bg-8", name: "watch ci" }]);
		await userEvent.click(screen.getByRole("button", { name: "Stop watch ci" }));
		await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
		expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/background-tasks/stop", {
			params: { path: { sessionId: "w-1" } },
			body: { taskId: "bg-8" },
		});
	});

	it("shows the daemon's reason when the stop is refused", async () => {
		post.mockResolvedValue({ error: { code: "BACKGROUND_TASK_NOT_STOPPED", message: "the agent did not stop the task" } });
		renderBanner([{ id: "bg-7", name: "tail -f log" }]);
		await userEvent.click(screen.getByRole("button", { name: "Stop tail -f log" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("did not stop the task");
	});

	// B562: a background subagent ran for minutes with nothing naming it.
	it("lists a background subagent with its model and elapsed time and no stop control", () => {
		const startedAt = new Date(Date.now() - 5 * 60_000 - 3_000).toISOString();
		renderBanner([
			{ id: "acc20e", name: "Fable planner: hub UX plan", kind: "subagent", model: "fable", startedAt },
			{ id: "bg-7", name: "tail -f log" },
		]);
		expect(screen.getByText("Subagent")).toBeInTheDocument();
		expect(screen.getByText("Fable planner: hub UX plan")).toBeInTheDocument();
		expect(screen.getByText("fable")).toBeInTheDocument();
		expect(screen.getByText("5m")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Stop Fable planner: hub UX plan" })).toBeNull();
		expect(screen.getByRole("button", { name: "Stop tail -f log" })).toBeInTheDocument();
	});

	it("renders nothing without tasks", () => {
		const { container } = renderBanner(undefined);
		expect(container).toBeEmptyDOMElement();
	});
});
