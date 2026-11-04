import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appShortcutBus } from "../../test/app-shortcut-bus";
import { backlogApi, renderBacklog, resetBacklogApi, workspaceData } from "../../test/backlog-harness";
import { useUiStore } from "../../stores/ui-store";
import { GlobalToast } from "../GlobalToast";
import { GlobalBacklogCapture } from "./GlobalBacklogCapture";

const { navigate, params } = vi.hoisted(() => ({ navigate: vi.fn(), params: { current: {} as { projectId?: string; hostId?: string } } }));
vi.mock("@tanstack/react-router", async (importActual) => ({
	...(await importActual<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigate,
	useParams: () => params.current,
}));
vi.mock("../../lib/backlog-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-api")>()), ...(await import("../../test/backlog-harness")).backlogApi }));
vi.mock("../../hooks/useWorkspaceQuery", async (importActual) => {
	const harness = await import("../../test/backlog-harness");
	return { ...(await importActual<typeof import("../../hooks/useWorkspaceQuery")>()), useWorkspaceQuery: () => ({ data: harness.workspaceData.current }) };
});

const mount = () => renderBacklog(<><GlobalBacklogCapture /><GlobalToast /></>);

beforeEach(() => {
	navigate.mockReset();
	params.current = {};
	appShortcutBus.reset();
	window.localStorage.clear();
	useUiStore.setState({ backlogCaptureRequest: null });
	useUiStore.getState().clearGlobalToast();
	resetBacklogApi([]);
	workspaceData.current = [
		{ id: "proj-1", name: "Project One", path: "/p1", sessions: [] },
		{ id: "proj-2", name: "Project Two", path: "/p2", sessions: [] },
	];
});

describe("GlobalBacklogCapture", () => {
	it("opens from the Cmd+Shift+K shortcut wherever focus is, and Escape closes it", async () => {
		mount();
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		act(() => appShortcutBus.emit({ id: "backlog-capture" }));
		expect(await screen.findByRole("dialog", { name: "Add to backlog" })).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("opens with the title a caller typed (the palette)", async () => {
		mount();
		act(() => useUiStore.getState().requestBacklogCapture("Typed in the palette"));
		expect(await screen.findByRole("textbox", { name: "Title and note" })).toHaveValue("Typed in the palette");
	});

	it("starts on the project in view, adds, and the toast opens the new item", async () => {
		params.current = { projectId: "proj-2" };
		mount();
		act(() => appShortcutBus.emit({ id: "backlog-capture" }));
		await userEvent.type(await screen.findByRole("textbox", { name: "Title and note" }), "Park this{Enter}");
		await waitFor(() => expect(backlogApi.createBacklogItem).toHaveBeenCalledWith("proj-2", { title: "Park this", note: "" }));
		expect(await screen.findByText("Added B99 · Park this")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Open" }));
		expect(navigate).toHaveBeenCalledWith({ to: "/backlog", search: { project: "proj-2", item: "new-Park this" } });
	});

	it("remembers the last project filed to when no project is in view", async () => {
		mount();
		act(() => appShortcutBus.emit({ id: "backlog-capture" }));
		await userEvent.click(await screen.findByRole("combobox", { name: "Project" }));
		await userEvent.click(await screen.findByRole("option", { name: "Project Two" }));
		await userEvent.type(screen.getByRole("textbox", { name: "Title and note" }), "First{Enter}");
		await waitFor(() => expect(backlogApi.createBacklogItem).toHaveBeenCalledWith("proj-2", expect.anything()));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

		act(() => appShortcutBus.emit({ id: "backlog-capture" }));
		await userEvent.type(await screen.findByRole("textbox", { name: "Title and note" }), "Second{Enter}");
		await waitFor(() => expect(backlogApi.createBacklogItem).toHaveBeenLastCalledWith("proj-2", { title: "Second", note: "" }));
	});

	it("ignores a remote host's project route when choosing the default", async () => {
		params.current = { hostId: "box", projectId: "proj-2" };
		mount();
		act(() => appShortcutBus.emit({ id: "backlog-capture" }));
		await userEvent.type(await screen.findByRole("textbox", { name: "Title and note" }), "Local{Enter}");
		await waitFor(() => expect(backlogApi.createBacklogItem).toHaveBeenCalledWith("proj-1", expect.anything()));
	});
});
