import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { UpdateStatus } from "../../hooks/useUpdate";
import { UpdateRow } from "./UpdateRow";

const test = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: test.get, POST: test.post },
	apiErrorMessage: (e: unknown) => String((e as { message?: string })?.message ?? e),
}));

const base: UpdateStatus = {
	installedCommit: "1111111aaaa",
	latestCommit: "2222222bbbb",
	latestCiPassed: true,
	candidate: "2222222bbbb",
	updateAvailable: true,
	buildAvailable: false,
	running: false,
};

function renderRow(status: UpdateStatus) {
	test.get.mockResolvedValue({ data: status });
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={client}><UpdateRow /></QueryClientProvider>);
}

beforeEach(() => {
	test.get.mockReset();
	test.post.mockReset();
});

describe("UpdateRow", () => {
	it("offers the CI-passed commit and enables the button", async () => {
		renderRow(base);
		expect(await screen.findByText("Update available: 2222222 (CI passed)")).toBeTruthy();
		expect((screen.getByTestId("update-restart") as HTMLButtonElement).disabled).toBe(false);
	});

	it("disables the button when up to date", async () => {
		renderRow({ ...base, updateAvailable: false, candidate: base.installedCommit });
		await screen.findByText("Up to date (1111111)");
		expect((screen.getByTestId("update-restart") as HTMLButtonElement).disabled).toBe(true);
	});

	it("disables the button while an update runs", async () => {
		renderRow({ ...base, running: true });
		await screen.findByText("Updating. The app restarts when it finishes.");
		expect((screen.getByTestId("update-restart") as HTMLButtonElement).disabled).toBe(true);
	});

	it("confirms that agents pause and resume, then starts the update for the candidate", async () => {
		test.post.mockResolvedValue({ data: { state: "running", commit: base.candidate } });
		renderRow(base);
		await userEvent.click(await screen.findByTestId("update-restart"));
		expect(await screen.findByText(/Running agents pause and resume/)).toBeTruthy();
		expect(test.post).not.toHaveBeenCalled();
		await userEvent.click(screen.getByTestId("update-confirm"));
		await waitFor(() => expect(test.post).toHaveBeenCalledWith("/api/v1/update", { body: { commit: base.candidate } }));
	});

	it("shows a rolled back result after reconnecting", async () => {
		renderRow({ ...base, last: { state: "rolled_back", commit: "3333333ccc", startedAt: "", finishedAt: "", message: "the new version was not healthy" } });
		expect((await screen.findByTestId("update-failed")).textContent).toContain("rolled back");
	});
});
