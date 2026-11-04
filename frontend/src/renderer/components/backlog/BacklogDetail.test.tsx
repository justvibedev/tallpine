import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogItem as item, daysAgo } from "../../test/backlog-fixtures";
import { backlogApi, renderBacklog, resetBacklogApi, workspaceData } from "../../test/backlog-harness";
import { useUiStore } from "../../stores/ui-store";
import type { BacklogItem } from "../../lib/backlog-api";
import { GlobalToast } from "../GlobalToast";
import { BacklogView } from "./BacklogView";

vi.mock("../../lib/backlog-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-api")>()), ...(await import("../../test/backlog-harness")).backlogApi }));
vi.mock("../../hooks/useWorkspaceQuery", async (importActual) => {
	const harness = await import("../../test/backlog-harness");
	return { ...(await importActual<typeof import("../../hooks/useWorkspaceQuery")>()), useWorkspaceQuery: () => ({ data: harness.workspaceData.current }) };
});
const openExternal = vi.fn();
vi.mock("../../lib/bridge", async (importActual) => {
	const actual = await importActual<typeof import("../../lib/bridge")>();
	return { ...actual, tallpineBridge: { ...actual.tallpineBridge, app: { ...actual.tallpineBridge.app, openExternal: (url: string) => openExternal(url) } } };
});

const PR_URL = "https://github.com/o/r/pull/7";

function open(selected: BacklogItem, others: BacklogItem[] = [], full?: BacklogItem, onOpenSession = vi.fn()) {
	resetBacklogApi([selected, ...others], { [selected.ref]: full ?? selected });
	renderBacklog(
		<>
			<BacklogView projectId="proj-1" itemId={selected.id} onProjectChange={vi.fn()} onItemChange={vi.fn()} onOpenSession={onOpenSession} />
			<GlobalToast />
		</>,
	);
	return onOpenSession;
}
const edit = async (field: string) => userEvent.click(await screen.findByRole("button", { name: `Edit ${field}` }));

beforeEach(() => {
	openExternal.mockReset();
	useUiStore.getState().clearGlobalToast();
	workspaceData.current = [
		{
			id: "proj-1",
			name: "Project One",
			path: "/p1",
			sessions: [
				{
					id: "worker-7",
					workspaceId: "proj-1",
					workspaceName: "Project One",
					title: "fix leak worker",
					provider: "claude-code",
					status: "working",
					updatedAt: daysAgo(0),
					prs: [{ url: PR_URL, number: 7, state: "open", ci: "passing", review: "none", mergeability: "clean", reviewComments: false, updatedAt: daysAgo(0) }],
				},
			],
		},
	];
});

describe("detail pane content", () => {
	it("shows the item with markdown, origin and excerpt, links with live status, collapsed agent notes and fields", async () => {
		const selected = item({
			ref: "B1",
			title: "Rune-safe truncation",
			note: "## Plan\n\nCut by **rune**.",
			doneWhen: "No garbled previews",
			scope: "pr",
			hold: "after the rename",
			origin: { kind: "worker", sessionId: "worker-131", excerpt: "truncate(note, 2048) cuts mid-rune" },
			agentNotes: "Use utf8.DecodeRune.",
			fields: { area: "reports", branch: "fix/rune" },
			links: [
				{ kind: "session", ref: "worker-7", display: "worker-7", createdAt: daysAgo(1) },
				{ kind: "pr", ref: PR_URL, display: PR_URL, createdAt: daysAgo(1) },
				{ kind: "blocked_by", ref: "item-x", display: "B9", createdAt: daysAgo(1) },
			],
		});
		open(selected);
		const detail = await screen.findByRole("article", { name: /B1 Rune-safe truncation/ });
		expect(within(detail).getByRole("heading", { name: "Rune-safe truncation" })).toBeInTheDocument();
		expect(within(detail).getByRole("heading", { name: "Plan" })).toBeInTheDocument();
		expect(within(detail).getByText("rune", { selector: "strong" })).toBeInTheDocument();
		expect(within(detail).getByText("No garbled previews")).toBeInTheDocument();
		expect(within(detail).getByText("Held: after the rename")).toBeInTheDocument();
		expect(within(detail).getByText("Blocked by B9")).toBeInTheDocument();
		expect(within(detail).getByText(/Worker report/)).toBeInTheDocument();
		expect(within(detail).getByText("truncate(note, 2048) cuts mid-rune")).toBeInTheDocument();
		// Live status for the linked session and PR comes from the workspace data.
		expect(within(detail).getByRole("button", { name: "fix leak worker" })).toBeInTheDocument();
		expect(within(detail).getByRole("button", { name: "PR #7" })).toBeInTheDocument();
		// Fields table.
		expect(within(detail).getByLabelText("Value of area")).toHaveValue("reports");
		// Agent notes are collapsed but one click away.
		const notesToggle = within(detail).getByRole("button", { name: "Agent notes" });
		expect(notesToggle).toHaveAttribute("aria-expanded", "false");
		expect(within(detail).getByText("Use utf8.DecodeRune.")).not.toBeVisible();
		await userEvent.click(notesToggle);
		expect(within(detail).getByText("Use utf8.DecodeRune.")).toBeVisible();
	});

	it("falls back to display text for a link the app cannot resolve", async () => {
		open(item({ ref: "B1", links: [{ kind: "session", ref: "gone-1", display: "gone-1", createdAt: daysAgo(1) }, { kind: "issue", ref: "#12", display: "#12", createdAt: daysAgo(1) }] }));
		expect(await screen.findByText("gone-1")).toBeInTheDocument();
		expect(screen.getByText("#12")).toBeInTheDocument();
	});

	it("opens a linked session and a PR from the links", async () => {
		const onOpenSession = open(item({ ref: "B1", links: [{ kind: "session", ref: "worker-7", display: "worker-7", createdAt: daysAgo(1) }, { kind: "pr", ref: PR_URL, display: PR_URL, createdAt: daysAgo(1) }] }));
		await userEvent.click(await screen.findByRole("button", { name: "fix leak worker" }));
		expect(onOpenSession).toHaveBeenCalledWith("proj-1", "worker-7");
		await userEvent.click(screen.getByRole("button", { name: "PR #7" }));
		expect(openExternal).toHaveBeenCalledWith(PR_URL);
	});
});

describe("detail edits", () => {
	it("saves only the changed field", async () => {
		open(item({ ref: "B1", title: "Old title", note: "old note", doneWhen: "old done" }));
		await edit("Title");
		const title = screen.getByRole("textbox", { name: "Title" });
		await userEvent.clear(title);
		await userEvent.type(title, "New title{Enter}");
		await waitFor(() => expect(backlogApi.updateBacklogItem).toHaveBeenCalledWith("proj-1", "B1", { title: "New title" }));
		expect(backlogApi.updateBacklogItem).toHaveBeenCalledTimes(1);

		await edit("Note");
		const note = screen.getByRole("textbox", { name: "Note" });
		await userEvent.clear(note);
		await userEvent.type(note, "fresh note");
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(backlogApi.updateBacklogItem).toHaveBeenLastCalledWith("proj-1", "B1", { note: "fresh note" }));

		await edit("Done when");
		const done = screen.getByRole("textbox", { name: "Done when" });
		await userEvent.clear(done);
		await userEvent.type(done, "shipped");
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(backlogApi.updateBacklogItem).toHaveBeenLastCalledWith("proj-1", "B1", { doneWhen: "shipped" }));
	});

	it("does not save an unchanged value, and Escape cancels", async () => {
		open(item({ ref: "B1", title: "Same", note: "keep" }));
		await edit("Note");
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		expect(backlogApi.updateBacklogItem).not.toHaveBeenCalled();
		await edit("Note");
		await userEvent.type(screen.getByRole("textbox", { name: "Note" }), " changed{Escape}");
		expect(screen.queryByRole("textbox", { name: "Note" })).not.toBeInTheDocument();
		expect(backlogApi.updateBacklogItem).not.toHaveBeenCalled();
	});

	it("keeps the editor open and shows the message when the server refuses", async () => {
		open(item({ ref: "B1", title: "Same" }));
		backlogApi.updateBacklogItem.mockRejectedValue(new Error("title too long"));
		await edit("Title");
		await userEvent.type(screen.getByRole("textbox", { name: "Title" }), " more{Enter}");
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't save: title too long");
		expect(screen.getByRole("textbox", { name: "Title" })).toBeInTheDocument();
	});

	it("edits agent notes after expanding them and sets the scope", async () => {
		open(item({ ref: "B1", agentNotes: "old notes" }));
		await userEvent.click(await screen.findByRole("button", { name: "Agent notes" }));
		await edit("Agent notes");
		const notes = screen.getByRole("textbox", { name: "Agent notes" });
		await userEvent.clear(notes);
		await userEvent.type(notes, "new notes");
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(backlogApi.updateBacklogItem).toHaveBeenCalledWith("proj-1", "B1", { agentNotes: "new notes" }));

		await userEvent.click(screen.getByRole("combobox", { name: "Scope" }));
		await userEvent.click(await screen.findByRole("option", { name: "PR and merge" }));
		await waitFor(() => expect(backlogApi.updateBacklogItem).toHaveBeenLastCalledWith("proj-1", "B1", { scope: "pr-merge" }));
	});

	it("adds, edits and clears custom fields", async () => {
		open(item({ ref: "B1", fields: { area: "reports" } }));
		await userEvent.type(await screen.findByLabelText("Key"), "worktree");
		await userEvent.type(screen.getByLabelText("Value"), "/tmp/wt");
		await userEvent.click(screen.getByRole("button", { name: "Add field" }));
		await waitFor(() => expect(backlogApi.setBacklogField).toHaveBeenCalledWith("proj-1", "B1", "worktree", "/tmp/wt"));

		const area = screen.getByLabelText("Value of area");
		await userEvent.clear(area);
		await userEvent.type(area, "sessions");
		await userEvent.tab();
		await waitFor(() => expect(backlogApi.setBacklogField).toHaveBeenLastCalledWith("proj-1", "B1", "area", "sessions"));

		await userEvent.click(screen.getByRole("button", { name: "Remove field area" }));
		await waitFor(() => expect(backlogApi.clearBacklogField).toHaveBeenCalledWith("proj-1", "B1", "area"));
	});

	it("rejects a malformed field key without calling the API", async () => {
		open(item({ ref: "B1" }));
		await userEvent.type(await screen.findByLabelText("Key"), "Not Valid");
		await userEvent.type(screen.getByLabelText("Value"), "x");
		await userEvent.click(screen.getByRole("button", { name: "Add field" }));
		expect(backlogApi.setBacklogField).not.toHaveBeenCalled();
	});

	it("adds and removes links", async () => {
		open(item({ ref: "B1", links: [{ kind: "memory", ref: "mem-1", display: "mem-1", createdAt: daysAgo(1) }] }));
		await userEvent.type(await screen.findByLabelText("Link target"), PR_URL);
		await userEvent.click(screen.getByRole("button", { name: "Add link" }));
		await waitFor(() => expect(backlogApi.addBacklogLink).toHaveBeenCalledWith("proj-1", "B1", "pr", PR_URL));

		await userEvent.click(screen.getByRole("button", { name: "Remove link mem-1" }));
		await waitFor(() => expect(backlogApi.removeBacklogLink).toHaveBeenCalledWith("proj-1", "B1", "memory", "mem-1"));
	});

	it("adds a blocker with the chosen link type", async () => {
		open(item({ ref: "B1" }));
		await userEvent.click(await screen.findByRole("combobox", { name: "Link type" }));
		await userEvent.click(await screen.findByRole("option", { name: "Blocked by" }));
		await userEvent.type(screen.getByLabelText("Link target"), "B3");
		await userEvent.click(screen.getByRole("button", { name: "Add link" }));
		await waitFor(() => expect(backlogApi.addBacklogLink).toHaveBeenCalledWith("proj-1", "B1", "blocked_by", "B3"));
	});
});

describe("detail actions", () => {
	it("marks an open item done and Undo puts it back", async () => {
		open(item({ ref: "B1", state: "open" }));
		await userEvent.click(await screen.findByRole("button", { name: "Mark done" }));
		await waitFor(() => expect(backlogApi.setBacklogState).toHaveBeenCalledWith("proj-1", "B1", "done", undefined));
		expect(await screen.findByText("B1 marked done")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Undo" }));
		await waitFor(() => expect(backlogApi.setBacklogState).toHaveBeenLastCalledWith("proj-1", "B1", "open", undefined));
		expect(screen.queryByText("B1 marked done")).not.toBeInTheDocument();
	});

	it("drops an item with an undo that restores its previous state", async () => {
		open(item({ ref: "B1", state: "inbox" }));
		await userEvent.click(await screen.findByRole("button", { name: "Drop" }));
		await waitFor(() => expect(backlogApi.setBacklogState).toHaveBeenCalledWith("proj-1", "B1", "dropped", undefined));
		await userEvent.click(await screen.findByRole("button", { name: "Undo" }));
		await waitFor(() => expect(backlogApi.setBacklogState).toHaveBeenLastCalledWith("proj-1", "B1", "inbox", undefined));
	});

	it("accepts an inbox item and offers Undo back to the inbox", async () => {
		open(item({ ref: "B1", state: "inbox", origin: { kind: "worker", sessionId: "s1" } }));
		expect(screen.queryByRole("button", { name: "Mark done" })).not.toBeInTheDocument();
		await userEvent.click(await screen.findByRole("button", { name: "Accept" }));
		await waitFor(() => expect(backlogApi.setBacklogState).toHaveBeenCalledWith("proj-1", "B1", "open", undefined));
		expect(await screen.findByText("B1 accepted")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Undo" }));
		await waitFor(() => expect(backlogApi.setBacklogState).toHaveBeenLastCalledWith("proj-1", "B1", "inbox", undefined));
	});

	it("reopens a closed item", async () => {
		open(item({ ref: "B1", state: "done", closedAt: daysAgo(1) }));
		await userEvent.click(await screen.findByRole("button", { name: "Reopen" }));
		await waitFor(() => expect(backlogApi.setBacklogState).toHaveBeenCalledWith("proj-1", "B1", "open", undefined));
		expect(screen.queryByRole("button", { name: "Mark done" })).not.toBeInTheDocument();
	});

	it("holds with a reason and releases the hold", async () => {
		open(item({ ref: "B1" }));
		await userEvent.click(await screen.findByRole("button", { name: "Hold" }));
		const dialog = await screen.findByRole("dialog");
		await userEvent.type(within(dialog).getByLabelText("Reason for holding"), "after the rename");
		await userEvent.click(within(dialog).getByRole("button", { name: "Hold" }));
		await waitFor(() => expect(backlogApi.updateBacklogItem).toHaveBeenCalledWith("proj-1", "B1", { hold: "after the rename" }));
	});

	it("releases an existing hold in one click", async () => {
		open(item({ ref: "B1", hold: "later" }));
		await userEvent.click(await screen.findByRole("button", { name: "Release hold" }));
		await waitFor(() => expect(backlogApi.updateBacklogItem).toHaveBeenCalledWith("proj-1", "B1", { hold: "" }));
	});

	it("shows an error toast when the state change fails and offers no Undo", async () => {
		open(item({ ref: "B1" }));
		backlogApi.setBacklogState.mockRejectedValue(new Error("conflict"));
		await userEvent.click(await screen.findByRole("button", { name: "Mark done" }));
		expect(await screen.findByText("Couldn't update B1")).toBeInTheDocument();
		expect(screen.getByText("conflict")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Undo" })).not.toBeInTheDocument();
	});
});
