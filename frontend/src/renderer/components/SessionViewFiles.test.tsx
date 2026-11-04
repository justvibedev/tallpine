import {
	navigateMock,
	nativeFullScreenMock,
	reviewGetMock,
	chatViewPostMock,
	workspaces,
	workspaceQueryState,
	browserViewState,
	workerSession,
	inspectorOpen,
	browserUnseen,
	inspectorButton,
	render,
} from "./SessionView.test.setup";
import { resetSessionViewState } from "./SessionView.test.reset";
import {
	act,
	fireEvent,
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
import type { BrowserTabsState } from "../../main/browser-view-host";
import { rememberedFileDisplayMode, useUiStore } from "../stores/ui-store";

// SessionFileExplorer and SessionFileWorkspace are module-level React.lazy
// components: only the first render in a worker waits for their chunk, so a
// test that queries them synchronously passed only after an earlier test had
// loaded them (B158). Load them once up front so every test is order-independent.
let lazyFilesLoaded = false;
async function loadLazyFilesViews() {
	if (lazyFilesLoaded) return;
	const view = render(<SessionView sessionId="sess-1" />);
	fireEvent.click(screen.getByRole("button", { name: "open files" }));
	fireEvent.click(await screen.findByRole("button", { name: "select src/App.tsx" }));
	await screen.findByTestId("session-file-workspace");
	view.unmount();
	lazyFilesLoaded = true;
}

describe("SessionView", () => {
	beforeEach(async () => {
		resetState();
		await loadLazyFilesViews();
		resetState();
	});

	function resetState() {
		resetSessionViewState();
	}

	it("opens the workspace changes from Files after an artifact was opened", async () => {
		useUiStore.getState().setFilesSelection("sess-1", { branch: { kind: "branch", name: "main" }, changes: { kind: "commits", shas: [] } });
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open artifact feedback" }));
		await waitFor(() => expect(screen.getByText("artifacts view")).toBeInTheDocument());

		fireEvent.click(screen.getByRole("button", { name: "open files" }));

		// Files opens on the checked-out branch's default review.
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.filesSelection).toBeUndefined();
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("files");
		expect(screen.queryByText("artifacts view")).not.toBeInTheDocument();
	});

	it("reopens the most recently closed file tab, newest first, and does nothing once the stack is empty", () => {
		workerSession("sess-1").mode = "chat";
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		expect(screen.getByRole("button", { name: "close file:src/App.tsx" })).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "close file:src/App.tsx" }));
		expect(screen.queryByRole("button", { name: "close file:src/App.tsx" })).not.toBeInTheDocument();

		act(() => appShortcutBus.emit({ id: "reopen-closed-tab" }));
		expect(screen.getByRole("button", { name: "close file:src/App.tsx" })).toBeInTheDocument();

		// Nothing left to reopen: the tab stays as it is.
		act(() => appShortcutBus.emit({ id: "reopen-closed-tab" }));
		expect(screen.getAllByRole("button", { name: "close file:src/App.tsx" })).toHaveLength(1);
	});

	it("keeps a tree-opened file tab's request across session navigation", async () => {
		workerSession("sess-1").mode = "chat";
		workerSession("sess-2").mode = "chat";
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		const view = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		const requestKey = (await screen.findByTestId("session-file-workspace")).getAttribute("data-request-key");
		expect(requestKey).toBeTruthy();

		view.rerender(<SessionView sessionId="sess-2" />);
		view.rerender(<SessionView sessionId="sess-1" />);

		expect(await screen.findByTestId("session-file-workspace")).toHaveAttribute("data-request-key", requestKey);
	});

	// #5997 review: SessionView remounts on a route round-trip (session -> board ->
	// session), so a new open request must never reuse a key the store
	// remembered a display mode for; otherwise an explicit "open diff" (or Edit)
	// is hijacked into the remembered Rich preview.
	it("never lets a remembered display mode capture a new request after a remount", async () => {
		workerSession("sess-1").mode = "chat";
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		const first = render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		const firstKey = Number((await screen.findByTestId("session-file-workspace")).getAttribute("data-request-key"));
		// The pane records the Rich preview picked for that request.
		act(() => useUiStore.getState().setFileDisplayMode("sess-1", "src/App.tsx", "rendered", firstKey));
		first.unmount();

		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "open diff src/App.tsx in center" }));
		const pane = await screen.findByTestId("session-file-workspace");
		expect(pane).toHaveAttribute("data-mode", "diff");
		const secondKey = Number(pane.getAttribute("data-request-key"));
		expect(rememberedFileDisplayMode(useUiStore.getState(), "sess-1", "src/App.tsx", secondKey)).toBeUndefined();
	});

	it("opens Edit as an editor, not a remembered Rich preview, after a remount", async () => {
		workerSession("sess-1").mode = "chat";
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		const first = render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		const firstKey = Number((await screen.findByTestId("session-file-workspace")).getAttribute("data-request-key"));
		act(() => useUiStore.getState().setFileDisplayMode("sess-1", "src/App.tsx", "rendered", firstKey));
		first.unmount();

		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "edit src/App.tsx in center" }));
		const pane = await screen.findByTestId("session-file-workspace");
		expect(pane).toHaveAttribute("data-mode", "file");
		expect(pane).toHaveAttribute("data-editing", "true");
		expect(rememberedFileDisplayMode(useUiStore.getState(), "sess-1", "src/App.tsx", Number(pane.getAttribute("data-request-key")))).toBeUndefined();
	});

	it("opens the files view in the inspector rail first", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open files" }));

		expect(
			within(screen.getByTestId("panel-inspector")).getByRole("button", { name: "files rail" }),
		).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "files center" })).not.toBeInTheDocument();
		expect(screen.getByText("terminal center")).toBeInTheDocument();
	});


	it("opens a selected tree file in a center tab while retaining the right-side tree", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));

		expect(screen.getByRole("tab", { name: "App.tsx" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByTestId("session-file-workspace")).toHaveTextContent("src/App.tsx");
		expect(screen.getByTestId("session-file-workspace")).toHaveAttribute("data-mode", "file");
		expect(within(screen.getByTestId("panel-inspector")).getByText("file tree")).toBeInTheDocument();
		expect(screen.getByText("terminal center")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "select agent tab" }));
		expect(screen.getByTestId("session-file-workspace").closest("[inert]")).not.toBeNull();
		expect(screen.getByRole("tab", { name: "App.tsx" })).toHaveAttribute("aria-selected", "false");
	});

 it("keeps a workspace file mounted when opening the same path from a historical source",()=>{
  act(()=>useUiStore.getState().setInspectorOpen("sess-1",true));
  render(<SessionView sessionId="sess-1"/>);
  fireEvent.click(screen.getByRole("button",{name:"open files"}));
  fireEvent.click(screen.getByRole("button",{name:"select src/App.tsx"}));
  const livePane=screen.getByTestId("session-file-workspace");
  fireEvent.click(screen.getByRole("button",{name:"open historical src/App.tsx"}));
  expect(screen.getByRole("tab",{name:"App.tsx · feature · aaaaaaa"})).toHaveAttribute("aria-selected","true");
  expect(screen.getByRole("tab",{name:"App.tsx"})).toHaveAttribute("aria-selected","false");
  expect(livePane).toBeInTheDocument();expect(livePane.closest("[inert]")).not.toBeNull();
  fireEvent.click(screen.getByRole("tab",{name:"App.tsx"}));
  expect(livePane.closest("[inert]")).toBeNull();
  expect(screen.getAllByTestId("session-file-workspace")).toHaveLength(2);
 });

	it("toggles the header whole-file feedback composer on repeat", async () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		fireEvent.click(screen.getByRole("button", { name: "header feedback" }));
		await userEvent.type(screen.getByRole("textbox", { name: "feedback draft" }), "keep this draft");

		fireEvent.click(screen.getByRole("button", { name: "header feedback" }));

		expect(screen.queryByRole("textbox", { name: "feedback draft" })).not.toBeInTheDocument();
	});

	it("applies the Files split preference to a diff opened in the center", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(screen.getByRole("button", { name: "open diff src/App.tsx in center" }));
		expect(screen.getByTestId("session-file-workspace")).toHaveAttribute("data-mode", "diff");
		expect(screen.getByTestId("session-file-workspace")).toHaveAttribute("data-split", "false");

		fireEvent.click(screen.getByRole("button", { name: "use split diff" }));
		expect(screen.getByTestId("session-file-workspace")).toHaveAttribute("data-split", "true");
	});

	it("opens a review file target directly in center without opening Files", async () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByText("view review file"));

		await screen.findByTestId("session-file-workspace");
		expect(screen.getByRole("tab", { name: "panel.tsx" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByTestId("session-file-workspace")).toHaveTextContent("src/panel.tsx");
		expect(screen.getByTestId("session-file-workspace")).toHaveAttribute("data-mode", "diff");
		expect(screen.getByText("terminal center")).toBeInTheDocument();
		expect(inspectorOpen("sess-1")).toBe(false);
		expect(screen.queryByRole("button", { name: "files center" })).not.toBeInTheDocument();
	});

	it("opens an initial Command-K file request directly in the center", async () => {
		reviewGetMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/workspace/manifest") {
				return {
					data: {
						sessionId: "sess-1",
						files: [{ path: "src/from-command.ts", status: "added", additions: 1, deletions: 0, binary: false, size: 10 }],
						truncated: false,
						sections: { staged: [], unstaged: [], untracked: [], committed: [] },
						commits: [],
						summary: { files: 1, additions: 1, deletions: 0 },
					},
					error: undefined,
				};
			}
			return { data: { reviewerHandleId: "", reviews: [], runs: [] }, error: undefined };
		});
		act(() => useUiStore.getState().requestWorkspaceFileOpen("sess-1", "src/from-command.ts"));

		render(<SessionView sessionId="sess-1" />);

		const workspace = await screen.findByTestId("session-file-workspace");
		expect(workspace).toHaveTextContent("src/from-command.ts");
		expect(screen.getByRole("tab", { name: "from-command.ts" })).toHaveAttribute("aria-selected", "true");
		expect(useUiStore.getState().workspaceFileOpenRequest).toBeNull();
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("summary");
	});

	it("opens a requested markdown artifact in the Artifacts view", () => {
		const worker = workspaces[0].sessions[0];
		worker.artifactFiles = [{ kind: "markdown", name: "report.md", path: "out/report.md", size: 10, updatedAt: "2026-06-10T00:00:00Z" }];
		try {
			act(() => useUiStore.getState().requestWorkspaceFileOpen("sess-1", "out/report.md", undefined, "artifact"));

			render(<SessionView sessionId="sess-1" />);

			expect(screen.getByText("selected out/report.md")).toBeInTheDocument();
			expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("artifacts");
			expect(inspectorOpen("sess-1")).toBe(true);
			expect(useUiStore.getState().workspaceFileOpenRequest).toBeNull();
		} finally {
			delete worker.artifactFiles;
		}
	});

	it("opens a requested HTML artifact in the Browser panel", async () => {
		const worker = workspaces[0].sessions[0];
		const previewUrl = "http://ao-preview-artifact.abc.localhost:3101/report.html";
		worker.artifactFiles = [{ kind: "html", name: "report.html", path: "report.html", previewUrl, size: 10, updatedAt: "2026-06-10T00:00:00Z" }];
		try {
			act(() => useUiStore.getState().requestWorkspaceFileOpen("sess-1", "report.html", undefined, "artifact"));

			render(<SessionView sessionId="sess-1" />);

			await waitFor(() =>
				expect(chatViewPostMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", {
					params: { path: { sessionId: "sess-1" } },
					body: { url: previewUrl },
				}),
			);
			expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("browser");
			expect(inspectorOpen("sess-1")).toBe(true);
			expect(useUiStore.getState().workspaceFileOpenRequest).toBeNull();
		} finally {
			delete worker.artifactFiles;
		}
	});

	it("resolves a basename against workspace files before opening on a cold cache", async () => {
		reviewGetMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/workspace/manifest") {
				return {
					data: {
						sessionId: "sess-1",
						files: [
							{
								path: "docs/notes.txt",
								status: "added",
								additions: 1,
								deletions: 0,
								binary: false,
								size: 10,
							},
						],
						truncated: false,
						sections: { staged: [], unstaged: [], untracked: [], committed: [] },
						commits: [],
						summary: { files: 1, additions: 1, deletions: 0 },
					},
					error: undefined,
				};
			}
			return { data: { reviewerHandleId: "", reviews: [], runs: [] }, error: undefined };
		});

		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "view review basename" }));

		await screen.findByTestId("session-file-workspace");
		expect(screen.getByTestId("session-file-workspace")).toHaveTextContent("docs/notes.txt");
		expect(screen.getByTestId("session-file-workspace")).toHaveAttribute("data-mode", "diff");
	});

	it("resolves a chat basename against workspace files before opening on a cold cache", async () => {
		workspaces[0].sessions[0].mode = "chat";
		reviewGetMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/workspace/manifest") {
				return {
					data: {
						sessionId: "sess-1",
						files: [
							{
								path: "docs/notes.txt",
								status: "added",
								additions: 1,
								deletions: 0,
								binary: false,
								size: 10,
							},
						],
						truncated: false,
						sections: { staged: [], unstaged: [], untracked: [], committed: [] },
						commits: [],
						summary: { files: 1, additions: 1, deletions: 0 },
					},
					error: undefined,
				};
			}
			return { data: { reviewerHandleId: "", reviews: [], runs: [] }, error: undefined };
		});

		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open chat basename" }));

		await screen.findByTestId("session-file-workspace");
		expect(screen.getByTestId("session-file-workspace")).toHaveTextContent("docs/notes.txt");
		expect(screen.getByTestId("session-file-workspace")).toHaveAttribute("data-line", "120");
		expect(inspectorOpen("sess-1")).toBe(false);
	});

	it("does not carry a maximized Files view into the next session", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		act(() => useUiStore.getState().setInspectorOpen("sess-2", true));
		const view = render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(within(screen.getByTestId("panel-inspector")).getByRole("button", { name: "files rail" }));
		expect(document.querySelector(".files-popout-overlay")).not.toBeNull();

		view.rerender(<SessionView sessionId="sess-2" />);

		expect(document.querySelector(".files-popout-overlay")).toBeNull();
	});

	it("maximizes files over the whole app window and returns to the rail", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(within(screen.getByTestId("panel-inspector")).getByRole("button", { name: "files rail" }));

		expect(screen.getByRole("button", { name: "files center" })).toBeInTheDocument();
		const overlay = document.querySelector(".files-popout-overlay");
		expect(overlay).toHaveClass("files-popout-overlay--mac-windowed");
		expect(overlay?.parentElement).toBe(document.body);
		// Same chrome as the maximized browser: the filter sits in the titlebar
		// band, outside the inset frame that holds the explorer.
		const titlebar = screen.getByTestId("files-popout-topbar");
		const frame = overlay?.querySelector(".files-popout-frame");
		expect(titlebar).toHaveClass("files-popout-titlebar--mac-windowed");
		expect(within(titlebar).getByRole("textbox", { name: "files filter" })).toBeInTheDocument();
		expect(frame).toContainElement(screen.getByRole("button", { name: "files center" }));
		expect(frame).not.toContainElement(titlebar);
		expect(screen.getByText("terminal center")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "files center" }));
		expect(screen.queryByRole("button", { name: "files center" })).not.toBeInTheDocument();
		expect(
			within(screen.getByTestId("panel-inspector")).getByRole("button", { name: "files rail" }),
		).toBeInTheDocument();
		expect(screen.getByText("terminal center")).toBeInTheDocument();
	});

	it("does not reserve the traffic-light band for maximized files during native macOS fullscreen", () => {
		nativeFullScreenMock.mockReturnValue(true);
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(within(screen.getByTestId("panel-inspector")).getByRole("button", { name: "files rail" }));

		expect(document.querySelector(".files-popout-overlay")).not.toHaveClass("files-popout-overlay--mac-windowed");
		expect(screen.getByTestId("files-popout-topbar")).not.toHaveClass("files-popout-titlebar--mac-windowed");
	});

	it("reveals Browser for a new live preview target in the viewed session", () => {
		const worker = workerSession("sess-1");
		const { rerender } = render(<SessionView sessionId="sess-1" />);

		worker.previewUrl = "http://localhost:5173/";
		worker.previewRevision = 1;
		rerender(<SessionView sessionId="sess-1" />);

		expect(screen.getByText("terminal center")).toBeInTheDocument();
		expect(inspectorOpen("sess-1")).toBe(true);
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
		expect(browserUnseen("sess-1")).toBe(false);
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("opens a collapsed inspector on Browser when a new live preview arrives", () => {
		const worker = workerSession("sess-1");
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		const { rerender } = render(<SessionView sessionId="sess-1" />);

		worker.previewUrl = "http://localhost:5173/";
		worker.previewRevision = 1;
		rerender(<SessionView sessionId="sess-1" />);

		expect(inspectorOpen("sess-1")).toBe(true);
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
		expect(browserUnseen("sess-1")).toBe(false);
	});

	it("keeps Summary on session entry and reveals Browser only for later preview work", () => {
		const secondWorker = workerSession("sess-2");
		secondWorker.previewUrl = "http://localhost:5173/";
		secondWorker.previewRevision = 1;

		const { rerender } = render(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");
		expect(screen.getByTestId("panel-inspector")).not.toHaveAttribute("inert");
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");

		rerender(<SessionView sessionId="sess-2" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(browserUnseen("sess-2")).toBe(false);

		secondWorker.previewRevision = 2;
		rerender(<SessionView sessionId="sess-2" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
		expect(browserUnseen("sess-2")).toBe(false);
	});

	it.each([false, true])("keeps the empty Browser selected after its final tab closes (popped out: %s)", (poppedOut) => {
		let tabsListener: ((state: BrowserTabsState) => void) | undefined;
		vi.spyOn(window.tallpine!.browser, "onTabsState").mockImplementation((listener) => {
			tabsListener = listener;
			return () => undefined;
		});
		browserViewState.url = "https://example.test/";
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		act(() => useUiStore.getState().setInspectorView("sess-1", "browser"));
		if (poppedOut) fireEvent.click(screen.getByRole("button", { name: "pop browser" }));
		act(() => {
			browserViewState.url = "";
			browserViewState.tabCount = 0;
			tabsListener?.({ viewId: "browser:sess-1", activeTabId: "", tabs: [], change: { kind: "closed", tabId: "t1" } });
		});
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
		if (poppedOut) expect(screen.getByRole("button", { name: "browser center" })).toBeInTheDocument();
		vi.mocked(window.tallpine!.browser.onTabsState).mockRestore();
	});

	it.each(["open", "tab-new"])("reveals Browser once for explicit %s commands", (action) => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		browserViewState.agentBrowserActive = true;
		browserViewState.agentBrowserActivity = { viewId: "browser:sess-1", active: true, action, phase: "started", commandId: "command-1" };
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorOpen("sess-1")).toBe(true);
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
		expect(browserUnseen("sess-1")).toBe(false);

		// The same start event remains in the hook when the user closes Browser.
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		expect(inspectorOpen("sess-1")).toBe(false);
		browserViewState.agentBrowserActivity = { ...browserViewState.agentBrowserActivity, active: false, phase: "finished" };
		// Another command can keep the aggregate active flag true at completion.
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorOpen("sess-1")).toBe(false);

		browserViewState.agentBrowserActivity = { viewId: "browser:sess-1", active: true, action, phase: "started", commandId: "command-2" };
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorOpen("sess-1")).toBe(true);
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it.each(["snapshot", "click", "fill", "act", "errors"])("only badges Browser for passive %s commands", (action) => {
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		browserViewState.agentBrowserActive = true;
		browserViewState.agentBrowserActivity = { viewId: "browser:sess-1", active: true, action, phase: "started", commandId: "passive-1" };
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(browserUnseen("sess-1")).toBe(true);
	});

	it("reveals a fast open command even when React only renders its finished event", () => {
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		browserViewState.agentBrowserActivity = { viewId: "browser:sess-1", active: false, action: "open", phase: "finished", commandId: "fast-1" };
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
		act(() => useUiStore.getState().setInspectorView("sess-1", "summary"));
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
	});

	it("does not reveal for an explicit browser command from another view", () => {
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		browserViewState.agentBrowserActive = true;
		browserViewState.agentBrowserActivity = { viewId: "browser:sess-2", active: true, action: "open", phase: "started", commandId: "background-1" };
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("keeps the foreground view when a background session previews, and baselines that target on entry", () => {
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		workerSession("sess-2").previewUrl = "http://localhost:5173/";
		workerSession("sess-2").previewRevision = 1;
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		rerender(<SessionView sessionId="sess-2" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("dismisses maximized Files when an explicit preview reveals Browser", () => {
		const worker = workerSession("sess-1");
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		fireEvent.click(within(screen.getByTestId("panel-inspector")).getByRole("button", { name: "files rail" }));
		expect(document.querySelector(".files-popout-overlay")).toBeInTheDocument();
		worker.previewUrl = "http://localhost:5173/";
		worker.previewRevision = 1;
		rerender(<SessionView sessionId="sess-1" />);
		expect(document.querySelector(".files-popout-overlay")).not.toBeInTheDocument();
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
	});

	// Regression: the session-entry effect that defaults a brand-new session to
	// Summary tracked only the single most-recently-initialized session ID, so
	// re-entering ANY session that was not the immediately preceding one looked
	// identical to a first-ever visit and forced it back to Summary — silently
	// discarding whatever tab (Files, Browser) the user had left it on.
	it("remembers the tab a session was left on when returning to it after visiting another session", () => {
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");

		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		expect(inspectorButton()).toHaveAttribute("data-view", "files");

		rerender(<SessionView sessionId="sess-2" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");

		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "files");
	});

	// Regression: the "initialized" marker used to live in a component-local
	// ref, which is recreated whenever SessionView unmounts. Remounting an
	// already-visited session (e.g. across a route transition) then looked
	// identical to a first-ever visit and forced the tab back to Summary. The
	// marker now lives in the ui-store's persisted inspectorSessions state, so
	// it survives unmount/remount, not just re-renders of one mounted instance.
	it("remembers the tab a session was left on after unmounting and remounting the view", () => {
		const view = render(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");

		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		expect(inspectorButton()).toHaveAttribute("data-view", "files");

		view.unmount();

		render(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "files");
	});

	it("keeps Summary selected when preview content arrives with the async workspace response", () => {
		const secondWorker = workerSession("sess-2");
		secondWorker.previewUrl = "http://localhost:5173/";
		secondWorker.previewRevision = 1;
		workspaceQueryState.data = undefined;
		workspaceQueryState.isLoading = true;

		const { rerender } = render(<SessionView sessionId="sess-2" />);

		workspaceQueryState.data = workspaces;
		workspaceQueryState.isLoading = false;
		rerender(<SessionView sessionId="sess-2" />);

		expect(inspectorOpen("sess-2")).toBe(true);
		expect(screen.getByTestId("panel-inspector")).not.toHaveAttribute("inert");
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(browserUnseen("sess-2")).toBe(false);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");
	});

	it("glows for agent browser activity after the user leaves Browser", () => {
		const { rerender } = render(<SessionView sessionId="sess-1" />);

		browserViewState.url = "http://localhost:4173/";
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");

		act(() => useUiStore.getState().setInspectorView("sess-1", "browser"));

		browserViewState.agentBrowserActive = true;
		rerender(<SessionView sessionId="sess-1" />);
		expect(browserUnseen("sess-1")).toBe(false);

		// Switching away during an already-running command must still mark the
		// Browser as unseen; it should not wait for another command transition.
		act(() => useUiStore.getState().setInspectorView("sess-1", "summary"));

		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(browserUnseen("sess-1")).toBe(true);
	});

	it("does not glow for preview or agent activity while Browser is visible as a popout", () => {
		const worker = workerSession("sess-1");
		worker.previewUrl = "http://localhost:4173/";
		worker.previewRevision = 1;
		const { rerender } = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "pop browser" }));
		expect(screen.getByRole("button", { name: "browser center" })).toBeInTheDocument();
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));

		worker.previewRevision = 2;
		browserViewState.agentBrowserActive = true;
		rerender(<SessionView sessionId="sess-1" />);

		expect(browserUnseen("sess-1")).toBe(false);
	});

	it("does not let a previous session's popout consume the destination session's preview glow", () => {
		const secondWorker = workerSession("sess-2");
		secondWorker.previewUrl = "http://localhost:4173/";
		secondWorker.previewRevision = 2;
		act(() => {
			useUiStore.setState({
				inspectorSessions: {
					"sess-1": { isOpen: true, view: "summary" },
					"sess-2": {
						isOpen: true,
						view: "summary",
						browserContentRevealed: true,
					},
				},
			});
		});
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "pop browser" }));
		expect(screen.getByRole("button", { name: "browser center" })).toBeInTheDocument();

		rerender(<SessionView sessionId="sess-2" />);

		expect(browserUnseen("sess-2")).toBe(false);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
	});

	it("does not open Browser when `tallpine preview clear` removes the target", () => {
		const worker = workerSession("sess-1");
		const { rerender } = render(<SessionView sessionId="sess-1" />);

		worker.previewUrl = "http://localhost:5173/";
		worker.previewRevision = 1;
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
		expect(browserUnseen("sess-1")).toBe(false);

		act(() => {
			useUiStore.getState().setInspectorView("sess-1", "summary");
			useUiStore.getState().setInspectorOpen("sess-1", false);
		});

		worker.previewUrl = undefined;
		worker.previewRevision = 2;
		rerender(<SessionView sessionId="sess-1" />);

		expect(inspectorOpen("sess-1")).toBe(false);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(browserUnseen("sess-1")).toBe(false);

		worker.previewUrl = "http://localhost:3000/";
		worker.previewRevision = 3;
		rerender(<SessionView sessionId="sess-1" />);

		expect(inspectorOpen("sess-1")).toBe(true);
		expect(inspectorButton()).toHaveAttribute("data-view", "browser");
		expect(browserUnseen("sess-1")).toBe(false);
	});

	// Regression: a terminated session's `previewUrl` is a stale DB fact —
	// useBrowserView suppresses and destroys the live preview for terminated
	// sessions, so it must not count as active Browser content.
	it("keeps Summary selected for a terminated session with a stale previewUrl", () => {
		const worker = workerSession("sess-1");
		worker.status = "merged";
		worker.isTerminated = true;
		worker.previewUrl = "http://localhost:5173/";
		worker.previewRevision = 1;

		render(<SessionView sessionId="sess-1" />);

		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.browserContentRevealed).toBeFalsy();
	});

	it("does not reveal or badge new previews and open commands on a terminated session", () => {
		const worker = workerSession("sess-1");
		worker.isTerminated = true;
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		worker.previewUrl = "http://localhost:5173/";
		worker.previewRevision = 1;
		browserViewState.agentBrowserActive = true;
		browserViewState.agentBrowserActivity = { viewId: "browser:sess-1", active: true, action: "open", phase: "started", commandId: "terminated-1" };
		rerender(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
		expect(browserUnseen("sess-1")).toBe(false);
	});

	// Regression: agent-browser commands (fill, click, snapshot, …) are real
	// activity even on an empty target that has not navigated anywhere yet.
	// Gating the glow on hasBrowserContent/browserContentRevealed meant a
	// command run before any page loaded never surfaced as unseen.
	it("glows for agent browser activity even before any browser content has loaded", () => {
		const { rerender } = render(<SessionView sessionId="sess-1" />);
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");

		browserViewState.agentBrowserActive = true;
		rerender(<SessionView sessionId="sess-1" />);

		expect(browserUnseen("sess-1")).toBe(true);

		// An explicit clear still resets unseen activity when the target was
		// already empty, so only previewRevision changes.
		browserViewState.agentBrowserActive = false;
		workerSession("sess-1").previewRevision = 1;
		rerender(<SessionView sessionId="sess-1" />);
		expect(browserUnseen("sess-1")).toBe(false);
	});
});
