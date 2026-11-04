import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ProjectSettingsChanges, settingsActorLabel } from "./ProjectSettingsChanges";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("../../lib/api-client", () => ({ apiClient: { GET: get } }));

function renderChanges() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<ProjectSettingsChanges projectId="app" />
		</QueryClientProvider>,
	);
}

describe("ProjectSettingsChanges", () => {
	beforeEach(() => get.mockReset());

	it("lists who changed which project setting, newest first", async () => {
		get.mockResolvedValue({
			data: {
				changes: [
					{ id: 2, changedAt: "2026-10-31T12:01:00Z", actor: "orchestrator:app-orch", projectId: "app", key: "project.noCI", oldValue: "null", newValue: "true" },
					{ id: 1, changedAt: "2026-10-31T12:00:00Z", actor: "user", projectId: "app", key: "project.agentRules", oldValue: '""', newValue: `"${"x".repeat(100)}"` },
				],
			},
		});
		renderChanges();
		expect(await screen.findByText("Recent setting changes")).toBeTruthy();
		expect(get).toHaveBeenCalledWith("/api/v1/settings/changes", { params: { query: { project: "app", limit: 10 } } });
		expect(screen.getByText("noCI")).toBeTruthy();
		expect(screen.getByText("— → true")).toBeTruthy();
		expect(screen.getByText(/^Orchestrator app-orch · /)).toBeTruthy();
		expect(screen.getByText(/^You · /)).toBeTruthy();
		const cut = screen.getByText(/^"" → "x+…$/);
		expect(cut.textContent?.length).toBeLessThan(80);
	});

	it("renders nothing before any change", async () => {
		get.mockResolvedValue({ data: { changes: [] } });
		const { container } = renderChanges();
		await vi.waitFor(() => expect(get).toHaveBeenCalled());
		expect(container.textContent).toBe("");
	});

	it("names actors", () => {
		const t = (key: string, o?: Record<string, string>) => `${key}${o ? JSON.stringify(o) : ""}`;
		expect(settingsActorLabel("user", t)).toBe("settings.project.changes.you");
		expect(settingsActorLabel("orchestrator:s-1", t)).toBe('settings.project.changes.orchestrator{"session":"s-1"}');
		expect(settingsActorLabel("worker:s-2", t)).toBe("worker:s-2");
	});
});
