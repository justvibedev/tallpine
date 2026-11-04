import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SessionWorkerInstructions } from "./SessionWorkerInstructions";

const get = vi.fn();
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: get }) }));

function renderIt() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<SessionWorkerInstructions session={{ id: "tallpine-1" } as never} />
		</QueryClientProvider>,
	);
}

describe("SessionWorkerInstructions", () => {
	it("shows the project rules the worker received", async () => {
		get.mockResolvedValue({ data: { workerInstructions: "Run the tests." } });
		renderIt();
		await waitFor(() => expect(screen.getByTestId("worker-instructions")).toBeTruthy());
		expect(screen.getByText("Run the tests.")).toBeTruthy();
	});

	it("renders nothing when the project sets no rules", async () => {
		get.mockResolvedValue({ data: {} });
		const { container } = renderIt();
		await waitFor(() => expect(get).toHaveBeenCalled());
		expect(container.querySelector("[data-testid=worker-instructions]")).toBeNull();
	});
});
