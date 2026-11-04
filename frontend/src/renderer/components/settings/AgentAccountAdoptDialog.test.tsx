import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { AgentAccountsPanel } from "./AgentAccountsSection";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../../lib/api-client", () => ({
	apiClient: { DELETE: vi.fn(), GET: getMock, POST: postMock },
	apiErrorMessage: (error: unknown) => error instanceof Error ? error.message : "request failed",
}));

vi.mock("../TerminalPane", () => ({ TerminalPane: () => <div /> }));

const device = { id: "device-claude", provider: "claude", device: true, default: true, signedIn: true, email: "a@example.com", historySplit: [] };
const target = { id: "acct-b", provider: "claude", device: false, default: false, signedIn: true, email: "b@example.com", historySplit: [] };
const signedOut = { id: "acct-c", provider: "claude", device: false, default: false, signedIn: false, email: "c@example.com", historySplit: [] };
const sessions = [
	{ sessionId: "s1", title: "Fix login", isOrchestrator: false, running: true, activity: "active", accountId: "device-claude" },
	{ sessionId: "s2", title: "Write docs", isOrchestrator: false, running: false, activity: "exited", accountId: "device-claude" },
	{ sessionId: "o1", title: "Project orchestrator", isOrchestrator: true, running: true, activity: "idle", accountId: "device-claude" },
];
const batch = (state: string, items: Record<string, unknown>[]) => ({ id: "batch-1", accountId: "acct-b", policy: "wait", state, items });

function renderPanel() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><TooltipProvider><AgentAccountsPanel provider="claude" /></TooltipProvider></QueryClientProvider>);
}

let adoptionState = batch("running", []);

beforeEach(() => {
	adoptionState = batch("running", []);
	getMock.mockReset().mockImplementation((path: string) => {
		if (path === "/api/v1/agents/accounts/{accountId}/adopt/preview") return Promise.resolve({ data: { accountId: "acct-b", sessions } });
		if (path === "/api/v1/agents/accounts/{accountId}/adoptions/{batchId}") return Promise.resolve({ data: adoptionState });
		return Promise.resolve({ data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [device, target, signedOut] } });
	});
	postMock.mockReset().mockResolvedValue({ data: batch("running", [
		{ sessionId: "s1", state: "moving", isOrchestrator: false },
		{ sessionId: "s2", state: "rebound", isOrchestrator: false },
		{ sessionId: "o1", state: "queued", isOrchestrator: true },
	]) });
});

const rowOf = async (id: string) => waitFor(() => {
	const row = document.querySelector<HTMLElement>(`[data-agent-account="${id}"]`);
	if (!row) throw new Error(`no row ${id}`);
	return row;
});

it("offers the switch on signed-in Claude rows only", async () => {
	renderPanel();
	expect(within(await rowOf("acct-b")).getByRole("button", { name: "Use for all sessions" })).toBeInTheDocument();
	expect(within(await rowOf("acct-c")).queryByRole("button", { name: "Use for all sessions" })).not.toBeInTheDocument();
});

it("lists the affected sessions and starts the switch once, then shows each outcome", async () => {
	const user = userEvent.setup();
	renderPanel();
	await user.click(within(await rowOf("acct-b")).getByRole("button", { name: "Use for all sessions" }));

	const dialog = await screen.findByRole("dialog");
	expect(await within(dialog).findByText("Fix login")).toBeInTheDocument();
	expect(within(dialog).getByText("Write docs")).toBeInTheDocument();
	expect(within(dialog).getByText("Project orchestrator")).toBeInTheDocument();
	expect(within(dialog).getByText("moves last")).toBeInTheDocument();
	expect(within(dialog).getByText(/Sonnet and Haiku thinking from the old account is dropped/)).toBeInTheDocument();
	await user.click(within(dialog).getByRole("radio", { name: "Stop its turn now" }));

	const confirm = within(dialog).getByRole("button", { name: "Switch sessions" });
	// Two clicks in one render pass: one request.
	act(() => { fireEvent.click(confirm); fireEvent.click(confirm); });

	const progress = await screen.findByTestId("agent-account-adopt-progress");
	const adoptCalls = postMock.mock.calls.filter(([path]) => path === "/api/v1/agents/accounts/{accountId}/adopt");
	expect(adoptCalls).toHaveLength(1);
	expect(adoptCalls[0][1]).toMatchObject({ params: { path: { accountId: "acct-b" } }, body: { policy: "interrupt", requestKey: expect.any(String) } });
	expect(within(progress).getByText("Fix login").closest("li")).toHaveAttribute("data-state", "moving");
	expect(within(progress).getByText("Write docs").closest("li")).toHaveAttribute("data-state", "rebound");
});

it("shows a busy session as waiting and a refused one with its reason", async () => {
	const user = userEvent.setup();
	adoptionState = batch("completed", [
		{ sessionId: "s1", state: "waiting", isOrchestrator: false },
		{ sessionId: "s2", state: "failed", isOrchestrator: false, errorCode: "NATIVE_SESSION_MISSING", errorMessage: "the account's folder cannot see this conversation" },
		{ sessionId: "o1", state: "moved", isOrchestrator: true },
	]);
	postMock.mockResolvedValue({ data: adoptionState });
	renderPanel();
	await user.click(within(await rowOf("acct-b")).getByRole("button", { name: "Use for all sessions" }));
	const dialog = await screen.findByRole("dialog");
	await within(dialog).findByText("Fix login");
	await user.click(within(dialog).getByRole("button", { name: "Switch sessions" }));

	const progress = await screen.findByTestId("agent-account-adopt-progress");
	expect(within(progress).getByText("Busy, waiting for its turn")).toBeInTheDocument();
	expect(within(progress).getByText("the account's folder cannot see this conversation")).toBeInTheDocument();
	expect(within(progress).getByText("All sessions are on this account.")).toBeInTheDocument();
});
