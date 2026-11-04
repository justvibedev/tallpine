import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SilentFailures } from "../hooks/useSilentFailures";
import { silentFailuresPolling, silentFailuresQueryKey } from "../hooks/useSilentFailures";
import { HealthBadge } from "./HealthBadge";
import { TooltipProvider } from "./ui/tooltip";

const test = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: test.get },
	apiErrorMessage: (e: unknown) => String(e),
}));

const healthy: SilentFailures = { healthy: true, chatNotAttached: [], reportBatches: [], turnFallbacks: [] };

function renderBadge() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}><HealthBadge /></TooltipProvider>
		</QueryClientProvider>,
	);
	// Resolves once the query has settled, so "renders nothing" is checked after the data arrived.
	return () => waitFor(() => expect(client.getQueryState(silentFailuresQueryKey)?.fetchStatus).toBe("idle"));
}

beforeEach(() => test.get.mockReset());

describe("HealthBadge", () => {
	it("renders nothing while healthy", async () => {
		test.get.mockResolvedValue({ data: healthy });
		const settled = renderBadge();
		await settled();
		expect(test.get).toHaveBeenCalledWith("/api/v1/health/silent-failures");
		expect(screen.queryByTestId("health-badge")).toBeNull();
	});

	it("renders nothing when the endpoint fails", async () => {
		test.get.mockResolvedValue({ error: { message: "boom" } });
		const settled = renderBadge();
		await settled();
		expect(screen.queryByTestId("health-badge")).toBeNull();
	});

	it("lists each failing condition in the tooltip", async () => {
		test.get.mockResolvedValue({
			data: {
				healthy: false,
				chatNotAttached: [{ sessionId: "s-1" }],
				reportBatches: [{ projectId: "p", pending: 2, claimed: 0, undeliverable: true }],
				turnFallbacks: [{ id: "f", sessionId: "s-2" }],
			} satisfies SilentFailures,
		});
		renderBadge();
		const badge = await screen.findByTestId("health-badge");
		await userEvent.hover(badge);
		const tip = await screen.findAllByText(/no attached agent/);
		expect(tip.length).toBeGreaterThan(0);
		expect((await screen.findAllByText(/reports are not being delivered/)).length).toBeGreaterThan(0);
		expect((await screen.findAllByText(/overdue/)).length).toBeGreaterThan(0);
	});

	it("names the detached chat sessions", async () => {
		test.get.mockResolvedValue({ data: { ...healthy, healthy: false, chatNotAttached: [{ sessionId: "sess-a" }, { sessionId: "sess-b" }] } });
		renderBadge();
		await userEvent.hover(await screen.findByTestId("health-badge"));
		expect((await screen.findAllByText(/sess-a, sess-b/)).length).toBeGreaterThan(0);
	});

	it("caps the listed sessions", async () => {
		const ids = ["a", "b", "c", "d", "e", "f", "g"].map((sessionId) => ({ sessionId }));
		test.get.mockResolvedValue({ data: { ...healthy, healthy: false, chatNotAttached: ids } });
		renderBadge();
		await userEvent.hover(await screen.findByTestId("health-badge"));
		expect((await screen.findAllByText(/a, b, c, d, e \+2/)).length).toBeGreaterThan(0);
	});

	it("keeps refreshing in the background and on focus, and drops the badge once healthy", async () => {
		expect(silentFailuresPolling).toEqual({ refetchIntervalInBackground: true, refetchOnWindowFocus: "always", staleTime: 0 });
		test.get.mockResolvedValueOnce({ data: { ...healthy, healthy: false, chatNotAttached: [{ sessionId: "s" }] } }).mockResolvedValue({ data: healthy });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><TooltipProvider><HealthBadge /></TooltipProvider></QueryClientProvider>);
		await screen.findByTestId("health-badge");
		await client.refetchQueries({ queryKey: silentFailuresQueryKey });
		await waitFor(() => expect(screen.queryByTestId("health-badge")).toBeNull());
	});

	it("omits conditions that are not present", async () => {
		test.get.mockResolvedValue({ data: { ...healthy, healthy: false, turnFallbacks: [{ id: "f", sessionId: "s" }] } });
		renderBadge();
		await userEvent.hover(await screen.findByTestId("health-badge"));
		await screen.findAllByText(/overdue/);
		expect(screen.queryByText(/no attached agent/)).toBeNull();
		expect(screen.queryByText(/reports are not being delivered/)).toBeNull();
	});

	it("does not mention overdue notices when only a chat agent is detached", async () => {
		test.get.mockResolvedValue({ data: { ...healthy, healthy: false, chatNotAttached: [{ sessionId: "s" }] } });
		renderBadge();
		await userEvent.hover(await screen.findByTestId("health-badge"));
		await screen.findAllByText(/no attached agent/);
		expect(screen.queryByText(/overdue/)).toBeNull();
	});
});
