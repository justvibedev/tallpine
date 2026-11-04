import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogItem as item, daysAgo } from "../../test/backlog-fixtures";
import { backlogApi, renderBacklog, resetBacklogApi, workspaceData } from "../../test/backlog-harness";
import { BacklogView, type BacklogViewProps } from "./BacklogView";

vi.mock("../../lib/backlog-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-api")>()), ...(await import("../../test/backlog-harness")).backlogApi }));
vi.mock("../../hooks/useWorkspaceQuery", async (importActual) => {
	const harness = await import("../../test/backlog-harness");
	return { ...(await importActual<typeof import("../../hooks/useWorkspaceQuery")>()), useWorkspaceQuery: () => ({ data: harness.workspaceData.current }) };
});

const noop = () => undefined;
function view(overrides: Partial<BacklogViewProps> = {}) {
	return renderBacklog(<BacklogView projectId="proj-1" itemId={undefined} onProjectChange={noop} onItemChange={noop} onOpenSession={noop} {...overrides} />);
}
const rowNames = () => screen.getAllByRole("button", { name: /^B\d+, / }).map((row) => row.getAttribute("aria-label")?.split(",")[0]);

beforeEach(() => {
	workspaceData.current = [
		{ id: "proj-1", name: "Project One", path: "/p1", sessions: [] },
		{ id: "proj-2", name: "Project Two", path: "/p2", sessions: [] },
	];
});

describe("BacklogView groups", () => {
	it("shows Inbox, Open, Held and a muted stale row, with children nested and progress on the parent", async () => {
		const parent = item({ id: "par", ref: "B1", title: "Big feature", position: 1, childCount: 2, childClosedCount: 1 });
		resetBacklogApi([
			parent,
			item({ ref: "B2", title: "Part one", parentId: "par", parentRef: "B1", position: 1 }),
			item({ ref: "B3", title: "Part two", parentId: "par", parentRef: "B1", state: "done", closedAt: daysAgo(1), position: 2 }),
			item({ ref: "B4", title: "From an agent", state: "inbox", origin: { kind: "worker", sessionId: "s1" } }),
			item({ ref: "B5", title: "Later", hold: "after the rename" }),
			item({ ref: "B6", title: "Forgotten", updatedAt: daysAgo(90) }),
		]);
		view();
		expect(await screen.findByRole("region", { name: "Inbox" })).toHaveTextContent("B4");
		expect(screen.getByRole("region", { name: "Open" })).toHaveTextContent("B1");
		expect(within(screen.getByRole("region", { name: "Open" })).getByText("1/2 done")).toBeInTheDocument();
		// Children nest inside the parent's list item.
		const parentRow = screen.getByRole("button", { name: /^B1, / });
		expect(parentRow.closest("li")).toContainElement(screen.getByRole("button", { name: /^B2, / }));
		expect(screen.getByRole("region", { name: "Held" })).toHaveTextContent("B5");
		const stale = screen.getByRole("button", { name: /1 item untouched for 60\+ days/ });
		expect(stale).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("Forgotten")).not.toBeInTheDocument();
		await userEvent.click(stale);
		expect(screen.getByText("Forgotten")).toBeInTheDocument();
	});

	it("lists open items in drag order and shows what blocks an item", async () => {
		resetBacklogApi([
			item({ ref: "B1", title: "Second", position: 2 }),
			item({ ref: "B2", title: "First", position: 1 }),
			item({ ref: "B3", title: "Blocked", position: 3, linkCounts: { blocked_by: 2 } }),
		]);
		view();
		await screen.findByRole("region", { name: "Open" });
		expect(rowNames()).toEqual(["B2", "B1", "B3"]);
		expect(screen.getByText("blocked by 2")).toBeInTheDocument();
	});

	it("shows the archive only behind its filter", async () => {
		resetBacklogApi([item({ ref: "B1", title: "Live" }), item({ ref: "B2", title: "Shipped", state: "done", closedAt: daysAgo(1) }), item({ ref: "B3", title: "Nope", state: "dropped", closedAt: daysAgo(2) })]);
		view();
		await screen.findByText("Live");
		expect(screen.queryByText("Shipped")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Archive" }));
		expect(screen.getByText("Shipped")).toBeInTheDocument();
		expect(screen.getByText("Nope")).toBeInTheDocument();
		expect(screen.queryByText("Live")).not.toBeInTheDocument();
	});

	it("renders the derived badge as a named glyph", async () => {
		resetBacklogApi([item({ ref: "B1", badges: { active: true, inReview: true, landed: false } }), item({ ref: "B2", badges: { active: false, inReview: false, landed: true } })]);
		view();
		await screen.findByRole("region", { name: "Open" });
		// One glyph per row, working wins over review.
		expect(screen.getAllByRole("img", { name: "A linked session is working" })).toHaveLength(1);
		expect(screen.queryByRole("img", { name: "A linked PR is in review" })).not.toBeInTheDocument();
		expect(screen.getAllByRole("img", { name: "The linked PR merged" })).toHaveLength(1);
	});

	it("filters by search text", async () => {
		resetBacklogApi([item({ ref: "B1", title: "Fix the leak" }), item({ ref: "B2", title: "Rename things" })]);
		view();
		await screen.findByText("Fix the leak");
		await userEvent.type(screen.getByRole("searchbox", { name: "Search backlog" }), "rename");
		expect(screen.queryByText("Fix the leak")).not.toBeInTheDocument();
		expect(screen.getByText("Rename things")).toBeInTheDocument();
	});

	it("lists the current project, or every project with its name when none is chosen", async () => {
		resetBacklogApi([item({ ref: "B1", title: "In one", projectId: "proj-1" }), item({ ref: "B1", id: "other", title: "In two", projectId: "proj-2" })]);
		const { unmount } = view({ projectId: "proj-1" });
		await screen.findByText("In one");
		expect(screen.queryByText("In two")).not.toBeInTheDocument();
		expect(backlogApi.listBacklog).not.toHaveBeenCalledWith("proj-2", expect.anything());
		unmount();
		view({ projectId: undefined });
		await screen.findByText("In two");
		expect(screen.getByText("In one")).toBeInTheDocument();
		expect(screen.getByText("Project Two")).toBeInTheDocument();
	});

	it("keeps the empty state to one sentence and shows a retry on error", async () => {
		resetBacklogApi([]);
		view();
		expect(await screen.findByText(/Nothing in the backlog/)).toBeInTheDocument();
	});

	it("shows an inline error with retry when loading fails", async () => {
		resetBacklogApi([]);
		backlogApi.listBacklog.mockRejectedValue(new Error("boom"));
		view();
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load the backlog.");
		backlogApi.listBacklog.mockImplementation(async (_project: string, state: string) => (state === "open" ? [item({ ref: "B1", title: "Back again" })] : []));
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("Back again")).toBeInTheDocument();
	});
});

describe("BacklogView truncation", () => {
	const closed = (count: number) => Array.from({ length: count }, (_, index) => item({ ref: `B${index + 1}`, id: `closed-${index}`, state: "done", closedAt: daysAgo(1) }));

	it("says so when a list fills the API page, so a clipped archive is not mistaken for the whole", async () => {
		resetBacklogApi(closed(500));
		view();
		expect(await screen.findByText(/Showing the first 500 items of a list/)).toBeInTheDocument();
	});

	it("stays quiet when every list fits", async () => {
		resetBacklogApi([...closed(499), item({ ref: "B900", title: "Live" })]);
		view();
		await screen.findByText("Live");
		expect(screen.queryByText(/Showing the first/)).not.toBeInTheDocument();
	});
});

describe("BacklogView deep links", () => {
	it("fetches an item the lists do not hold instead of rendering nothing", async () => {
		const old = item({ id: "old-1", ref: "B77", title: "Closed long ago", state: "done", closedAt: daysAgo(900) });
		resetBacklogApi([item({ ref: "B1", title: "Live" })]);
		backlogApi.getBacklogItem.mockImplementation(async (projectId: string, id: string) => {
			if (projectId === "proj-1" && id === "old-1") return old;
			throw new Error("not found");
		});
		view({ itemId: "old-1" });
		expect(await screen.findByRole("article", { name: /B77 Closed long ago/ })).toBeInTheDocument();
		expect(backlogApi.getBacklogItem).toHaveBeenCalledWith("proj-1", "old-1");
	});

	it("finds the owning project when every project is listed", async () => {
		const elsewhere = item({ id: "far-1", ref: "B5", title: "In project two", projectId: "proj-2", state: "dropped", closedAt: daysAgo(400) });
		resetBacklogApi([item({ ref: "B1" })]);
		backlogApi.getBacklogItem.mockImplementation(async (projectId: string, id: string) => {
			if (projectId === "proj-2" && id === "far-1") return elsewhere;
			throw new Error("not found");
		});
		view({ projectId: undefined, itemId: "far-1" });
		expect(await screen.findByRole("article", { name: /B5 In project two/ })).toBeInTheDocument();
	});

	it("does not ask the server for an item the lists already hold", async () => {
		const held = item({ id: "here-1", ref: "B1", title: "Right here" });
		resetBacklogApi([held]);
		view({ itemId: "here-1" });
		await screen.findByRole("article", { name: /B1 Right here/ });
		// Only the detail pane's own read by ref; never a lookup by id.
		expect(backlogApi.getBacklogItem).not.toHaveBeenCalledWith("proj-1", "here-1");
	});
});

describe("BacklogView reordering", () => {
	const items = () => [item({ ref: "B1", title: "One", position: 1 }), item({ ref: "B2", title: "Two", position: 2 }), item({ ref: "B3", title: "Three", position: 3 })];

	it("moves an item with Alt+ArrowDown and posts the new place", async () => {
		resetBacklogApi(items());
		view();
		const row = await screen.findByRole("button", { name: /^B1, / });
		row.focus();
		fireEvent.keyDown(row, { key: "ArrowDown", altKey: true });
		await waitFor(() => expect(backlogApi.reorderBacklogItem).toHaveBeenCalledWith("proj-1", "B1", { after: "B2" }));
		expect(await screen.findByText("B1 moved to position 2")).toBeInTheDocument();
	});

	it("moves up with Alt+ArrowUp and does nothing at the top", async () => {
		resetBacklogApi(items());
		view();
		const second = await screen.findByRole("button", { name: /^B2, / });
		fireEvent.keyDown(second, { key: "ArrowUp", altKey: true });
		await waitFor(() => expect(backlogApi.reorderBacklogItem).toHaveBeenCalledWith("proj-1", "B2", { before: "B1" }));
		backlogApi.reorderBacklogItem.mockClear();
		fireEvent.keyDown(screen.getByRole("button", { name: /^B1, / }), { key: "ArrowUp", altKey: true });
		expect(backlogApi.reorderBacklogItem).not.toHaveBeenCalled();
	});

	it("restores the order when the server refuses", async () => {
		resetBacklogApi(items());
		backlogApi.reorderBacklogItem.mockRejectedValue(new Error("no"));
		view();
		const row = await screen.findByRole("button", { name: /^B1, / });
		fireEvent.keyDown(row, { key: "ArrowDown", altKey: true });
		await waitFor(() => expect(backlogApi.reorderBacklogItem).toHaveBeenCalled());
		await waitFor(() => expect(rowNames()).toEqual(["B1", "B2", "B3"]));
	});

	it("offers a drag handle on open rows only when one project is shown", async () => {
		resetBacklogApi(items());
		const { unmount } = view({ projectId: "proj-1" });
		await screen.findByRole("button", { name: "Reorder B1" });
		unmount();
		view({ projectId: undefined });
		await screen.findByRole("button", { name: /^B1, / });
		expect(screen.queryByRole("button", { name: "Reorder B1" })).not.toBeInTheDocument();
	});

	it("moves between rows with the arrow keys and j/k without selecting", async () => {
		resetBacklogApi(items());
		const onItemChange = vi.fn();
		view({ onItemChange });
		const first = await screen.findByRole("button", { name: /^B1, / });
		first.focus();
		fireEvent.keyDown(first, { key: "ArrowDown" });
		expect(screen.getByRole("button", { name: /^B2, / })).toHaveFocus();
		fireEvent.keyDown(screen.getByRole("button", { name: /^B2, / }), { key: "k" });
		expect(first).toHaveFocus();
		expect(onItemChange).not.toHaveBeenCalled();
	});
});

describe("BacklogView Escape", () => {
	it("closes the open item without letting the page-level Esc leave Backlog", async () => {
		const held = item({ id: "here-1", ref: "B1", title: "Right here" });
		resetBacklogApi([held]);
		const onItemChange = vi.fn();
		view({ itemId: "here-1", onItemChange });
		await screen.findByRole("article", { name: /B1 Right here/ });
		const seen: boolean[] = [];
		const record = (event: KeyboardEvent) => seen.push(event.defaultPrevented);
		document.addEventListener("keydown", record);
		const row = screen.getAllByRole("button", { name: /^B1, / })[0];
		await userEvent.type(row as HTMLElement, "{Escape}");
		document.removeEventListener("keydown", record);
		expect(onItemChange).toHaveBeenCalledWith(undefined);
		expect(seen).toEqual([true]);
	});
});
