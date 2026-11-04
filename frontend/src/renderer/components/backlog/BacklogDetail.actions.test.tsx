import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogItem as item, daysAgo } from "../../test/backlog-fixtures";
import { renderBacklog, resetBacklogApi, workspaceData } from "../../test/backlog-harness";
import type { BacklogItem } from "../../lib/backlog-api";
import { loadChatSessionDraft } from "../../lib/chat-drafts";
import { useUiStore } from "../../stores/ui-store";
import type { WorkspaceSession, WorkspaceSummary } from "../../types/workspace";
import { BacklogView } from "./BacklogView";

vi.mock("../../lib/backlog-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-api")>()), ...(await import("../../test/backlog-harness")).backlogApi }));
vi.mock("../../hooks/useWorkspaceQuery", async (importActual) => {
	const harness = await import("../../test/backlog-harness");
	return { ...(await importActual<typeof import("../../hooks/useWorkspaceQuery")>()), useWorkspaceQuery: () => ({ data: harness.workspaceData.current }) };
});

const session = (id: string, extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
	id,
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title: `title ${id}`,
	provider: "claude-code",
	status: "working",
	updatedAt: daysAgo(0),
	prs: [],
	...extra,
});
const orchestrator = session("proj-1-orch", { kind: "orchestrator", createdAt: "2026-10-01T00:00:00Z", title: "Orchestrator" });
const withSessions = (...sessions: WorkspaceSession[]): WorkspaceSummary[] => [{ id: "proj-1", name: "Project One", path: "/p1", sessions }];

function open(selected: BacklogItem, onOpenSession = vi.fn()) {
	resetBacklogApi([selected]);
	renderBacklog(<BacklogView projectId="proj-1" itemId={selected.id} onProjectChange={vi.fn()} onItemChange={vi.fn()} onOpenSession={onOpenSession} />);
	return onOpenSession;
}

beforeEach(() => {
	workspaceData.current = withSessions(orchestrator);
	useUiStore.setState({ newTaskRequest: null, provisioningProjectIds: new Set() });
	window.localStorage.clear();
});

describe("Start worker", () => {
	it("opens New Task for the item's project with the item to start on", async () => {
		open(item({ ref: "B12", title: "Fix the leak" }));
		await userEvent.click(await screen.findByRole("button", { name: "Start worker" }));
		expect(useUiStore.getState().newTaskRequest).toMatchObject({ projectId: "proj-1", backlog: { ref: "B12", title: "Fix the leak" } });
		expect(useUiStore.getState().newTaskRequest?.backlog).not.toHaveProperty("force");
	});

	it("asks before starting a held item, then forces it", async () => {
		open(item({ ref: "B7", title: "Held thing", hold: "after the rename" }));
		await userEvent.click(await screen.findByRole("button", { name: "Start worker" }));
		expect(useUiStore.getState().newTaskRequest).toBeNull();
		const dialog = await screen.findByRole("dialog");
		expect(dialog).toHaveTextContent("Start B7 anyway?");
		expect(dialog).toHaveTextContent("after the rename");
		await userEvent.click(screen.getAllByRole("button", { name: "Start worker" }).at(-1) as HTMLElement);
		expect(useUiStore.getState().newTaskRequest?.backlog).toMatchObject({ ref: "B7", force: true });
	});

	it("is disabled for a parent, with the reason", async () => {
		open(item({ ref: "B1", childCount: 3 }));
		const start = await screen.findByRole("button", { name: "Start worker" });
		expect(start).toBeDisabled();
		await userEvent.hover(start.parentElement as HTMLElement);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Running a parent runs its children");
		expect(useUiStore.getState().newTaskRequest).toBeNull();
	});

	it("is not offered for an inbox item, which is accepted first", async () => {
		open(item({ ref: "B3", state: "inbox" }));
		await screen.findByRole("button", { name: "Accept" });
		expect(screen.queryByRole("button", { name: "Start worker" })).not.toBeInTheDocument();
	});
});

describe("Discuss in chat and Send to orchestrator", () => {
	const draftOf = () => loadChatSessionDraft({ sessionId: "proj-1-orch", incarnation: "2026-10-01T00:00:00Z" });

	it("fills the orchestrator's draft with the whole item, opens that chat, and sends nothing", async () => {
		const fetchSpy = vi.spyOn(globalThis, "fetch");
		const onOpenSession = open(item({
			ref: "B12",
			title: "Fix the leak",
			note: "see sessions.go",
			doneWhen: "no leak",
			agentNotes: "use the pool",
			fields: { area: "sessions" },
			links: [{ kind: "pr", ref: "https://x/pull/1", display: "https://x/pull/1", createdAt: daysAgo(1) }],
			origin: { kind: "worker", sessionId: "w-1", excerpt: "leaks on restart" },
		}));
		await userEvent.click(await screen.findByRole("button", { name: "Discuss in chat" }));
		const loaded = draftOf();
		expect(loaded.ok).toBe(true);
		const text = loaded.draft.composer.text;
		for (const want of ["B12", "Fix the leak", "see sessions.go", "no leak", "use the pool", "area: sessions", "https://x/pull/1", "leaks on restart"]) expect(text).toContain(want);
		expect(onOpenSession).toHaveBeenCalledWith("proj-1", "proj-1-orch");
		expect(fetchSpy).not.toHaveBeenCalled();
		fetchSpy.mockRestore();
	});

	it("fills 'Do B12' for Send to orchestrator and keeps text already in the draft", async () => {
		const onOpenSession = open(item({ ref: "B12" }));
		await userEvent.click(await screen.findByRole("button", { name: "Discuss in chat" }));
		await userEvent.click(screen.getByRole("button", { name: "Send to orchestrator" }));
		const text = draftOf().draft.composer.text;
		expect(text.startsWith("Let's talk through backlog item B12")).toBe(true);
		expect(text.endsWith("\n\nDo B12")).toBe(true);
		expect(onOpenSession).toHaveBeenCalledTimes(2);
	});

	it("is disabled with the reason when the project has no running orchestrator", async () => {
		workspaceData.current = withSessions(session("w-1"));
		const onOpenSession = open(item({ ref: "B12" }));
		const discuss = await screen.findByRole("button", { name: "Discuss in chat" });
		expect(discuss).toBeDisabled();
		expect(screen.getByRole("button", { name: "Send to orchestrator" })).toBeDisabled();
		await userEvent.hover(discuss.parentElement as HTMLElement);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("This project has no running orchestrator.");
		expect(onOpenSession).not.toHaveBeenCalled();
	});

	it("offers Discuss for an inbox item but not Send", async () => {
		open(item({ ref: "B3", state: "inbox" }));
		await screen.findByRole("button", { name: "Discuss in chat" });
		expect(screen.queryByRole("button", { name: "Send to orchestrator" })).not.toBeInTheDocument();
	});
});

describe("Open session from the active badge", () => {
	const link = (ref: string) => ({ kind: "session", ref, display: ref, createdAt: daysAgo(1) });
	const active = { active: true, inReview: false, landed: false };

	it("opens the one running linked session", async () => {
		workspaceData.current = withSessions(orchestrator, session("w-1"));
		const onOpenSession = open(item({ ref: "B1", badges: active, links: [link("w-1")] }));
		await userEvent.click(await screen.findByRole("button", { name: "Open session title w-1" }));
		expect(onOpenSession).toHaveBeenCalledWith("proj-1", "w-1");
	});

	it("offers a menu when several linked sessions are running, skipping ended ones", async () => {
		workspaceData.current = withSessions(orchestrator, session("w-1"), session("w-2"), session("w-3", { isTerminated: true }));
		const onOpenSession = open(item({ ref: "B1", badges: active, links: [link("w-1"), link("w-2"), link("w-3")] }));
		await userEvent.click(await screen.findByRole("button", { name: "Open a running session" }));
		expect(screen.queryByRole("menuitem", { name: "title w-3" })).not.toBeInTheDocument();
		await userEvent.click(await screen.findByRole("menuitem", { name: "title w-2" }));
		expect(onOpenSession).toHaveBeenCalledWith("proj-1", "w-2");
	});

	it("shows a plain glyph when the app does not know a running session", async () => {
		open(item({ ref: "B1", badges: active, links: [link("unknown")] }));
		await waitFor(() => expect(screen.getByRole("article")).toBeInTheDocument());
		expect(screen.getAllByRole("img", { name: "A linked session is working" }).length).toBeGreaterThan(0);
		expect(screen.queryByRole("button", { name: /Open session|Open a running session/ })).not.toBeInTheDocument();
	});
});
