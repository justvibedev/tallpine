import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { TopbarAccountMenu, topbarAccountLabel } from "./TopbarAccountMenu";
import { TooltipProvider } from "./ui/tooltip";

const switchSession = vi.fn();
const refreshSession = vi.fn();
const accounts = [
	{ id: "acc-a", provider: "claude", email: "alice@example.com", signedIn: true },
	{ id: "acc-b", provider: "claude", email: "bob@example.com", signedIn: true },
	{ id: "acc-c", provider: "claude", email: "carol@example.com", signedIn: false },
];
let transition: Record<string, unknown> | undefined;

vi.mock("../hooks/useAgentAccounts", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useAgentAccounts")>();
	return {
		...actual,
		useAgentAccounts: () => ({ data: accounts }),
		useSessionAgentAccounts: (sessionId: string | undefined) => ({ data: sessionId ? { claude: "acc-a" } : undefined }),
		useAgentAccountActions: () => ({ switchSession, refreshSession }),
		useAgentAccountLabel: () => (id: string | undefined) => accounts.find((account) => account.id === id)?.email ?? "Removed account",
	};
});
vi.mock("../hooks/useSessionInterfaceTransition", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useSessionInterfaceTransition")>();
	return { ...actual, useSessionInterfaceTransitionStatus: () => ({ transition }) };
});

const base: WorkspaceSession = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "tp/sess-1",
	status: "working",
	updatedAt: "2026-06-10T00:00:00Z",
	prs: [],
};

function renderMenu(session: WorkspaceSession) {
	return render(<TooltipProvider><TopbarAccountMenu session={session} /></TooltipProvider>);
}

beforeEach(() => {
	switchSession.mockReset().mockResolvedValue(undefined);
	refreshSession.mockReset();
	transition = undefined;
});

describe("TopbarAccountMenu", () => {
	it("shows the session's account in the top bar", () => {
		renderMenu(base);
		const trigger = screen.getByTestId("topbar-account-menu");
		expect(trigger).toHaveAccessibleName("AI account: alice@example.com");
		expect(trigger).toHaveTextContent("alice");
	});

	it("moves an idle session to the picked account at once", async () => {
		renderMenu({ ...base, activity: { state: "idle", lastActivityAt: "2026-06-10T00:00:00Z" } });
		await userEvent.click(screen.getByTestId("topbar-account-menu"));
		// A signed-out account can't take the session.
		expect(screen.getByRole("menuitemradio", { name: "carol@example.com" })).toHaveAttribute("aria-disabled", "true");
		await userEvent.click(screen.getByRole("menuitemradio", { name: "bob@example.com" }));
		expect(switchSession).toHaveBeenCalledWith("sess-1", "acc-b", false);
	});

	it("asks before stopping a working agent's turn, then moves with stopTurn", async () => {
		renderMenu({ ...base, activity: { state: "active", lastActivityAt: "2026-06-10T00:00:00Z" } });
		await userEvent.click(screen.getByTestId("topbar-account-menu"));
		await userEvent.click(screen.getByRole("menuitemradio", { name: "bob@example.com" }));
		expect(switchSession).not.toHaveBeenCalled();
		expect(screen.getByText("Stop the turn and move?")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Stop and move" }));
		await waitFor(() => expect(switchSession).toHaveBeenCalledWith("sess-1", "acc-b", true));
	});

	it("disables the trigger while a move runs", () => {
		transition = { id: "t1", phase: "draining", targetAccountId: "acc-b", sourceAccountId: "acc-a", updatedAt: "2026-06-10T00:00:00Z" };
		renderMenu(base);
		expect(screen.getByTestId("topbar-account-menu")).toBeDisabled();
	});

	it("is hidden for a harness without accounts and for an ended session", () => {
		const other = renderMenu({ ...base, provider: "opencode" as WorkspaceSession["provider"] });
		expect(screen.queryByTestId("topbar-account-menu")).not.toBeInTheDocument();
		other.unmount();
		renderMenu({ ...base, isTerminated: true });
		expect(screen.queryByTestId("topbar-account-menu")).not.toBeInTheDocument();
	});

	it("shortens an email to its local part", () => {
		expect(topbarAccountLabel("alice@example.com")).toBe("alice");
		expect(topbarAccountLabel("OAuth token")).toBe("OAuth token");
	});
});
