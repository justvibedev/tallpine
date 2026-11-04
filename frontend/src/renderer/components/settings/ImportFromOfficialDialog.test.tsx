import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { POST: post },
	apiErrorMessage: (_error: unknown, fallback = "failed") => fallback,
}));

import { ImportFromOfficialDialog } from "./ImportFromOfficialDialog";

const counts = { create: 0, update: 0, unchanged: 0, conflict: 0, skip: 0, error: 0 };

function plan(overrides: Record<string, unknown>) {
	return { id: "x", name: "X", path: "/w/x", kind: "single_repo", action: "create", selected: true, ...overrides };
}

function report(overrides: Record<string, unknown> = {}) {
	return {
		sourceDataDir: "/home/u/.tallpine/data",
		found: true,
		dryRun: true,
		includeSettings: false,
		counts,
		projects: [
			plan({ id: "alpha", name: "Alpha", path: "/w/alpha", warnings: ["worker harness \"gemini\" is not available in Tallpine; using claude-code"] }),
			plan({ id: "beta", name: "Beta", path: "/w/beta", action: "update" }),
			plan({ id: "gamma", name: "Gamma", path: "/w/gamma", action: "unchanged" }),
			plan({ id: "old", name: "Old", path: "/w/old", action: "skip", selected: false, reason: "archived in Tallpine" }),
			plan({ id: "clash", name: "Clash", path: "/w/clash", action: "conflict", reason: "same id with different active path" }),
		],
		...overrides,
	};
}

function renderDialog(onImported = vi.fn()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<ImportFromOfficialDialog onImported={onImported} onOpenChange={vi.fn()} open />
		</QueryClientProvider>,
	);
	return onImported;
}

describe("ImportFromOfficialDialog", () => {
	beforeEach(() => post.mockReset());

	it("lists detected projects, ticks only new ones, and imports the chosen ids", async () => {
		post.mockResolvedValueOnce({ data: { report: report() } });
		post.mockResolvedValueOnce({
			data: { report: report({ dryRun: false, counts: { ...counts, create: 1 }, projects: [plan({ id: "alpha", name: "Alpha", applied: true })] }) },
		});
		const onImported = renderDialog();

		const alpha = await screen.findByRole("checkbox", { name: "Alpha" });
		expect(alpha).toBeChecked();
		expect(screen.getByRole("checkbox", { name: "Beta" })).not.toBeChecked();
		expect(screen.getByRole("checkbox", { name: "Beta" })).toBeEnabled();
		expect(screen.getByRole("checkbox", { name: "Gamma" })).toBeDisabled();
		expect(screen.getByRole("checkbox", { name: "Old" })).toBeDisabled();
		expect(screen.getByRole("checkbox", { name: "Clash" })).toBeDisabled();
		expect(screen.getByText(/worker harness "gemini"/)).toBeInTheDocument();
		expect(screen.getByText("archived in Tallpine")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("checkbox", { name: "Beta" }));
		await userEvent.click(screen.getByRole("button", { name: "Import" }));

		await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		expect(post.mock.calls[0][1]).toEqual({ body: { dryRun: true } });
		expect(post.mock.calls[1][1]).toEqual({ body: { dryRun: false, projectIds: ["alpha", "beta"], includeSettings: false } });
		expect(await screen.findByText("Imported 1 new and updated 0.")).toBeInTheDocument();
		expect(onImported).toHaveBeenCalled();
	});

	it("disables Import until something is selected", async () => {
		post.mockResolvedValueOnce({ data: { report: report() } });
		renderDialog();
		await userEvent.click(await screen.findByRole("checkbox", { name: "Alpha" }));
		expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
	});

	it("offers global settings only when they would change, and sends the choice", async () => {
		post.mockResolvedValueOnce({
			data: { report: report({ settings: [{ key: "defaultSessionMode", value: "chat", current: "tui", action: "update" }]}) },
		});
		post.mockResolvedValueOnce({ data: { report: report({ dryRun: false, projects: [] }) } });
		renderDialog();
		await userEvent.click(await screen.findByRole("checkbox", { name: "Also import global settings" }));
		await userEvent.click(screen.getByRole("button", { name: "Import" }));
		await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		expect(post.mock.calls[1][1].body.includeSettings).toBe(true);
	});

	it("says so when no official Agent Orchestrator install is found", async () => {
		post.mockResolvedValueOnce({ data: { report: report({ found: false, projects: [] }) } });
		renderDialog();
		expect(await screen.findByText("No Agent Orchestrator install was found on this computer.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Import" })).not.toBeInTheDocument();
	});

	it("shows a request failure in place", async () => {
		post.mockResolvedValueOnce({ error: { message: "boom" } });
		renderDialog();
		expect(await screen.findByRole("alert")).toHaveTextContent("Import failed");
	});
});
