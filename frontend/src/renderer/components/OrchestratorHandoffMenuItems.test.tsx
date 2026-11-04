import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../i18n";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "./ui/dropdown-menu";

const { navigate, ask, handOff } = vi.hoisted(() => ({ navigate: vi.fn(), ask: vi.fn(), handOff: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
vi.mock("../lib/handoff-orchestrator", () => ({ askOrchestratorToHandOff: ask, handoffOrchestratorWithLatestNote: handOff }));

import { OrchestratorHandoffMenuItems } from "./OrchestratorHandoffMenuItems";

function renderMenu() {
	return render(
		<I18nextProvider i18n={appI18n}>
			<QueryClientProvider client={new QueryClient()}>
				<DropdownMenu open>
					<DropdownMenuTrigger>menu</DropdownMenuTrigger>
					<DropdownMenuContent>
						<OrchestratorHandoffMenuItems sessionId="demo-1" />
					</DropdownMenuContent>
				</DropdownMenu>
			</QueryClientProvider>
		</I18nextProvider>,
	);
}

describe("OrchestratorHandoffMenuItems", () => {
	beforeEach(() => {
		navigate.mockReset();
		ask.mockReset().mockResolvedValue(undefined);
		handOff.mockReset();
	});

	it("asks the orchestrator to write its note and hand off", async () => {
		renderMenu();
		fireEvent.click(screen.getByText("Start fresh orchestrator (handoff)"));
		await waitFor(() => expect(ask).toHaveBeenCalledWith("demo-1", undefined));
		expect(handOff).not.toHaveBeenCalled();
	});

	it("hands off from the latest note and opens the new orchestrator", async () => {
		handOff.mockResolvedValue({ kind: "handedOff", sessionId: "demo-2", projectId: "demo" });
		renderMenu();
		fireEvent.click(screen.getByText("Start fresh orchestrator from its latest note"));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "demo", sessionId: "demo-2" },
		}));
	});

	it("stays on the current orchestrator when there is no note yet", async () => {
		handOff.mockResolvedValue({ kind: "noNote" });
		renderMenu();
		fireEvent.click(screen.getByText("Start fresh orchestrator from its latest note"));
		await waitFor(() => expect(handOff).toHaveBeenCalled());
		expect(navigate).not.toHaveBeenCalled();
	});
});
