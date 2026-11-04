import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../i18n";
import type { WorkspaceSession } from "../types/workspace";

const { copyLinkText } = vi.hoisted(() => ({ copyLinkText: vi.fn() }));
vi.mock("../lib/copy-link", () => ({ copyLinkText }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
vi.mock("../hooks/conversation", () => ({ useProviderSwitchCommand: () => ({ mutateAsync: vi.fn(), isPending: false }) }));

import { SessionTabMenu } from "./TerminalAgentControls";

describe("SessionTabMenu on an orchestrator", () => {
	beforeEach(() => copyLinkText.mockClear());
	it("copies the orchestrator's session ID and link next to the handoff entries", async () => {
		const session = { id: "orch-1", workspaceId: "proj", role: "orchestrator", kind: "orchestrator", isTerminated: false } as unknown as WorkspaceSession;
		render(
			<I18nextProvider i18n={appI18n}>
				<QueryClientProvider client={new QueryClient()}>
					<SessionTabMenu session={session} blocked={false} switchTarget="tui" switchAction={{ disabled: false, run: vi.fn() }} />
				</QueryClientProvider>
			</I18nextProvider>,
		);
		fireEvent.pointerDown(screen.getByLabelText("Session actions"), { button: 0, ctrlKey: false });
		fireEvent.click(await screen.findByText("Copy session ID"));
		expect(copyLinkText).toHaveBeenCalledWith("orch-1");
		fireEvent.pointerDown(screen.getByLabelText("Session actions"), { button: 0, ctrlKey: false });
		fireEvent.click(await screen.findByText("Copy session link"));
		expect(copyLinkText).toHaveBeenCalledWith("tallpine://sessions/proj/orch-1");
	});
});
