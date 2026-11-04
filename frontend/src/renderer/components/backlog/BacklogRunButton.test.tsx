import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogRun, resetRunApi, runApi, runSwitch } from "../../test/backlog-run-fixtures";
import { renderWithClient } from "../../test/backlog-run-harness";
import { BacklogRunButton } from "./BacklogRunButton";

vi.mock("../../lib/backlog-run-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-run-api")>()), ...(await import("../../test/backlog-run-fixtures")).runApi }));
vi.mock("../../hooks/useSettings", async () => {
	const fixtures = await import("../../test/backlog-run-fixtures");
	return { useSettings: () => ({ settings: { backlogRunEnabled: fixtures.runSwitch.enabled } }) };
});

beforeEach(() => resetRunApi(undefined));

const button = (selection: Parameters<typeof BacklogRunButton>[0]["selection"] = { kind: "all" }, hasOrchestrator = true) =>
	renderWithClient(<BacklogRunButton projectId="proj-1" selection={selection} hasOrchestrator={hasOrchestrator} />);

describe("BacklogRunButton and dialog", () => {
	it("starts one run with the chosen scope and closes", async () => {
		button();
		await userEvent.click(await screen.findByRole("button", { name: "Run" }));
		const dialog = await screen.findByRole("dialog", { name: /Run all open items/ });
		await userEvent.click(within(dialog).getByRole("combobox", { name: "Publish as" }));
		await userEvent.click(await screen.findByRole("option", { name: "PR and merge" }));
		await userEvent.click(within(dialog).getByRole("button", { name: "Start run" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(runApi.startBacklogRun).toHaveBeenCalledTimes(1);
		expect(runApi.startBacklogRun).toHaveBeenCalledWith("proj-1", "pr-merge", { kind: "all" });
	});

	it("sends exactly one start however fast Start is clicked", async () => {
		let release: (value: unknown) => void = () => undefined;
		runApi.startBacklogRun.mockImplementation(() => new Promise((resolve) => { release = resolve; }));
		button();
		await userEvent.click(await screen.findByRole("button", { name: "Run" }));
		const start = await screen.findByRole("button", { name: "Start run" });
		// Two clicks in the same tick, before React can re-render the button as busy.
		fireEvent.click(start);
		fireEvent.click(start);
		await waitFor(() => expect(runApi.startBacklogRun).toHaveBeenCalledTimes(1));
		await new Promise((resolve) => setTimeout(resolve, 30));
		expect(runApi.startBacklogRun).toHaveBeenCalledTimes(1);
		release(backlogRun());
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(runApi.startBacklogRun).toHaveBeenCalledTimes(1);
	});

	it("names a parent's children in the dialog and starts the parent selection", async () => {
		button({ kind: "parent", ref: "B30" });
		await userEvent.click(await screen.findByRole("button", { name: "Run" }));
		await screen.findByRole("dialog", { name: /Run B30 and its children/ });
		await userEvent.click(screen.getByRole("button", { name: "Start run" }));
		await waitFor(() => expect(runApi.startBacklogRun).toHaveBeenCalledWith("proj-1", "pr", { kind: "parent", ref: "B30" }));
	});

	it("keeps the dialog open and shows why when the daemon refuses", async () => {
		runApi.startBacklogRun.mockRejectedValue(new Error("The project has no running orchestrator to plan the run"));
		button();
		await userEvent.click(await screen.findByRole("button", { name: "Run" }));
		await userEvent.click(await screen.findByRole("button", { name: "Start run" }));
		expect(await screen.findByText(/Couldn't start the run: The project has no running orchestrator/)).toBeInTheDocument();
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});

	it("is disabled with the reason while a run is open or nobody can plan it", async () => {
		resetRunApi(backlogRun({ ref: "R4", status: "running" }));
		button();
		await waitFor(() => expect(screen.getByRole("button", { name: "Run" })).toBeDisabled());
		resetRunApi(undefined);
		renderWithClient(<BacklogRunButton projectId="proj-9" selection={{ kind: "all" }} hasOrchestrator={false} />);
		await waitFor(() => expect(screen.getAllByRole("button", { name: "Run" }).at(-1)).toBeDisabled());
	});

	it("is enabled again once the latest run is done", async () => {
		resetRunApi(backlogRun({ status: "done" }));
		button();
		await waitFor(() => expect(runApi.getLatestBacklogRun).toHaveBeenCalled());
		expect(await screen.findByRole("button", { name: "Run" })).toBeEnabled();
	});

	it("is not there at all when run mode is off", async () => {
		runSwitch.enabled = false;
		button();
		await new Promise((resolve) => setTimeout(resolve, 30));
		expect(screen.queryByRole("button", { name: "Run" })).not.toBeInTheDocument();
		expect(runApi.getLatestBacklogRun).not.toHaveBeenCalled();
	});
});
