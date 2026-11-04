import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { GlobalLaneOrchestrator, laneChoices, runningLaneRefs } from "./GlobalLaneOrchestrator";
import type { BacklogItem } from "../lib/backlog-api";

const { navigateMock, spawnMock, listMock, createMock, stateMock, workspacesMock } = vi.hoisted(() => ({
	createMock: vi.fn(),
	stateMock: vi.fn(),
	workspacesMock: vi.fn(),
	navigateMock: vi.fn(),
	spawnMock: vi.fn(),
	listMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));
vi.mock("../lib/spawn-orchestrator", () => ({ spawnOrchestrator: spawnMock }));
vi.mock("../lib/backlog-api", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/backlog-api")>()),
	listBacklog: listMock,
	createBacklogItem: createMock,
	setBacklogState: stateMock,
}));
vi.mock("../hooks/useWorkspaceQuery", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useWorkspaceQuery")>()),
	useWorkspaceQuery: () => ({ data: workspacesMock() }),
}));

const item = (over: Partial<BacklogItem>) => ({ id: "bl_1", ref: "B1", title: "Topic", parentId: undefined, ...over }) as BacklogItem;

function renderDialog() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<GlobalLaneOrchestrator />
		</QueryClientProvider>,
	);
}

describe("laneChoices", () => {
	it("offers only top-level items as lanes", () => {
		const items = [item({ id: "a", ref: "B1" }), item({ id: "b", ref: "B2", parentId: "a" })];
		expect(laneChoices(items).map((i) => i.ref)).toEqual(["B1"]);
	});
});

describe("GlobalLaneOrchestrator", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		useUiStore.setState({ laneOrchestratorRequest: null });
		listMock.mockResolvedValue([item({ id: "bl_9", ref: "B9", title: "Mobile app" })]);
		spawnMock.mockResolvedValue("proj-5");
		stateMock.mockResolvedValue(undefined);
		workspacesMock.mockReturnValue([]);
	});

	it("starts the default orchestrator when no lane is chosen", async () => {
		renderDialog();
		expect(screen.queryByRole("dialog")).toBeNull();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("button", { name: "Start" }));
		await waitFor(() => expect(spawnMock).toHaveBeenCalledTimes(1));
		expect(spawnMock).toHaveBeenCalledWith("proj", "lane_dialog", false, undefined, undefined, undefined, undefined);
		await waitFor(() => expect(navigateMock).toHaveBeenCalled());
	});

	it("starts the chosen lane's orchestrator", async () => {
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("combobox"));
		await userEvent.click(await screen.findByRole("option", { name: "B9 Mobile app" }));
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await waitFor(() => expect(spawnMock).toHaveBeenCalledTimes(1));
		expect(spawnMock).toHaveBeenCalledWith("proj", "lane_dialog", false, undefined, undefined, undefined, "B9");
	});

	it("creates a top-level backlog item and uses it as the lane", async () => {
		createMock.mockResolvedValue(item({ ref: "B20", title: "Billing" }));
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("combobox"));
		await userEvent.click(await screen.findByRole("option", { name: /New lane/ }));
		expect(screen.getByRole("button", { name: "Start" })).toBeDisabled();
		await userEvent.type(screen.getByLabelText("Lane title"), "Billing");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await waitFor(() => expect(spawnMock).toHaveBeenCalledTimes(1));
		expect(createMock).toHaveBeenCalledWith("proj", { title: "Billing" });
		expect(spawnMock).toHaveBeenCalledWith("proj", "lane_dialog", false, undefined, undefined, undefined, "B20");
	});

	it("reuses the created backlog item when Start is retried after a failed spawn", async () => {
		createMock.mockResolvedValue(item({ ref: "B20", title: "Billing" }));
		spawnMock.mockRejectedValueOnce(new Error("boom"));
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("combobox"));
		await userEvent.click(await screen.findByRole("option", { name: /New lane/ }));
		await userEvent.type(screen.getByLabelText("Lane title"), "Billing");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("boom");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await waitFor(() => expect(spawnMock).toHaveBeenCalledTimes(2));
		expect(createMock).toHaveBeenCalledTimes(1);
		expect(spawnMock).toHaveBeenLastCalledWith("proj", "lane_dialog", false, undefined, undefined, undefined, "B20");
	});

	it("shows a spawn failure and stays open", async () => {
		spawnMock.mockRejectedValue(new Error("no orchestrator agent"));
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("button", { name: "Start" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("no orchestrator agent");
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("tags lanes that already have a live orchestrator as running", async () => {
		workspacesMock.mockReturnValue([{ id: "proj", sessions: [{ id: "proj-3", kind: "orchestrator", lane: "B9" }] }]);
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("combobox"));
		expect(await screen.findByRole("option", { name: "B9 Mobile app (running)" })).toBeInTheDocument();
	});

	it("drops the created backlog item when the dialog closes after a failed spawn", async () => {
		createMock.mockResolvedValue(item({ ref: "B20", title: "Billing" }));
		spawnMock.mockRejectedValueOnce(new Error("boom"));
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("combobox"));
		await userEvent.click(await screen.findByRole("option", { name: /New lane/ }));
		await userEvent.type(screen.getByLabelText("Lane title"), "Billing");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await screen.findByRole("alert");
		expect(stateMock).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Close" }));
		await waitFor(() => expect(stateMock).toHaveBeenCalledWith("proj", "B20", "dropped", expect.any(String)));
	});

	it("drops the earlier item when the title changes before a retry", async () => {
		createMock.mockResolvedValueOnce(item({ ref: "B20", title: "Billing" })).mockResolvedValueOnce(item({ ref: "B21", title: "Invoices" }));
		spawnMock.mockRejectedValueOnce(new Error("boom"));
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("combobox"));
		await userEvent.click(await screen.findByRole("option", { name: /New lane/ }));
		await userEvent.type(screen.getByLabelText("Lane title"), "Billing");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await screen.findByRole("alert");
		await userEvent.clear(screen.getByLabelText("Lane title"));
		await userEvent.type(screen.getByLabelText("Lane title"), "Invoices");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await waitFor(() => expect(spawnMock).toHaveBeenLastCalledWith("proj", "lane_dialog", false, undefined, undefined, undefined, "B21"));
		expect(stateMock).toHaveBeenCalledTimes(1);
		expect(stateMock).toHaveBeenCalledWith("proj", "B20", "dropped", expect.any(String));
	});

	it("drops the created item when the retry targets an existing lane", async () => {
		createMock.mockResolvedValue(item({ ref: "B20", title: "Billing" }));
		spawnMock.mockRejectedValueOnce(new Error("boom"));
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("combobox"));
		await userEvent.click(await screen.findByRole("option", { name: /New lane/ }));
		await userEvent.type(screen.getByLabelText("Lane title"), "Billing");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await screen.findByRole("alert");
		await userEvent.click(screen.getByRole("combobox"));
		await userEvent.click(await screen.findByRole("option", { name: "B9 Mobile app" }));
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await waitFor(() => expect(spawnMock).toHaveBeenLastCalledWith("proj", "lane_dialog", false, undefined, undefined, undefined, "B9"));
		expect(stateMock).toHaveBeenCalledWith("proj", "B20", "dropped", expect.any(String));
	});

	it("keeps the created item when the spawn succeeds", async () => {
		createMock.mockResolvedValue(item({ ref: "B20", title: "Billing" }));
		renderDialog();
		act(() => useUiStore.getState().requestLaneOrchestrator("proj"));
		await userEvent.click(await screen.findByRole("combobox"));
		await userEvent.click(await screen.findByRole("option", { name: /New lane/ }));
		await userEvent.type(screen.getByLabelText("Lane title"), "Billing");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		await waitFor(() => expect(navigateMock).toHaveBeenCalled());
		expect(stateMock).not.toHaveBeenCalled();
	});
});

describe("runningLaneRefs", () => {
	it("counts live lane orchestrators of the project only", () => {
		const ws = [
			{ id: "proj", sessions: [
				{ id: "a", kind: "orchestrator", lane: "B1" },
				{ id: "b", kind: "orchestrator", lane: "B2", isTerminated: true },
				{ id: "c", kind: "worker", lane: "B3" },
				{ id: "d", kind: "orchestrator" },
			] },
			{ id: "other", sessions: [{ id: "e", kind: "orchestrator", lane: "B4" }] },
		] as never;
		expect([...runningLaneRefs(ws, "proj")]).toEqual(["B1"]);
	});
});
