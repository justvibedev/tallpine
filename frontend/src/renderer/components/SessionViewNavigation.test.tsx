import {
	navigateMock,
	openShellTerminalMock,
	adoptedShellHandles,
	closeShellTerminalMock,
	interfaceTransitionMock,
	interfaceTransitionState,
	reviewGetMock,
	chatViewPostMock,
	artifactFeedbackConsumes,
	chatSurfaceTransitionRenders,
	resumeAgentPostMock,
	chooseSessionAction,
	routeBlockerState,
	workspaces,
	workspaceQueryState,
	shellTerminalsState,
	mockShellCreation,
	workerSession,
	testInterfaceTransition,
	inspectorOpen,
	unsafeChatLeaveDialog,
	confirmUnsafeChatLeave,
	render,
} from "./SessionView.test.setup";
import { resetSessionViewState } from "./SessionView.test.reset";
import { StrictMode } from "react";
import {
	act,
	fireEvent,
	renderHook,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { SessionView } from "./SessionView";
import { appShortcutBus } from "../test/app-shortcut-bus";
import { useUiStore } from "../stores/ui-store";
import { setChatDraftBoundary } from "../lib/chat-draft-boundary";
import { chatDraftScopeKey } from "../lib/chat-drafts";
import { useFileAttachments, type FileAttachment } from "../hooks/useFileAttachments";

describe("SessionView", () => {
	beforeEach(() => {
		resetSessionViewState();
	});

	it("opens the Artifacts view for an artifact and consumes its feedback request once", async () => {
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open artifact feedback" }));

		await waitFor(() => expect(artifactFeedbackConsumes).toEqual([1]));
		await waitFor(() => expect(screen.queryByText("feedback request 1")).not.toBeInTheDocument());
		expect(screen.getByText("artifacts view")).toBeInTheDocument();
		expect(screen.getByText("selected report.html")).toBeInTheDocument();
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("artifacts");
		expect(inspectorOpen("sess-1")).toBe(true);

		fireEvent.click(screen.getByRole("tab", { name: "Summary" }));
		expect(screen.queryByText("artifacts view")).not.toBeInTheDocument();
		expect(artifactFeedbackConsumes).toEqual([1]);
	});

	it("resumes only the opened stopped session once, including in StrictMode", async () => {
		for (const session of workspaces[0].sessions) {
			session.status = "exited";
			session.activity = { state: "exited", lastActivityAt: "" };
		}
		const view = render(<StrictMode><SessionView sessionId="sess-1" /></StrictMode>);
		await waitFor(() => expect(resumeAgentPostMock).toHaveBeenCalledTimes(1));
		expect(resumeAgentPostMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/resume-agent", {
			params: { path: { sessionId: "sess-1" } },
		});
		view.rerender(<StrictMode><SessionView sessionId="sess-1" /></StrictMode>);
		await act(async () => {});
		expect(resumeAgentPostMock).toHaveBeenCalledTimes(1);
	});

	it("hides the stopped Chat banner from the first render until automatic resume settles", async () => {
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "exited";
		session.activity = { state: "exited", lastActivityAt: "" };
		let finishResume!: (value: { data: object; error: undefined }) => void;
		resumeAgentPostMock.mockImplementation(() => new Promise((resolve) => { finishResume = resolve; }));
		render(<SessionView sessionId="sess-1" />);
		expect(chatSurfaceTransitionRenders[0]).toBe(true);
		await waitFor(() => expect(resumeAgentPostMock).toHaveBeenCalledTimes(1));
		expect(chatSurfaceTransitionRenders.every(Boolean)).toBe(true);
		await act(async () => {
			session.activity = { state: "idle", lastActivityAt: "" };
			session.status = "working";
			finishResume({ data: {}, error: undefined });
		});
		await waitFor(() => expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "false"));
	});

	it("does not restart an agent that exits while its session is already open", async () => {
		const session = workerSession("sess-1");
		session.activity = { state: "idle", lastActivityAt: "" };
		const view = render(<SessionView sessionId="sess-1" />);
		session.status = "exited";
		session.activity = { state: "exited", lastActivityAt: "" };
		view.rerender(<SessionView sessionId="sess-1" />);
		await act(async () => {});
		expect(resumeAgentPostMock).not.toHaveBeenCalled();
	});

	it("keeps terminated sessions stopped when opened", async () => {
		const session = workerSession("sess-1");
		session.isTerminated = true;
		session.status = "terminated";
		session.activity = { state: "exited", lastActivityAt: "" };
		render(<SessionView sessionId="sess-1" />);
		await act(async () => {});
		expect(resumeAgentPostMock).not.toHaveBeenCalled();
	});

	it("leaves a failed automatic resume stopped for manual retry", async () => {
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "exited";
		session.activity = { state: "exited", lastActivityAt: "" };
		resumeAgentPostMock.mockRejectedValue(new Error("provider unavailable"));
		const view = render(<SessionView sessionId="sess-1" />);
		await waitFor(() => expect(resumeAgentPostMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(view.client.isMutating()).toBe(0));
		expect(chatSurfaceTransitionRenders[0]).toBe(true);
		expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "false");
		view.rerender(<SessionView sessionId="sess-1" />);
		await act(async () => {});
		expect(resumeAgentPostMock).toHaveBeenCalledTimes(1);
	});





	it("waits for the direct session lookup after the workspace list has loaded", () => {
		workspaceQueryState.data = [];
		workspaceQueryState.directLoading = true;
		const view = render(<SessionView projectId="proj-1" sessionId="starting-orchestrator" />);
		expect(screen.queryByText(/Session not found/)).not.toBeInTheDocument();
		workspaceQueryState.directLoading = false;
		view.rerender(<SessionView projectId="proj-1" sessionId="starting-orchestrator" />);
		expect(screen.getByText(/Session not found/)).toBeInTheDocument();
	});

	it("does not use another project's cached session as a local route fallback", () => {
		render(<SessionView projectId="proj-1" sessionId="sess-cross-project" />);
		expect(screen.getByText(/Session not found/)).toBeInTheDocument();
		expect(screen.queryByTestId("terminal-center")).not.toBeInTheDocument();
	});
	// Regression: shell terminals are an app-wide list, so without a per-session
	// filter a shell opened in another session would show up as a tab in this
	// session's strip. Only this session's shells (not another session's, and no
	// session-less ones) should reach the terminal pane.
	it("shows only the current session's shell terminals as tabs", () => {
		shellTerminalsState.data = [
			{
				handleId: "sh-a",
				sessionId: "sess-1",
				title: "sess-1-shell",
				workingDir: "/p",
				createdAt: "2026-07-24T00:00:00Z",
			},
			{
				handleId: "sh-b",
				sessionId: "sess-2",
				title: "sess-2-shell",
				workingDir: "/q",
				createdAt: "2026-07-24T00:00:00Z",
			},
			{ handleId: "sh-c", title: "loose-shell", workingDir: "/r", createdAt: "2026-07-24T00:00:00Z" },
		];
		render(<SessionView sessionId="sess-1" />);
		const tabs = screen.getByTestId("shell-tabs");
		expect(tabs).toHaveTextContent("sess-1-shell");
		expect(tabs).not.toHaveTextContent("sess-2-shell");
		expect(tabs).not.toHaveTextContent("loose-shell");
	});

	it("toggles between the session pane and its latest shell terminal from the terminal shortcut", () => {
		shellTerminalsState.data = [
			{ handleId: "sh-a", sessionId: "sess-1", title: "sess-1-shell", workingDir: "/p", createdAt: "2026-07-24T00:00:00Z" },
		];
		render(<SessionView sessionId="sess-1" />);
		expect(useUiStore.getState().visibleTerminalKindBySession["sess-1"]).toBe("worker");

		act(() => appShortcutBus.emit({ id: "toggle-terminal" }));
		expect(useUiStore.getState().visibleTerminalKindBySession["sess-1"]).toBe("shell");
		expect(openShellTerminalMock).not.toHaveBeenCalled();

		act(() => appShortcutBus.emit({ id: "toggle-terminal" }));
		expect(useUiStore.getState().visibleTerminalKindBySession["sess-1"]).toBe("worker");
	});

	it("opens a shell from the terminal shortcut when the session has none", () => {
		shellTerminalsState.data = [];
		render(<SessionView sessionId="sess-1" />);

		act(() => appShortcutBus.emit({ id: "toggle-terminal" }));

		expect(openShellTerminalMock).toHaveBeenCalledWith({ projectId: "proj-1", sessionId: "sess-1" });
	});

	it("opens the model picker from its shortcut, and leaves a disabled picker alone", () => {
		render(<SessionView sessionId="sess-1" />);
		const picker = document.createElement("button");
		picker.dataset.shortcutTarget = "model-picker";
		const onClick = vi.fn();
		picker.addEventListener("keydown", onClick);
		document.body.append(picker);

		act(() => appShortcutBus.emit({ id: "model-picker" }));
		expect(onClick).toHaveBeenCalledTimes(1);

		picker.disabled = true;
		act(() => appShortcutBus.emit({ id: "model-picker" }));
		expect(onClick).toHaveBeenCalledTimes(1);
		picker.remove();
	});

	it("ignores session shortcuts while the global Settings page covers the session", async () => {
		const copy = vi.spyOn(window.tallpine!.editorHandoff, "copyWorkspacePath").mockResolvedValue(undefined);
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		render(<SessionView sessionId="sess-1" />);
		act(() => useUiStore.setState({ settingsModal: { scope: "global", section: "general" } as never }));

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
		act(() => appShortcutBus.emit({ id: "copy-session-path" }));

		expect(inspectorOpen("sess-1")).toBe(false);
		expect(copy).not.toHaveBeenCalled();
		act(() => useUiStore.setState({ settingsModal: null }));
	});

	it("opens the Backlog page from the backlog shortcut", () => {
		navigateMock.mockClear();
		render(<SessionView sessionId="sess-1" />);
		act(() => appShortcutBus.emit({ id: "open-backlog-panel" }));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/backlog" });
	});

	it("copies the session path from its shortcut and says so", async () => {
		const copy = vi.spyOn(window.tallpine!.editorHandoff, "copyWorkspacePath").mockResolvedValue(undefined);
		render(<SessionView sessionId="sess-1" />);

		act(() => appShortcutBus.emit({ id: "copy-session-path" }));

		expect(copy).toHaveBeenCalledWith("sess-1");
		await waitFor(() => expect(useUiStore.getState().globalToast?.title).toBe("Session path copied"));
		copy.mockRestore();
	});

	it("reports a failed path copy instead of failing silently", async () => {
		const copy = vi.spyOn(window.tallpine!.editorHandoff, "copyWorkspacePath").mockRejectedValue(new Error("Workspace removed"));
		render(<SessionView sessionId="sess-1" />);

		act(() => appShortcutBus.emit({ id: "copy-session-path" }));

		await waitFor(() => expect(useUiStore.getState().globalToast?.title).toBe("Could not copy session path"));
		expect(useUiStore.getState().globalToast?.body).toBe("Workspace removed");
		copy.mockRestore();
	});

	// The pane shows one terminal at a time, so selecting a shell takes the
	// agent's terminal off screen while the route still points at this session.
	// The notification runtime lives outside this subtree and reads the published
	// kind to decide whether the user can actually see a needs_input prompt.
	it("publishes which terminal the session pane is showing", () => {
		shellTerminalsState.data = [
			{
				handleId: "sh-a",
				sessionId: "sess-1",
				title: "sess-1-shell",
				workingDir: "/p",
				createdAt: "2026-07-24T00:00:00Z",
			},
		];
		const view = render(<SessionView sessionId="sess-1" />);
		expect(useUiStore.getState().visibleTerminalKindBySession["sess-1"]).toBe("worker");

		fireEvent.click(screen.getByRole("button", { name: "select sess-1-shell" }));
		expect(useUiStore.getState().visibleTerminalKindBySession["sess-1"]).toBe("shell");

		fireEvent.click(screen.getByRole("button", { name: "select agent tab" }));
		expect(useUiStore.getState().visibleTerminalKindBySession["sess-1"]).toBe("worker");

		// Leaving the session drops the entry rather than leaving a stale "worker"
		// behind for a pane that is no longer mounted.
		view.unmount();
		expect(useUiStore.getState().visibleTerminalKindBySession["sess-1"]).toBeUndefined();
	});

	it("keeps a session-scoped shell reachable from a Chat session", () => {
		workspaces[0].sessions[0].mode = "chat";
		shellTerminalsState.data = [
			{
				handleId: "chat-shell",
				sessionId: "sess-1",
				title: "chat worktree shell",
				workingDir: "/p",
				createdAt: "2026-08-04T00:00:00Z",
			},
		];

		render(<SessionView sessionId="sess-1" />);
		expect(screen.getByTestId("chat-surface")).toBeInTheDocument();

		// Selecting the shell keeps the chat surface mounted — the shell renders
		// as a tab inside it instead of swapping in the terminal CenterPane.
		act(() => useUiStore.getState().setActiveShellTerminal("chat-shell"));
		expect(screen.getByTestId("chat-surface")).toBeInTheDocument();
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("shell");

		fireEvent.click(screen.getByRole("button", { name: "select chat tab" }));
		expect(screen.getByTestId("chat-surface")).toBeInTheDocument();
		expect(screen.queryByTestId("terminal-target")).not.toBeInTheDocument();
	});

	it("keeps only the selected primary Chat tab awake", async () => {
		workerSession("sess-1").mode = "chat";
		shellTerminalsState.data = [{
			handleId: "chat-shell",
			sessionId: "sess-1",
			title: "chat shell",
			workingDir: "/p",
			createdAt: "2026-08-04T00:00:00Z",
		}];
		const view = render(<SessionView sessionId="sess-1" />);
		await waitFor(() => expect(chatViewPostMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/chat-view",
			expect.objectContaining({
				params: { path: { sessionId: "sess-1" } },
				body: { viewId: expect.any(String), active: true },
			}),
		));
		const viewId = chatViewPostMock.mock.calls[0][1].body.viewId;

		act(() => useUiStore.getState().setActiveShellTerminal("chat-shell"));
		await waitFor(() => expect(chatViewPostMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/chat-view",
			{ params: { path: { sessionId: "sess-1" } }, body: { viewId, active: false } },
		));
		fireEvent.click(screen.getByRole("button", { name: "select chat tab" }));
		await waitFor(() => expect(chatViewPostMock.mock.calls.filter(([, input]) => input.body.active)).toHaveLength(2));
		view.unmount();
		await waitFor(() => expect(chatViewPostMock.mock.calls.filter(([, input]) => !input.body.active)).toHaveLength(2));
	});

	it("releases Chat without waiting for a pending wake and cleans up its late response", async () => {
		workerSession("sess-1").mode = "chat";
		let finishWake!: (result: { error: undefined }) => void;
		chatViewPostMock.mockImplementationOnce(() => new Promise((resolve) => { finishWake = resolve; }));
		const view = render(<SessionView sessionId="sess-1" />);
		await waitFor(() => expect(chatViewPostMock).toHaveBeenCalledTimes(1));
		const viewId = chatViewPostMock.mock.calls[0][1].body.viewId;
		view.unmount();
		await waitFor(() => expect(chatViewPostMock).toHaveBeenLastCalledWith(
			"/api/v1/sessions/{sessionId}/chat-view",
			{ params: { path: { sessionId: "sess-1" } }, body: { viewId, active: false } },
		));
		await act(async () => { finishWake({ error: undefined }); });
		expect(chatViewPostMock.mock.calls.filter(([, input]) => !input.body.active)).toHaveLength(2);
	});

	it("retries a failed automatic wake only after reopening Chat", async () => {
		workerSession("sess-1").mode = "chat";
		chatViewPostMock.mockReset()
			.mockResolvedValueOnce({ error: { code: "CHAT_RESUME_FAILED" } })
			.mockResolvedValue({ error: undefined });
		const view = render(<SessionView sessionId="sess-1" />);
		await waitFor(() => expect(chatViewPostMock.mock.calls.filter(([, input]) => input.body.active)).toHaveLength(1));

		view.rerender(<SessionView sessionId="sess-2" />);
		await waitFor(() => expect(chatViewPostMock.mock.calls.some(([, input]) => input.body.active === false)).toBe(true));
		view.rerender(<SessionView sessionId="sess-1" />);
		await waitFor(() => expect(chatViewPostMock.mock.calls.filter(([, input]) => input.body.active)).toHaveLength(2));
	});

	it("does not wake Chat while restoring its selected shell tab", async () => {
		workerSession("sess-1").mode = "chat";
		shellTerminalsState.data = [{
			handleId: "chat-shell",
			sessionId: "sess-1",
			title: "chat shell",
			workingDir: "/p",
			createdAt: "2026-08-04T00:00:00Z",
		}];
		useUiStore.setState({ activeShellTerminalHandleId: "chat-shell" });
		shellTerminalsState.loaded = false;

		const view = render(<SessionView sessionId="sess-1" />);
		expect(chatViewPostMock).not.toHaveBeenCalled();
		shellTerminalsState.loaded = true;
		view.rerender(<SessionView sessionId="sess-1" />);
		await waitFor(() => expect(screen.getByTestId("terminal-target")).toHaveTextContent("shell"));
		expect(chatViewPostMock).not.toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/chat-view",
			expect.objectContaining({ body: expect.objectContaining({ active: true }) }),
		);

		fireEvent.click(screen.getByRole("button", { name: "select chat tab" }));
		await waitFor(() => expect(chatViewPostMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/chat-view",
			expect.objectContaining({ body: expect.objectContaining({ active: true }) }),
		));
	});

	it("keeps Chat awake when the selected shell belongs to another session", async () => {
		workerSession("sess-1").mode = "chat";
		shellTerminalsState.data = [{
			handleId: "other-shell",
			sessionId: "sess-2",
			title: "other shell",
			workingDir: "/p",
			createdAt: "2026-08-04T00:00:00Z",
		}];
		useUiStore.setState({ activeShellTerminalHandleId: "other-shell" });

		render(<SessionView sessionId="sess-1" />);
		await waitFor(() => expect(chatViewPostMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/chat-view",
			expect.objectContaining({ body: expect.objectContaining({ active: true }) }),
		));
	});

	it("remounts the session-owned Chat surface when navigation selects another Chat session", () => {
		workerSession("sess-1").mode = "chat";
		workerSession("sess-2").mode = "chat";
		const view = render(<SessionView sessionId="sess-1" />);
		const firstSurface = screen.getByTestId("chat-surface");

		view.rerender(<SessionView sessionId="sess-2" />);

		expect(screen.getByTestId("chat-surface")).not.toBe(firstSurface);
	});

	// The strip only ever shows the session on screen — pinning another session's
	// terminal as a tab (and the cross-project picker that did it) is gone (#3208).
	it("shows only the session on screen in the tab strip", () => {
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("session-tab")).toHaveTextContent("do the thing");
		expect(screen.getByTestId("session-tab")).not.toHaveTextContent("do the other thing");
		expect(screen.queryByRole("button", { name: /^Add / })).not.toBeInTheDocument();
	});

	// The daemon roots a shell in the session's worktree when it is given that
	// session's id, so a new terminal must name the session actually on screen.
	it("opens new terminals in the on-screen session's worktree", () => {
		render(<SessionView sessionId="sess-2" />);

		const newTerminalButton = screen.getByRole("button", { name: "New terminal" });
		fireEvent.click(newTerminalButton);
		expect(openShellTerminalMock).toHaveBeenCalledWith({ projectId: "proj-1", sessionId: "sess-2" });
		expect(useUiStore.getState().activeShellTerminalHandleId).toBe("pending-shell:test");
	});


























	it("activates a new terminal opened while a file tab is selected", async () => {
		const shell = {
			handleId: "sh-after-file",
			projectId: "proj-1",
			sessionId: "sess-1",
			title: "Terminal 1",
			workingDir: "/p",
			createdAt: "2026-08-31T00:00:00Z",
		};
		const finishCreate = mockShellCreation();
		const view = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "view review file" }));
		expect(await screen.findByTestId("session-file-workspace")).toHaveTextContent("src/panel.tsx");

		fireEvent.click(screen.getByRole("button", { name: "New terminal" }));
		expect(screen.getByTestId("session-file-workspace").closest("[inert]")).not.toBeNull();
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("pending-shell:1");
		finishCreate(0, shell, view);
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("sh-after-file");
	});

	it("keeps the selection and tab order when shells opened in quick succession are created", () => {
		const finishCreate = mockShellCreation();
		const shell = (n: number) => ({
			handleId: `sh-${n}`,
			projectId: "proj-1",
			sessionId: "sess-1",
			title: `Terminal ${n}`,
			workingDir: "/p",
			createdAt: `2026-08-31T00:00:0${n}Z`,
		});
		const view = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "New terminal" }));
		fireEvent.click(screen.getByRole("button", { name: "New terminal" }));
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("pending-shell:2");

		// The first shell arrives while the second tab is selected.
		finishCreate(0, shell(1), view);
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("pending-shell:2");
		expect(screen.getByTestId("auxiliary-tab-order-tui-sess-1")).toHaveTextContent("sh-1|pending-shell:2");

		finishCreate(1, shell(2), view);
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("sh-2");
		expect(screen.getByTestId("auxiliary-tab-order-tui-sess-1")).toHaveTextContent("sh-1|sh-2");
	});

	it("selects the neighbouring shell when the pending tab is closed", () => {
		const finishCreate = mockShellCreation();
		const shell = (n: number) => ({
			handleId: `sh-${n}`,
			projectId: "proj-1",
			sessionId: "sess-1",
			title: `Terminal ${n}`,
			workingDir: "/p",
			createdAt: `2026-08-31T00:00:0${n}Z`,
		});
		const view = render(<SessionView sessionId="sess-1" />);
		for (let i = 0; i < 4; i++) fireEvent.click(screen.getByRole("button", { name: "New terminal" }));
		for (let i = 0; i < 3; i++) finishCreate(i, shell(i + 1), view);
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("pending-shell:4");

		fireEvent.click(screen.getByRole("button", { name: "close Terminal 4" }));
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("sh-3");
	});

	// The close runs against a render that has not seen Terminal 3's shell
	// arrive, so it selects Terminal 3's pending tab; the selection must follow
	// that tab to its shell rather than fall back to the agent.
	it("selects the neighbouring shell when a pending tab is closed before the view sees its neighbour arrive", () => {
		const shell = (n: number) => ({
			handleId: `sh-${n}`,
			projectId: "proj-1",
			sessionId: "sess-1",
			title: `Terminal ${n}`,
			workingDir: "/p",
			createdAt: `2026-08-31T00:00:0${n}Z`,
		});
		const pending = (n: number) => ({ ...shell(n), handleId: `pending-shell:${n}`, workingDir: "", optimistic: true as const });
		const view = render(<SessionView sessionId="sess-1" />);
		// The shell layout opens the tab and makes it active; the view follows.
		for (let n = 1; n <= 4; n++) {
			shellTerminalsState.data = [...shellTerminalsState.data, pending(n)];
			act(() => useUiStore.getState().setActiveShellTerminal(`pending-shell:${n}`));
			view.rerender(<SessionView sessionId="sess-1" />);
		}
		for (let n = 1; n <= 3; n++) {
			shellTerminalsState.data = shellTerminalsState.data.map((row) => (row.handleId === `pending-shell:${n}` ? shell(n) : row));
			adoptedShellHandles.set(`pending-shell:${n}`, `sh-${n}`);
			if (n < 3) view.rerender(<SessionView sessionId="sess-1" />);
		}
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("pending-shell:4");

		fireEvent.click(screen.getByRole("button", { name: "close Terminal 4" }));
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("sh-3");
	});

	it("reveals a command cue terminal over an open file", async () => {
		shellTerminalsState.data = [{ handleId: "cue-shell", projectId: "proj-1", sessionId: "sess-1", title: "Terminal 4", workingDir: "/p", createdAt: "2026-09-26T00:00:00Z" }];
		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "view review file" }));
		expect(await screen.findByTestId("session-file-workspace")).toBeInTheDocument();
		act(() => useUiStore.getState().setActiveShellTerminal("cue-shell"));
		await waitFor(() => expect(screen.getByTestId("session-file-workspace").closest("[inert]")).not.toBeNull());
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("cue-shell");
	});

	it("reveals a command cue terminal over reviewer chat", async () => {
		shellTerminalsState.data = [{ handleId: "cue-shell", projectId: "proj-1", sessionId: "sess-1", title: "Terminal 4", workingDir: "/p", createdAt: "2026-09-26T00:00:00Z" }];
		const view = render(<SessionView sessionId="sess-1" />);
		act(() => view.client.setQueryData(["session-reviews", "sess-1"], {
			reviewerHandleId: "review-chat:review-1",
			reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" },
			reviews: [], runs: [],
		}));
		fireEvent.click(screen.getByRole("button", { name: "open reviewer chat" }));
		expect(screen.getByTestId("reviewer-chat-surface")).toBeInTheDocument();
		act(() => useUiStore.getState().setActiveShellTerminal("cue-shell"));
		await waitFor(() => expect(screen.queryByTestId("reviewer-chat-surface")).not.toBeInTheDocument());
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("cue-shell");
	});

	it("does not offer a new terminal for orchestrator sessions", () => {
		render(<SessionView sessionId="sess-orch" />);

		expect(screen.queryByRole("button", { name: "New terminal" })).not.toBeInTheDocument();
	});

	// Regression (#3874 then re-added by #4252): the prime top bar carries session
	// identity, status, and controls — never the worktree branch. A branch badge
	// beside the actions duplicates a fact the inspector, board card, and command
	// palette already own, and its long name crowds the controls it sits next to.
	it.each([
		["a terminal worker", "sess-1", "tui", true],
		["a chat worker", "sess-1", "chat", true],
		["an orchestrator", "sess-orch", "tui", false],
	] as const)("keeps the git branch out of %s session's top bar", (_label, sessionId, mode, offersNewTerminal) => {
		workerSession(sessionId).mode = mode;

		render(<SessionView sessionId={sessionId} />);

		expect(screen.queryByText("ao/sess-1")).not.toBeInTheDocument();
		expect(screen.queryByTitle("ao/sess-1")).not.toBeInTheDocument();
		expect(document.querySelector(".lucide-git-branch")).toBeNull();
		// The session's own actions still ride in the same top-bar slot.
		expect(screen.getByTestId("mock-session-topbar")).toBeInTheDocument();
		expect(Boolean(screen.queryByRole("button", { name: "New terminal" }))).toBe(offersNewTerminal);
	});

	it("shows a shell opened from chat and returns to the chat agent tab", () => {
		const session = workspaces[0]!.sessions.find((candidate) => candidate.id === "sess-1")!;
		session.mode = "chat";
		const shell = {
			handleId: "sh-chat",
			projectId: "proj-1",
			sessionId: "sess-1",
			title: "chat shell",
			workingDir: "/p",
			createdAt: "2026-08-04T00:00:00Z",
		};
		const finishCreate = mockShellCreation();

		const view = render(<SessionView sessionId="sess-1" />);
		expect(screen.getByText("chat surface")).toBeInTheDocument();

		// Opening a shell from chat keeps the chat surface mounted: the shell is
		// a tab in its header and renders as the surface's active pane.
		fireEvent.click(screen.getByRole("button", { name: "open shell from chat" }));
		finishCreate(0, shell, view);
		expect(screen.getByText("chat surface")).toBeInTheDocument();
		expect(screen.getByTestId("shell-tabs")).toHaveTextContent("chat shell");
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("shell");

		fireEvent.click(screen.getByRole("button", { name: "select chat tab" }));
		expect(screen.getByText("chat surface")).toBeInTheDocument();
		expect(screen.queryByTestId("terminal-target")).not.toBeInTheDocument();
	});

	it("does not replay a consumed chat line request after session navigation", async () => {
		workerSession("sess-1").mode = "chat";
		workerSession("sess-2").mode = "chat";
		const view = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open chat basename" }));
		expect(await screen.findByTestId("session-file-workspace")).toHaveAttribute("data-line", "120");
		fireEvent.click(screen.getByRole("button", { name: "consume initial line" }));

		view.rerender(<SessionView sessionId="sess-2" />);
		view.rerender(<SessionView sessionId="sess-1" />);

		expect(await screen.findByTestId("session-file-workspace")).not.toHaveAttribute("data-line");
	});

	// #5984 follow-up from the issue author: a file revealed from a link must
	// not reopen, or take focus back from the agent, on every return.
	it("does not reopen a revealed file the user closed after switching sessions", async () => {
		workerSession("sess-1").mode = "chat";
		workerSession("sess-2").mode = "chat";
		const view = render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "view review file" }));
		await screen.findByTestId("session-file-workspace");
		fireEvent.click(screen.getByRole("button", { name: "Close panel.tsx" }));
		await waitFor(() => expect(screen.queryByTestId("session-file-workspace")).not.toBeInTheDocument());

		view.rerender(<SessionView sessionId="sess-2" />);
		view.rerender(<SessionView sessionId="sess-1" />);

		await act(async () => {});
		expect(screen.queryByTestId("session-file-workspace")).not.toBeInTheDocument();
	});

	it("keeps the agent tab selected after switching sessions when a revealed file is open", async () => {
		workerSession("sess-1").mode = "tui";
		workerSession("sess-2").mode = "tui";
		const view = render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "view review file" }));
		await screen.findByTestId("session-file-workspace");
		fireEvent.click(screen.getByRole("button", { name: "select agent tab" }));
		await waitFor(() => expect(screen.getByTestId("session-file-workspace").closest("[inert]")).not.toBeNull());

		view.rerender(<SessionView sessionId="sess-2" />);
		view.rerender(<SessionView sessionId="sess-1" />);

		await act(async () => {});
		expect(screen.getByTestId("session-file-workspace").closest("[inert]")).not.toBeNull();
		expect(screen.getByRole("button", { name: "Close panel.tsx" })).toBeInTheDocument();
	});

	it("preserves mixed tab order across session navigation and interface changes", async () => {
		workerSession("sess-1").mode = "chat";
		workerSession("sess-2").mode = "chat";
		shellTerminalsState.data = [
			{
				handleId: "sh-a",
				projectId: "proj-1",
				sessionId: "sess-1",
				title: "session one shell",
				workingDir: "/p",
				createdAt: "2026-08-31T00:00:00Z",
			},
		];
		const view = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "view review file" }));
		await screen.findByTestId("session-file-workspace");
		fireEvent.click(screen.getByRole("button", { name: "reorder auxiliary tabs" }));
		expect(screen.getByTestId("auxiliary-tab-order-sess-1")).toHaveTextContent(
			"sh-a|file:src/panel.tsx",
		);

		view.rerender(<SessionView sessionId="sess-2" />);
		view.rerender(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("auxiliary-tab-order-sess-1")).toHaveTextContent(
			"sh-a|file:src/panel.tsx",
		);

		workerSession("sess-1").mode = "tui";
		view.rerender(<SessionView sessionId="sess-1" />);
		expect(screen.getByTestId("auxiliary-tab-order-tui-sess-1")).toHaveTextContent(
			"sh-a|file:src/panel.tsx",
		);
	});

	it("selects the adjacent shell when closing a reordered file tab", async () => {
		workerSession("sess-1").mode = "chat";
		shellTerminalsState.data = [
			{
				handleId: "sh-a",
				projectId: "proj-1",
				sessionId: "sess-1",
				title: "session one shell",
				workingDir: "/p",
				createdAt: "2026-08-31T00:00:00Z",
			},
		];
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "view review file" }));
		await screen.findByTestId("session-file-workspace");
		fireEvent.click(screen.getByRole("button", { name: "reorder auxiliary tabs" }));
		fireEvent.click(screen.getByRole("button", { name: "Close panel.tsx" }));

		expect(screen.queryByTestId("session-file-workspace")).not.toBeInTheDocument();
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("shell");
	});

	it("preserves a reordered reviewer position while the session is inactive", async () => {
		const worker = workerSession("sess-1");
		worker.mode = "chat";
		worker.prs = [
			{
				url: "https://github.com/acme/repo/pull/7",
				number: 7,
				state: "open",
				ci: "passing",
				review: "none",
				mergeability: "mergeable",
				reviewComments: false,
				updatedAt: "2026-06-15T00:00:00Z",
			},
		];
		shellTerminalsState.data = [
			{
				handleId: "sh-a",
				projectId: "proj-1",
				sessionId: "sess-1",
				title: "session one shell",
				workingDir: "/p",
				createdAt: "2026-08-31T00:00:00Z",
			},
		];
		reviewGetMock.mockResolvedValueOnce({
			data: { reviewerHandleId: "review-sess-1", reviewerHarness: "codex", reviews: [] },
			error: undefined,
		});
		const view = render(<SessionView sessionId="sess-1" />);

		await screen.findByRole("button", { name: "Reviewer" });
		fireEvent.click(screen.getByRole("button", { name: "view review file" }));
		await screen.findByTestId("session-file-workspace");
		fireEvent.click(screen.getByRole("button", { name: "reorder reviewer tab" }));
		expect(screen.getByTestId("auxiliary-tab-order-sess-1")).toHaveTextContent(
			"sh-a|reviewer:review-sess-1|file:src/panel.tsx",
		);

		worker.status = "terminated";
		worker.isTerminated = true;
		view.rerender(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "reorder visible tabs" }));
		worker.status = "working";
		worker.isTerminated = false;
		view.rerender(<SessionView sessionId="sess-1" />);

		await screen.findByRole("button", { name: "Reviewer" });
		expect(screen.getByTestId("auxiliary-tab-order-sess-1")).toHaveTextContent(
			"file:src/panel.tsx|reviewer:review-sess-1|sh-a",
		);
	});

	it("keeps a shell's reordered position when closing it fails", async () => {
		workerSession("sess-1").mode = "chat";
		shellTerminalsState.data = [
			{
				handleId: "sh-a",
				projectId: "proj-1",
				sessionId: "sess-1",
				title: "session one shell",
				workingDir: "/p",
				createdAt: "2026-08-31T00:00:00Z",
			},
		];
		closeShellTerminalMock.mockImplementation((_handleId, options) => {
			options?.onError?.({ code: "SHELL_TERMINAL_CLOSE_FAILED" });
		});
		const view = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "view review file" }));
		await screen.findByTestId("session-file-workspace");
		fireEvent.click(screen.getByRole("button", { name: "reorder auxiliary tabs" }));
		fireEvent.click(screen.getByRole("button", { name: "session one shell" }));
		fireEvent.click(screen.getByRole("button", { name: "close session one shell" }));
		view.rerender(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("auxiliary-tab-order-sess-1")).toHaveTextContent(
			"sh-a|file:src/panel.tsx",
		);
	});

	it.each([
		["Terminal UI worker", "sess-1", "tui", "chat", "Switch to chat UI"],
		["Terminal UI orchestrator", "sess-orch", "tui", "chat", "Switch to chat UI"],
	] as const)("switches an idle %s directly with drain", async (_label, sessionId, mode, targetMode, buttonName) => {
		interfaceTransitionState.status = { supported: true, targetMode };
		const session = workerSession(sessionId);
		session.mode = mode;
		session.status = "idle";
		session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };

		render(<SessionView sessionId={sessionId} />);

		await chooseSessionAction(buttonName);

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({ targetMode, policy: "drain", historyPolicy: "strict" });
	});















	it.each([
		["worker", "sess-1"],
		["orchestrator", "sess-orch"],
	] as const)("switches an idle Chat %s directly to Terminal UI with drain", async (_label, sessionId) => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession(sessionId);
		session.mode = "chat";
		session.status = "idle";
		session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };

		render(<SessionView sessionId={sessionId} />);
		fireEvent.click(screen.getByRole("button", { name: "report chat work" }));

		await chooseSessionAction("Switch to terminal UI");

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		await waitFor(() =>
			expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
				targetMode: "tui",
				policy: "drain",
				historyPolicy: "strict",
			}),
		);
	});

	it("keeps the policy dialog closed when an idle direct switch fails", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "chat" };
		const session = workerSession("sess-1");
		session.status = "idle";
		session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };
		interfaceTransitionMock.start.mockRejectedValueOnce(new Error("switch failed"));

		render(<SessionView sessionId="sess-1" />);

		await chooseSessionAction("Switch to chat UI");

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("shows and dismisses a refused idle switch outside the policy dialog", () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		interfaceTransitionState.startError = "The agent has not exposed a native conversation (NATIVE_SESSION_MISSING)";
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "idle";
		render(<SessionView sessionId="sess-1" />);
		expect(screen.getByRole("alert")).toHaveTextContent("NATIVE_SESSION_MISSING");
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Dismiss interface switch error" }));
		expect(interfaceTransitionMock.resetStartError).toHaveBeenCalled();
	});

	it("prioritizes a new refusal over an older transition notice until dismissed", () => {
		workerSession("sess-1");
		interfaceTransitionState.status = { supported: true, targetMode: "chat", transition: {
			id: "old-transition", sessionId: "sess-1", sourceMode: "tui", targetMode: "chat", policy: "drain", historyPolicy: "strict", phase: "failed",
			errorDetail: "Previous switch failed", createdAt: "2026-09-05T00:00:00Z", updatedAt: "2026-09-05T00:00:00Z",
		} };
		interfaceTransitionState.startError = "Current request refused";
		const view = render(<SessionView sessionId="sess-1" />);
		expect(screen.getByRole("alert")).toHaveTextContent("Current request refused");
		expect(screen.queryByText("Previous switch failed")).not.toBeInTheDocument();
		interfaceTransitionState.startError = undefined;
		view.rerender(<SessionView sessionId="sess-1" />);
		expect(screen.getByText("Previous switch failed")).toBeInTheDocument();
	});



	it("keeps the policy dialog open when an explicit busy Chat choice fails", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "working";
		session.activity = { state: "active", lastActivityAt: "2026-08-06T00:00:00Z" };
		interfaceTransitionMock.start.mockRejectedValueOnce(new Error("switch failed"));

		render(<SessionView sessionId="sess-1" />);

		await chooseSessionAction("Switch to terminal UI");
		expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();
		await act(async () => {
			fireEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		});

		expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({ targetMode: "tui", policy: "interrupt", historyPolicy: "strict" });
	});

	it.each([
		["working status", "sess-1", "tui", "chat", "Switch to chat UI", "working", "idle"],
		["needs-input status", "sess-orch", "tui", "chat", "Switch to chat UI", "needs_input", "idle"],
		["blocked activity", "sess-1", "tui", "chat", "Switch to chat UI", "idle", "blocked"],
	] as const)("opens the switch policy dialog for %s", async (_label, sessionId, mode, targetMode, buttonName, status, activityState) => {
		interfaceTransitionState.status = { supported: true, targetMode };
		const session = workerSession(sessionId);
		session.mode = mode;
		session.status = status;
		session.activity = { state: activityState, lastActivityAt: "2026-08-06T00:00:00Z" };

		render(<SessionView sessionId={sessionId} />);

		await chooseSessionAction(buttonName);

		expect(screen.getByRole("dialog")).toBeInTheDocument();
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();
	});

	it("requires an explicit policy while current Chat work is not yet known", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "idle";
		session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };

		render(<SessionView sessionId="sess-1" />);
		await chooseSessionAction("Switch to terminal UI");

		expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();
	});

	it.each([
		["working status", "sess-1", "working", "idle"],
		["needs-input status", "sess-orch", "needs_input", "idle"],
		["active activity", "sess-1", "idle", "active"],
		["waiting-input activity", "sess-orch", "idle", "waiting_input"],
		["blocked activity", "sess-1", "idle", "blocked"],
	] as const)("asks for policy before a busy Chat session switches for %s", async (_label, sessionId, status, activityState) => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession(sessionId);
		session.mode = "chat";
		session.status = status;
		session.activity = { state: activityState, lastActivityAt: "2026-08-06T00:00:00Z" };

			render(<SessionView sessionId={sessionId} />);

			await chooseSessionAction("Switch to terminal UI");

			expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();
			expect(interfaceTransitionMock.start).not.toHaveBeenCalled();
		},
	);

	it("blocks route unload while a Chat draft is unsafe and uses explicit discard copy", async () => {
		let finishStaging!: (attachments: FileAttachment[]) => void;
		const staging = renderHook(() =>
			useFileAttachments({
				initialKey: chatDraftScopeKey({
					sessionId: "sess-1",
					incarnation: "2026-08-26T08:00:00.000Z",
				}),
				prepareAttachments: () =>
					new Promise<FileAttachment[]>((resolve) => {
						finishStaging = resolve;
					}),
			}),
		);
		let pending!: Promise<unknown>;
		act(() => {
			pending = staging.result.current.addFiles([
				new File([new Uint8Array(8).fill(1)], "route-late.txt", { type: "text/plain" }),
			]);
		});
		await waitFor(() => expect(finishStaging).toBeTypeOf("function"));
		setChatDraftBoundary("sess-1", "composer", "persistence-failed");
		render(<SessionView sessionId="sess-1" />);

		expect(routeBlockerState.options).toMatchObject({
			disabled: false,
			enableBeforeUnload: true,
		});
		let blocked!: Promise<boolean>;
		act(() => {
			blocked = Promise.resolve(routeBlockerState.options!.shouldBlockFn());
		});
		const cancelled = await unsafeChatLeaveDialog();
		expect(cancelled).toHaveTextContent("discard the unsaved changes");
		fireEvent.click(within(cancelled).getByRole("button", { name: "Cancel" }));
		expect(await blocked).toBe(true);

		let allowed!: Promise<boolean>;
		act(() => {
			allowed = Promise.resolve(routeBlockerState.options!.shouldBlockFn());
		});
		await confirmUnsafeChatLeave();
		expect(await allowed).toBe(false);
		await waitFor(() => expect(staging.result.current.preparing).toBe(false));
		await act(async () => {
			finishStaging([
				{
					id: "route-discarded-late",
					mimeType: "text/plain",
					bytes: 8,
					name: "route-late.txt",
					stagedPath: ".tallpine/attachments/route-late.txt",
				},
			]);
			await pending;
		});
		expect(staging.result.current.attachments).toEqual([]);
		act(() => setChatDraftBoundary("sess-1", "composer", undefined));
		expect(routeBlockerState.options).toMatchObject({
			disabled: true,
			enableBeforeUnload: false,
		});
	});

	it("publishes the full stable native risk set and clears it only on unmount", async () => {
		const publishRisk = vi.spyOn(window.tallpine!.app, "setChatDraftRisk");
		const view = render(<SessionView sessionId="sess-1" />);
		await waitFor(() => expect(publishRisk).toHaveBeenLastCalledWith([], expect.objectContaining({ stay: "Stay" })));

		act(() => setChatDraftBoundary("sess-1", "composer", "persistence-failed"));
		await waitFor(() =>
			expect(publishRisk).toHaveBeenLastCalledWith(["persistence-failed"], expect.objectContaining({ detail: expect.stringContaining("could not be saved locally") })),
		);
		publishRisk.mockClear();

		act(() =>
			setChatDraftBoundary("sess-1", "composer", [
				"persistence-failed",
				"pending-attachments",
			]),
		);
		await waitFor(() =>
			expect(publishRisk).toHaveBeenCalledWith([
				"persistence-failed",
				"pending-attachments",
			], expect.objectContaining({ detail: expect.stringContaining("Attachments are still being saved") })),
		);
		expect(publishRisk).not.toHaveBeenCalledWith([]);

		view.unmount();
		expect(publishRisk).toHaveBeenLastCalledWith([]);
		publishRisk.mockRestore();
	});

	it("uses an app-rendered confirmation before leaving unsafe Chat for Terminal", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession("sess-1");
		session.mode = "chat";
		setChatDraftBoundary("sess-1", "composer", "pending-attachments");
		const nativeConfirm = vi.spyOn(window, "confirm");
		render(<SessionView sessionId="sess-1" />);

		await chooseSessionAction("Switch to terminal UI");
		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();
		const cancelled = await screen.findByRole("dialog", { name: "Discard unsafe Chat draft state?" });
		expect(cancelled).toHaveTextContent("Attachments are still being saved");
		fireEvent.click(within(cancelled).getByRole("button", { name: "Cancel" }));
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();

		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		const confirmed = await screen.findByRole("dialog", { name: "Discard unsafe Chat draft state?" });
		fireEvent.click(within(confirmed).getByRole("button", { name: "Leave chat" }));
		await waitFor(() =>
			expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
				targetMode: "tui",
				policy: "interrupt",
				historyPolicy: "strict",
			}),
		);
		expect(nativeConfirm).not.toHaveBeenCalled();
		setChatDraftBoundary("sess-1", "composer", undefined);
		nativeConfirm.mockRestore();
	});

	it("cancels pending attachments only after the exact interface transition completes", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const transition = testInterfaceTransition("discard-after-complete", "requested");
		interfaceTransitionMock.start.mockResolvedValueOnce({ transition });
		const session = workerSession("sess-1");
		session.mode = "chat";
		let finishStaging!: (attachments: FileAttachment[]) => void;
		const staging = renderHook(() =>
			useFileAttachments({
				initialKey: chatDraftScopeKey({
					sessionId: session.id,
					incarnation: "2026-08-26T09:00:00.000Z",
				}),
				prepareAttachments: () =>
					new Promise<FileAttachment[]>((resolve) => {
						finishStaging = resolve;
					}),
			}),
		);
		let pending!: Promise<unknown>;
		act(() => {
			pending = staging.result.current.addFiles([
				new File([new Uint8Array(8).fill(1)], "discard-late.txt", {
					type: "text/plain",
				}),
			]);
		});
		await waitFor(() => expect(finishStaging).toBeTypeOf("function"));
		act(() => setChatDraftBoundary(session.id, "composer", "pending-attachments"));
		const view = render(<SessionView sessionId={session.id} />);

		await chooseSessionAction("Switch to terminal UI");
		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		await confirmUnsafeChatLeave();
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
			targetMode: "tui",
			policy: "interrupt",
			historyPolicy: "strict",
		});
		await waitFor(() => expect(interfaceTransitionMock.start).toHaveBeenCalledTimes(1));
		expect(staging.result.current.preparing).toBe(true);
		await act(async () => undefined);

		interfaceTransitionState.status = {
			supported: true,
			targetMode: "tui",
			transition: testInterfaceTransition("older-completed-transition", "completed"),
		};
		view.rerender(<SessionView sessionId={session.id} />);
		expect(staging.result.current.preparing).toBe(true);

		interfaceTransitionState.status = {
			supported: true,
			targetMode: "tui",
			transition: { ...transition, phase: "completed" },
		};
		view.rerender(<SessionView sessionId={session.id} />);
		await waitFor(() => expect(staging.result.current.preparing).toBe(false));

		await act(async () => {
			finishStaging([
				{
					id: "discarded-late",
					mimeType: "text/plain",
					bytes: 8,
					name: "discard-late.txt",
					stagedPath: ".tallpine/attachments/discard-late.txt",
				},
			]);
			await pending;
		});
		expect(staging.result.current.attachments).toEqual([]);
		act(() => setChatDraftBoundary(session.id, "composer", undefined));
	});
});
