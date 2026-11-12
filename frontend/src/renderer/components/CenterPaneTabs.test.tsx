import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../i18n";
import type { WorkspaceSession } from "../types/workspace";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));

import { SessionPaneTab } from "./CenterPaneTabs";

function renderTab(session: Partial<WorkspaceSession>) {
	render(
		<I18nextProvider i18n={appI18n}>
			<QueryClientProvider client={new QueryClient()}>
				<SessionPaneTab isActive label="Orchestrator" onRenamed={() => {}} session={{ id: "demo-1", workspaceId: "demo", provider: "claude", title: "t", ...session } as WorkspaceSession} />
			</QueryClientProvider>
		</I18nextProvider>,
	);
	fireEvent.contextMenu(screen.getByRole("tab"));
}

describe("SessionPaneTab context menu", () => {
	it("offers Reset orchestrator on a live orchestrator, behind a confirmation", async () => {
		renderTab({ kind: "orchestrator" });
		fireEvent.click(screen.getByText("Reset orchestrator"));
		expect(await screen.findByText("Reset the orchestrator?")).toBeTruthy();
	});

	it("has no reset entry on a worker or an ended orchestrator", () => {
		renderTab({ kind: "worker", id: "demo-2" });
		expect(screen.queryByText("Reset orchestrator")).toBeNull();
	});

	it("has no reset entry once the orchestrator has ended", () => {
		renderTab({ kind: "orchestrator", isTerminated: true });
		expect(screen.queryByText("Reset orchestrator")).toBeNull();
	});
});
