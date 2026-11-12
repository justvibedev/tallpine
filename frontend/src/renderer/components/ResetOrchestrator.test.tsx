import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../i18n";

const { navigate, ask, handOff } = vi.hoisted(() => ({ navigate: vi.fn(), ask: vi.fn(), handOff: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
vi.mock("../lib/handoff-orchestrator", () => ({ askOrchestratorToHandOff: ask, handoffOrchestratorWithLatestNote: handOff }));

import { useResetOrchestrator } from "./ResetOrchestrator";

function Harness() {
	const { request, dialog } = useResetOrchestrator("demo-1");
	return (
		<>
			<button onClick={request}>open</button>
			{dialog}
		</>
	);
}

function renderHarness() {
	render(
		<I18nextProvider i18n={appI18n}>
			<QueryClientProvider client={new QueryClient()}>
				<Harness />
			</QueryClientProvider>
		</I18nextProvider>,
	);
	fireEvent.click(screen.getByText("open"));
}

describe("useResetOrchestrator", () => {
	beforeEach(() => {
		navigate.mockReset();
		ask.mockReset().mockResolvedValue(undefined);
		handOff.mockReset();
	});

	it("does nothing until the reset is confirmed", async () => {
		renderHarness();
		await screen.findByText("Reset the orchestrator?");
		expect(handOff).not.toHaveBeenCalled();
		fireEvent.click(screen.getByText("Cancel"));
		expect(handOff).not.toHaveBeenCalled();
	});

	it("hands off and opens the fresh orchestrator on confirm", async () => {
		handOff.mockResolvedValue({ kind: "handedOff", sessionId: "demo-2", projectId: "demo" });
		renderHarness();
		fireEvent.click(await screen.findByRole("button", { name: "Reset orchestrator" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "demo", sessionId: "demo-2" },
		}));
		expect(handOff).toHaveBeenCalledWith("demo-1", undefined);
		expect(ask).not.toHaveBeenCalled();
	});

	it("asks the orchestrator to write a note and hand off when none exists", async () => {
		handOff.mockResolvedValue({ kind: "noNote" });
		renderHarness();
		fireEvent.click(await screen.findByRole("button", { name: "Reset orchestrator" }));
		await waitFor(() => expect(ask).toHaveBeenCalledWith("demo-1", undefined));
		expect(navigate).not.toHaveBeenCalled();
	});
});
