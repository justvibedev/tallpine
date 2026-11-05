import {
	nativeFullScreenMock,
	reviewGetMock,
	skillsGetMock,
	browserDestroy,
	browserViewOptions,
	browserViewState,
	workerSession,
	inspectorOpen,
	browserUnseen,
	render,
} from "./SessionView.test.setup";
import { resetSessionViewState } from "./SessionView.test.reset";
import {
	act,
	fireEvent,
	screen,
	waitFor,
} from "@testing-library/react";
import {
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { SessionView } from "./SessionView";
import { useUiStore } from "../stores/ui-store";

describe("SessionView", () => {
	beforeEach(() => {
		resetSessionViewState();
	});

	function mockAbsolutePathLookup(resolve: unknown) {
		reviewGetMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/workspace/resolve") return resolve;
			if (path === "/api/v1/sessions/{sessionId}/workspace/manifest") {
				return {
					data: {
						sessionId: "sess-orch",
						files: [{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, binary: false, size: 10 }],
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
	}

	it("no longer renders a skills banner above the session; the offer lives in the chat", async () => {
		render(<SessionView sessionId="sess-1" />);
		expect(await screen.findByTestId("session-detail")).toBeInTheDocument();
		expect(skillsGetMock).not.toHaveBeenCalled();
	});

	it("opens an absolute chat path at the workspace-relative path the daemon returns", async () => {
		const defaultGet = reviewGetMock.getMockImplementation()!;
		reviewGetMock.mockImplementation(async (path: string, ...args: unknown[]) =>
			path === "/api/v1/sessions/{sessionId}/workspace/resolve"
				? { data: { sessionId: "sess-orch", kind: "workspace", path: "src/App.tsx" }, error: undefined }
				: defaultGet(path, ...args));
		workerSession("sess-orch").mode = "chat";
		render(<SessionView sessionId="sess-orch" />);
		fireEvent.click(screen.getByRole("button", { name: "open chat absolute path" }));
		const file = await screen.findByTestId("session-file-workspace");
		expect(file).toHaveTextContent("src/App.tsx");
		expect(file).not.toHaveTextContent("/Users/me");
		expect(file).toHaveAttribute("data-line", "7");
		expect(reviewGetMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/resolve", {
			params: { path: { sessionId: "sess-orch" }, query: { path: "/Users/me/worktrees/s/src/App.tsx" } },
		});
	});

	it("does not open a same-named workspace file for a path the daemon places outside the session", async () => {
		mockAbsolutePathLookup({ data: { sessionId: "sess-orch", kind: "outside", path: "" }, error: undefined });
		workerSession("sess-orch").mode = "chat";
		render(<SessionView sessionId="sess-orch" />);
		fireEvent.click(screen.getByRole("button", { name: "open chat absolute path" }));
		expect(await screen.findByTestId("session-file-workspace")).toHaveTextContent("/Users/me/worktrees/s/src/App.tsx");
	});

	it("does not guess from the file list when the daemon cannot resolve an absolute chat path", async () => {
		mockAbsolutePathLookup({ data: undefined, error: { code: "NOT_IMPLEMENTED" } });
		workerSession("sess-orch").mode = "chat";
		render(<SessionView sessionId="sess-orch" />);
		fireEvent.click(screen.getByRole("button", { name: "open chat absolute path" }));
		expect(await screen.findByTestId("session-file-workspace")).toHaveTextContent("/Users/me/worktrees/s/src/App.tsx");
	});

	it("opens an absolute chat path to a listed artifact in the Artifacts view", async () => {
		const worker = workerSession("sess-1");
		worker.mode = "chat";
		worker.artifactFiles = [{ kind: "markdown", name: "report.md", path: "out/report.md", size: 10, updatedAt: "2026-06-10T00:00:00Z" }];
		mockAbsolutePathLookup({ data: { sessionId: "sess-1", kind: "artifact", path: "out/report.md" }, error: undefined });
		try {
			render(<SessionView sessionId="sess-1" />);
			fireEvent.click(screen.getByRole("button", { name: "open chat absolute path" }));
			expect(await screen.findByText("selected out/report.md")).toBeInTheDocument();
			expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("artifacts");
			expect(screen.queryByTestId("session-file-workspace")).not.toBeInTheDocument();
		} finally {
			delete worker.artifactFiles;
		}
	});

	it("explains an artifact the session still does not list after a refresh", async () => {
		workerSession("sess-1").mode = "chat";
		mockAbsolutePathLookup({ data: { sessionId: "sess-1", kind: "artifact", path: "out/new.md" }, error: undefined });
		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open chat absolute path" }));
		expect(await screen.findByTestId("session-file-workspace")).toHaveTextContent("/Users/me/worktrees/s/src/App.tsx");
		expect(screen.queryByText("selected out/new.md")).not.toBeInTheDocument();
	});

	it("does not ask the daemon about relative chat paths", async () => {
		workerSession("sess-orch").mode = "chat";
		render(<SessionView sessionId="sess-orch" />);
		fireEvent.click(screen.getByRole("button", { name: "open chat basename" }));
		await screen.findByTestId("session-file-workspace");
		expect(reviewGetMock.mock.calls.some(([path]) => path === "/api/v1/sessions/{sessionId}/workspace/resolve")).toBe(false);
	});

	it("opens orchestrator chat files in the center without revealing Browser", async () => {
		workerSession("sess-orch").mode = "chat";
		render(<SessionView sessionId="sess-orch" />);
		fireEvent.click(screen.getByRole("button", { name: "open chat basename" }));
		await waitFor(() => expect(screen.getByTestId("session-file-workspace")).toBeInTheDocument());
		expect(inspectorOpen("sess-orch")).toBe(false);
		expect(useUiStore.getState().inspectorSessions["sess-orch"]?.view).not.toBe("browser");
	});

	it("reveals each explicit orchestrator preview and respects closing it during passive activity", () => {
		const orchestrator = workerSession("sess-orch");
		const { rerender } = render(<SessionView sessionId="sess-orch" />);
		orchestrator.previewUrl = "https://example.com";
		orchestrator.previewRevision = 1;
		rerender(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(true);
		fireEvent.click(screen.getByTestId("sidebar-toggle"));
		orchestrator.previewRevision = 2;
		rerender(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(true);
		fireEvent.click(screen.getByTestId("sidebar-toggle"));
		browserViewState.agentBrowserActive = true;
		rerender(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(false);
		// The unseen dot itself is drawn by the panel switcher (PanelSwitcher.test.tsx); here the flag that feeds it.
		expect(browserUnseen("sess-orch")).toBe(true);
		browserViewState.agentBrowserActive = false;
		rerender(<SessionView sessionId="sess-orch" />);
		expect(browserUnseen("sess-orch")).toBe(true);
		rerender(<SessionView sessionId="sess-1" />);
		rerender(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(false);
		expect(browserUnseen("sess-orch")).toBe(true);
		fireEvent.click(screen.getByTestId("sidebar-toggle"));
		expect(browserUnseen("sess-orch")).toBe(false);
	});

	it("does not carry an open orchestrator Browser, or re-open it on return, when switching sessions", () => {
		const orchestrator = workerSession("sess-orch");
		const { rerender } = render(<SessionView sessionId="sess-orch" />);
		orchestrator.previewUrl = "https://example.com";
		orchestrator.previewRevision = 1;
		rerender(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(true);
		// Another session whose view already holds a page opens closed: entering a
		// session with an existing target never reveals it.
		const other = workerSession("sess-1");
		other.previewUrl = "https://example.org";
		other.previewRevision = 3;
		rerender(<SessionView sessionId="sess-1" />);
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).not.toBe("browser");
		expect(browserViewOptions.current).toMatchObject({ sessionId: "sess-1", active: false });
		// Coming back keeps the orchestrator's own state; nothing reopens or closes it.
		rerender(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(true);
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).not.toBe("browser");
	});

	it("does not open the orchestrator Browser on passive agent activity, only flags it unseen", () => {
		const { rerender } = render(<SessionView sessionId="sess-orch" />);
		browserViewState.agentBrowserActive = true;
		rerender(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(false);
		expect(browserUnseen("sess-orch")).toBe(true);
	});

	it("switches the browser between its dock and the whole app window immediately", () => {
		const dockRect = {
			x: 780,
			y: 96,
			top: 96,
			left: 780,
			right: 1280,
			bottom: 796,
			width: 500,
			height: 700,
			toJSON: () => ({}),
		} as DOMRect;
		const rect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(dockRect);
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		try {
			render(<SessionView sessionId="sess-1" />);

			expect(screen.getByText("terminal center")).toBeInTheDocument();
			fireEvent.click(screen.getByRole("button", { name: "pop browser" }));

			// Native browser geometry switches atomically. Animating it would require
			// resizing the WebContentsView over IPC on every frame and leaves the
			// portaled URL bar visible until the animation finishes.
			const overlay = document.querySelector(".browser-popout-overlay");
			expect(overlay).toHaveAttribute("data-phase", "open");
			expect(overlay).toHaveClass("browser-popout-overlay--mac-windowed");
			expect(overlay?.querySelector(".browser-popout-titlebar")).toBeInTheDocument();
			expect(overlay?.querySelector(".browser-popout-frame")?.contains(overlay?.querySelector(".browser-popout-titlebar") ?? null)).toBe(false);
			expect(screen.getByRole("button", { name: "browser center" })).toBeInTheDocument();
			expect(screen.getByText("terminal center")).toBeInTheDocument();
			fireEvent.click(screen.getByRole("button", { name: "browser center" }));
			expect(document.querySelector(".browser-popout-overlay")).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "browser center" })).not.toBeInTheDocument();
			expect(screen.getByText("terminal center")).toBeInTheDocument();
			expect(browserDestroy).not.toHaveBeenCalled();
		} finally {
			rect.mockRestore();
		}
	});

	it("does not reserve the traffic-light band during native macOS fullscreen", () => {
		nativeFullScreenMock.mockReturnValue(true);
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "pop browser" }));

		expect(document.querySelector(".browser-popout-overlay")).not.toHaveClass("browser-popout-overlay--mac-windowed");
	});

	it("does not carry popped-out browser visibility into the next session", () => {
		act(() => useUiStore.getState().setInspectorView("sess-1", "browser"));
		const { rerender } = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "pop browser" }));
		expect(browserViewOptions.current).toMatchObject({ sessionId: "sess-1", active: true });

		rerender(<SessionView sessionId="sess-2" />);

		expect(browserViewOptions.current).toMatchObject({ sessionId: "sess-2", active: false });
	});
});
