import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogProjectKey } from "../../lib/backlog-api";
import { backlogRun, resetRunApi, runApi, runItem, runSwitch } from "../../test/backlog-run-fixtures";
import { renderWithClient } from "../../test/backlog-run-harness";
import { BacklogRunCard } from "./BacklogRunCard";

vi.mock("../../lib/backlog-run-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-run-api")>()), ...(await import("../../test/backlog-run-fixtures")).runApi }));
vi.mock("../../hooks/useSettings", async () => {
	const fixtures = await import("../../test/backlog-run-fixtures");
	return { useSettings: () => ({ settings: { backlogRunEnabled: fixtures.runSwitch.enabled } }) };
});

const planned = () =>
	backlogRun({
		items: [
			runItem({ ref: "B1", itemId: "i1", title: "Build parser", lane: 0, position: 0, status: "active", displayStatus: "active", model: "opus", usage: { costNanos: 1_500_000_000, costComplete: true, elapsedSeconds: 600 } }),
			runItem({ ref: "B2", itemId: "i2", title: "Wire the API", lane: 0, position: 1, status: "queued", displayStatus: "queued" }),
			runItem({ ref: "B3", itemId: "i3", title: "Pick a database", lane: 1, position: 0, status: "needs_you", displayStatus: "needs_you", question: "Postgres or SQLite?" }),
			runItem({ ref: "B4", itemId: "i4", title: "Old cleanup", lane: 1, position: 1, status: "active", displayStatus: "landed" }),
			runItem({ ref: "B5", itemId: "i5", title: "Flaky thing", lane: 2, position: 0, status: "failed", displayStatus: "held", reason: "failed twice" }),
			runItem({ ref: "B6", itemId: "i6", title: "Docs", lane: 2, position: 1, status: "in_review", displayStatus: "in_review" }),
		],
		counts: { active: 1, queued: 1, needs_you: 1, landed: 1, held: 1, in_review: 1 },
	});

beforeEach(() => resetRunApi(planned()));

describe("BacklogRunCard", () => {
	it("shows lanes, every item's status (derived landed and held included) and the workers in use", async () => {
		renderWithClient(<BacklogRunCard projectId="proj-1" />);
		const card = await screen.findByRole("region", { name: "Run R1" });
		expect(within(card).getByText(/Running · PR$/)).toBeInTheDocument();
		expect(within(card).getByText("1/3 workers")).toBeInTheDocument();
		expect(within(card).getAllByRole("region", { name: /^Lane \d$/ })).toHaveLength(3);
		const row = (title: string) => within(card).getByText(title).closest("li") as HTMLElement;
		expect(within(row("Build parser")).getByText("Working")).toBeInTheDocument();
		expect(within(row("Build parser")).getByText("opus")).toBeInTheDocument();
		expect(within(row("Build parser")).getByText("Cost $1.50")).toBeInTheDocument();
		expect(within(row("Build parser")).getByText("Elapsed 10m")).toBeInTheDocument();
		expect(within(row("Wire the API")).getByText("Queued")).toBeInTheDocument();
		expect(within(row("Pick a database")).getByText("Needs you")).toBeInTheDocument();
		expect(within(row("Pick a database")).getByText("Postgres or SQLite?")).toBeInTheDocument();
		// Stored status is still active; the item itself says it landed.
		expect(within(row("Old cleanup")).getByText("Landed")).toBeInTheDocument();
		expect(within(row("Flaky thing")).getByText("Held")).toBeInTheDocument();
		expect(within(row("Flaky thing")).getByText("failed twice")).toBeInTheDocument();
		expect(within(row("Docs")).getByText("In review")).toBeInTheDocument();
	});

	it("updates live when a backlog event invalidates the project's keys", async () => {
		const { client } = renderWithClient(<BacklogRunCard projectId="proj-1" />);
		const card = await screen.findByRole("region", { name: "Run R1" });
		expect(within(card).getByText("Queued")).toBeInTheDocument();
		const next = planned();
		next.items[1] = runItem({ ref: "B2", itemId: "i2", title: "Wire the API", lane: 0, position: 1, status: "active", displayStatus: "active" });
		next.counts = { active: 2 };
		runApi.getLatestBacklogRun.mockResolvedValue(next);
		await client.invalidateQueries({ queryKey: backlogProjectKey("proj-1") });
		await waitFor(() => expect(within(screen.getByRole("region", { name: "Run R1" })).getByText("2/3 workers")).toBeInTheDocument());
		expect(within(screen.getByText("Wire the API").closest("li") as HTMLElement).getByText("Working")).toBeInTheDocument();
	});

	it("sends exactly one request for each Pause, Resume and Stop click", async () => {
		renderWithClient(<BacklogRunCard projectId="proj-1" />);
		await screen.findByRole("region", { name: "Run R1" });
		await userEvent.click(screen.getByRole("button", { name: "Pause" }));
		await waitFor(() => expect(runApi.controlBacklogRun).toHaveBeenCalledTimes(1));
		expect(runApi.controlBacklogRun).toHaveBeenLastCalledWith("proj-1", "R1", "pause");
		await userEvent.click(screen.getByRole("button", { name: "Stop" }));
		await waitFor(() => expect(runApi.controlBacklogRun).toHaveBeenCalledTimes(2));
		expect(runApi.controlBacklogRun).toHaveBeenLastCalledWith("proj-1", "R1", "stop");
	});

	it("offers Resume on a paused run and says why it paused", async () => {
		resetRunApi({ ...planned(), status: "paused", pauseReason: "Claude usage limit" });
		renderWithClient(<BacklogRunCard projectId="proj-1" />);
		expect(await screen.findByText("Paused: Claude usage limit")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Pause" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Resume" }));
		await waitFor(() => expect(runApi.controlBacklogRun).toHaveBeenCalledTimes(1));
		expect(runApi.controlBacklogRun).toHaveBeenLastCalledWith("proj-1", "R1", "resume");
	});

	it("has no controls once the run is stopping or done, and a done run folds to its summary", async () => {
		resetRunApi({
			...planned(),
			status: "done",
			summary: "B1 landed; B3 waits on you.",
			counts: { landed: 3, needs_you: 1, held: 2 },
			usage: { costNanos: 4_000_000_000, costComplete: false, elapsedSeconds: 3 * 3600 },
		});
		renderWithClient(<BacklogRunCard projectId="proj-1" />);
		expect(await screen.findByText("3 landed · 1 waiting on you · 2 held")).toBeInTheDocument();
		expect(screen.getByText("B1 landed; B3 waits on you.")).toBeInTheDocument();
		expect(screen.getByText("Cost $4.00+")).toBeInTheDocument();
		expect(screen.getByText("Elapsed 3h 0m")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^(Pause|Resume|Stop)$/ })).not.toBeInTheDocument();
		expect(screen.queryByText("Build parser")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Show or hide run R1" }));
		expect(screen.getByText("Build parser")).toBeInTheDocument();
	});

	it("is absent, and asks the daemon for nothing, when run mode is off", async () => {
		runSwitch.enabled = false;
		renderWithClient(<BacklogRunCard projectId="proj-1" />);
		await new Promise((resolve) => setTimeout(resolve, 30));
		expect(screen.queryByRole("region", { name: /^Run / })).not.toBeInTheDocument();
		expect(runApi.getLatestBacklogRun).not.toHaveBeenCalled();
	});

	it("shows nothing for a project that never had a run", async () => {
		resetRunApi(undefined);
		renderWithClient(<BacklogRunCard projectId="proj-1" />);
		await waitFor(() => expect(runApi.getLatestBacklogRun).toHaveBeenCalled());
		expect(screen.queryByRole("region", { name: /^Run / })).not.toBeInTheDocument();
	});
});

describe("BacklogRunCard plan status", () => {
	it("shows each item's plan status", async () => {
		resetRunApi(backlogRun({ items: [runItem({ ref: "B1", itemId: "i1", title: "Planned", planStatus: "awaiting_approval" }), runItem({ ref: "B2", itemId: "i2", title: "Plain" })], counts: { queued: 2 } }));
		renderWithClient(<BacklogRunCard projectId="p1" />);
		expect(await screen.findByText("Plan: waiting for you")).toBeInTheDocument();
		expect(screen.getAllByTestId("run-item-plan-status")).toHaveLength(1);
	});
});
