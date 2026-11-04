import {
	reviewGetMock,
	inspectorVisibilityRenders,
	chatSurfaceRenders,
	sessionPlanState,
	browserViewOptions,
	browserViewState,
	workerSession,
	inspectorOpen,
	browserUnseen,
	inspectorButton,
	render,
} from "./SessionView.test.setup";
import { resetSessionViewState } from "./SessionView.test.reset";
import { StrictMode } from "react";
import {
	act,
	fireEvent,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
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

describe("SessionView", () => {
	function inspectorWidthVariable() {
		return screen
			.getByTestId("panel-group")
			.querySelector<HTMLElement>('[data-slot="inspector-gap"]')
			?.style.getPropertyValue("--tallpine-inspector-w");
	}

	function inspectorPanelWidthVariable() {
		return screen.getByTestId("panel-inspector").style.getPropertyValue("--tallpine-inspector-w");
	}

	beforeEach(() => {
		resetSessionViewState();
	});

	it("keeps reviewer Chat selected when its controller is replaced", async () => {
		const worker = workerSession("sess-1");
		worker.mode = "chat";
		const view = render(<SessionView sessionId="sess-1" />);
		act(() => {
			view.client.setQueryData(["session-reviews", "sess-1"], {
				reviewerHandleId: "review-chat:review-1",
				reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" },
				reviews: [],
				runs: [],
			});
		});

		fireEvent.click(screen.getByRole("button", { name: "open reviewer chat" }));
		expect(screen.getByTestId("reviewer-chat-surface")).toHaveTextContent("review-1");

		act(() => {
			view.client.setQueryData(["session-reviews", "sess-1"], {
				reviewerHandleId: "review-chat:review-2",
				reviewerSurface: { mode: "chat", reviewId: "review-2", harness: "claude-code" },
				reviews: [],
				runs: [],
			});
		});

		await waitFor(() => expect(screen.getByTestId("reviewer-chat-surface")).toHaveTextContent("review-2"));
	});

	it.each([['chat', 'terminal'], ['terminal', 'chat'], ['terminal', 'terminal']] as const)(
		"keeps reviewer focus through a %s to %s replacement", async (before, after) => {
			workerSession("sess-1").mode = "tui";
			const view = render(<SessionView sessionId="sess-1" />);
			act(() => view.client.setQueryData(["session-reviews", "sess-1"], {
				reviewerHandleId: before === "chat" ? "review-chat:review-1" : "old-reviewer",
				...(before === "chat" ? { reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" } } : {}),
				reviewerHarness: "codex", reviews: [], runs: [],
			}));
			fireEvent.click(await screen.findByRole("button", { name: before === "chat" ? "open reviewer chat" : "select reviewer tab" }));
			act(() => view.client.setQueryData(["session-reviews", "sess-1"], {
				reviewerHandleId: after === "chat" ? "review-chat:review-2" : "new-reviewer",
				...(after === "chat" ? { reviewerSurface: { mode: "chat", reviewId: "review-2", harness: "claude-code" } } : {}),
				reviewerHarness: "claude-code", reviews: [], runs: [],
			}));
			await waitFor(() => {
				if (after === "chat") expect(screen.getByTestId("reviewer-chat-surface")).toHaveTextContent("review-2");
				else expect(screen.getByTestId("terminal-target")).toHaveTextContent("reviewer");
			});
		},
	);

	it("keeps the selected reviewer through an empty switch teardown response", async () => {
		const view = render(<SessionView sessionId="sess-1" />);
		act(() => view.client.setQueryData(["session-reviews", "sess-1"], {
			reviewerHandleId: "review-chat:review-1",
			reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" }, reviews: [], runs: [],
		}));
		fireEvent.click(screen.getByRole("button", { name: "open reviewer chat" }));
		let finish!: () => void;
		const mutation = view.client.getMutationCache().build(view.client, {
			mutationKey: ["session-reviews", "sess-1", "switch-reviewer"],
			mutationFn: () => new Promise<void>((resolve) => { finish = resolve; }),
		});
		let pending!: Promise<unknown>;
		await act(async () => { pending = mutation.execute(undefined); await Promise.resolve(); });
		act(() => view.client.setQueryData(["session-reviews", "sess-1"], { reviewerHandleId: "", reviews: [], runs: [] }));
		expect(screen.getByTestId("reviewer-chat-surface")).toHaveTextContent("review-1");
		await act(async () => {
			view.client.setQueryData(["session-reviews", "sess-1"], {
				reviewerHandleId: "review-chat:review-2",
				reviewerSurface: { mode: "chat", reviewId: "review-2", harness: "claude-code" }, reviews: [], runs: [],
			});
			finish(); await pending;
		});
		await waitFor(() => expect(screen.getByTestId("reviewer-chat-surface")).toHaveTextContent("review-2"));
	});

	it("returns to the worker terminal when the selected reviewer Chat disappears", async () => {
		const view = render(<SessionView sessionId="sess-1" />);
		act(() => {
			view.client.setQueryData(["session-reviews", "sess-1"], {
				reviewerHandleId: "review-chat:review-1",
				reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" },
				reviews: [],
				runs: [],
			});
		});

		fireEvent.click(screen.getByRole("button", { name: "open reviewer chat" }));
		expect(screen.getByTestId("reviewer-chat-surface")).toHaveTextContent("review-1");

		act(() => {
			view.client.setQueryData(["session-reviews", "sess-1"], {
				reviewerHandleId: "",
				reviews: [],
				runs: [],
			});
		});

		await waitFor(() => expect(screen.queryByTestId("reviewer-chat-surface")).not.toBeInTheDocument());
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("worker");
	});

	it("returns to the session terminal when the reviewer handle is cleared", async () => {
		const worker = workerSession("sess-1");
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
		reviewGetMock.mockResolvedValueOnce({
			data: { reviewerHandleId: "review-sess-1", reviewerHarness: "codex", reviews: [] },
			error: undefined,
		});

		const view = render(<SessionView sessionId="sess-1" />);
		await screen.findByRole("button", { name: "select reviewer tab" });
		fireEvent.click(screen.getByRole("button", { name: "select reviewer tab" }));
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("reviewer");

		act(() => {
			view.client.setQueryData(["session-reviews", "sess-1"], { reviewerHandleId: "", reviews: [] });
		});

		await waitFor(() => expect(screen.getByTestId("terminal-target")).toHaveTextContent("worker"));
		expect(screen.queryByRole("button", { name: "select reviewer tab" })).not.toBeInTheDocument();
	});

	it.each(["tui", "chat"] as const)(
		"restores the selected reviewer terminal when a %s session becomes active again",
		async (mode) => {
			const worker = workerSession("sess-1");
			worker.mode = mode;
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
			reviewGetMock.mockResolvedValueOnce({
				data: { reviewerHandleId: "review-sess-1", reviewerHarness: "codex", reviews: [] },
				error: undefined,
			});

			const view = render(<SessionView sessionId="sess-1" />);
			const reviewerButtonName = mode === "chat" ? "Reviewer" : "select reviewer tab";
			await screen.findByRole("button", { name: reviewerButtonName });
			fireEvent.click(screen.getByRole("button", { name: reviewerButtonName }));
			expect(screen.getByTestId("terminal-target")).toHaveTextContent("reviewer");

			worker.status = "terminated";
			worker.isTerminated = true;
			view.rerender(<SessionView sessionId="sess-1" />);
			if (mode === "chat") expect(screen.getByTestId("chat-surface")).toBeInTheDocument();
			expect(screen.getByTestId("terminal-target")).toHaveTextContent("reviewer");
			expect(screen.queryByRole("button", { name: reviewerButtonName })).not.toBeInTheDocument();

			worker.status = "working";
			worker.isTerminated = false;
			view.rerender(<SessionView sessionId="sess-1" />);

			await screen.findByRole("button", { name: reviewerButtonName });
			expect(screen.getByTestId("terminal-target")).toHaveTextContent("reviewer");
		},
	);

	it("opens the inspector with the shared sidebar spring tokens", () => {
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("panel-group")).toHaveStyle({
			"--session-inspector-motion-duration": "300ms",
			"--session-inspector-motion-easing":
				"linear(0, 0.333 12.5%, 0.642 25%, 0.813 37.5%, 0.902 50%, 0.949 62.5%, 0.974 75%, 0.986 87.5%, 1)",
		});
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");
		expect(screen.getByTestId("inspector-resize-handle")).toBeInTheDocument();
	});

	it("keeps the inspector closed when a session opens, then opens it on Summary", () => {
		useUiStore.setState({ inspectorSessions: {} });
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByText("terminal center")).toBeInTheDocument();
		expect(inspectorOpen("sess-1")).toBe(false);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "collapsed");
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("inert");
		expect(screen.getByTestId("sidebar-toggle")).toHaveAttribute("aria-pressed", "false");

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));

		expect(inspectorOpen("sess-1")).toBe(true);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");
		expect(inspectorButton()).toHaveAttribute("data-view", "summary");
	});

	it("treats a merged terminated session as terminated for Browser preview", () => {
		const worker = workerSession("sess-1");
		worker.status = "merged";
		worker.isTerminated = true;

		render(<SessionView sessionId="sess-1" />);

		expect(browserViewOptions.current).toMatchObject({ sessionId: "sess-1", terminated: true });
	});

	it("keeps Browser live for a terminated session showing an opened artifact preview", () => {
		const worker = workerSession("sess-1");
		worker.status = "merged";
		worker.isTerminated = true;
		worker.previewUrl = "http://ao-preview-artifact.abc.localhost:3101/report.html";

		render(<SessionView sessionId="sess-1" />);

		expect(browserViewOptions.current).toMatchObject({ sessionId: "sess-1", terminated: false });
	});

	it("still tears Browser down for a terminated session with a workspace preview", () => {
		const worker = workerSession("sess-1");
		worker.status = "merged";
		worker.isTerminated = true;
		worker.previewUrl = "http://ao-preview.abc.localhost:3101/index.html";

		render(<SessionView sessionId="sess-1" />);

		expect(browserViewOptions.current).toMatchObject({ sessionId: "sess-1", terminated: true });
	});

	it("mounts the inspector open when the store says open", () => {
		render(<SessionView sessionId="sess-1" />);

		const pane = screen.getByTestId("panel-inspector");
		expect(pane).not.toHaveAttribute("inert");
		expect(pane).toHaveAttribute("aria-hidden", "false");
		expect(pane).toHaveAttribute("data-state", "expanded");
	});

	it("mounts collapsed and inert when the store says closed", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		render(<SessionView sessionId="sess-1" />);

		const pane = screen.getByTestId("panel-inspector");
		expect(pane).toHaveAttribute("inert");
		expect(pane).toHaveAttribute("aria-hidden", "true");
		expect(pane).toHaveAttribute("data-state", "collapsed");
		expect(pane).toHaveAttribute("hidden");
		expect(screen.getByTestId("inspector-resize-handle")).toHaveClass("hidden");
		expect(screen.getByTestId("sidebar-toggle")).toHaveAttribute("aria-pressed", "false");
	});

	it("keeps inspector chrome visible from the first render of the opening transition", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		render(<SessionView sessionId="sess-1" />);
		const renderCountBeforeOpening = inspectorVisibilityRenders.length;

		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));

		const openingRenders = inspectorVisibilityRenders.slice(renderCountBeforeOpening);
		expect(openingRenders.length).toBeGreaterThan(0);
		expect(openingRenders).not.toContain(false);
	});

	it("does not rerender the memoized chat surface when the inspector toggles", () => {
		render(<SessionView sessionId="sess-1" />);
		const rendersBeforeToggle = chatSurfaceRenders.length;

		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));

		expect(chatSurfaceRenders).toHaveLength(rendersBeforeToggle);
	});

	it("starts collapsing the resize handle with the inspector panel", () => {
		render(<SessionView sessionId="sess-1" />);

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));

		expect(screen.getByTestId("inspector-resize-handle")).toHaveClass("hidden");
		expect(within(screen.getByTestId("session-pinned-actions")).getByTestId("sidebar-toggle")).toHaveAttribute("aria-pressed", "false");
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("aria-hidden", "false");
	});

	it("keeps the live browser active throughout the inspector close transition", () => {
		render(<SessionView sessionId="sess-1" />);
		act(() => useUiStore.getState().setInspectorView("sess-1", "browser"));
		expect(browserViewOptions.current).toMatchObject({ active: true });

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));

		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "collapsed");
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("aria-hidden", "false");
		expect(browserViewOptions.current).toMatchObject({ active: true });
	});

	it("keeps StrictMode mount from collapsing, then collapses on the first user toggle", () => {
		render(
			<StrictMode>
				<SessionView sessionId="sess-1" />
			</StrictMode>,
		);

		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));

		expect(inspectorOpen("sess-1")).toBe(false);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "collapsed");
	});

	it("marks the split for live terminal fitting throughout the inspector transition", () => {
		vi.useFakeTimers();
		try {
			render(<SessionView sessionId="sess-1" />);

			act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
			const split = screen.getByTestId("panel-group");
			expect(split).toHaveAttribute("data-terminal-live-resize", "true");
			expect(split).toHaveAttribute("data-topbar-secondary-label-mode", "expanded");

			act(() => vi.advanceTimersByTime(299));
			expect(split).toHaveAttribute("data-terminal-live-resize", "true");
			expect(split).toHaveAttribute("data-topbar-secondary-label-mode", "expanded");

			act(() => vi.advanceTimersByTime(1));
			expect(split).not.toHaveAttribute("data-terminal-live-resize");
			expect(split).not.toHaveAttribute("data-topbar-secondary-label-mode");
		} finally {
			vi.useRealTimers();
		}
	});

	it("keeps inspector labels expanded at the default width throughout the opening transition", () => {
		vi.useFakeTimers();
		try {
			act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
			render(<SessionView sessionId="sess-1" />);

			act(() => useUiStore.getState().setInspectorOpen("sess-1", true));

			const split = screen.getByTestId("panel-group");
			expect(split).toHaveAttribute("data-terminal-live-resize", "true");
			expect(split).toHaveAttribute("data-inspector-label-mode", "expanded");
			expect(split).toHaveAttribute("data-topbar-secondary-label-mode", "compact");

			act(() => vi.advanceTimersByTime(299));
			expect(split).toHaveAttribute("data-inspector-label-mode", "expanded");
			expect(split).toHaveAttribute("data-topbar-secondary-label-mode", "compact");

			act(() => vi.advanceTimersByTime(1));
			expect(split).not.toHaveAttribute("data-inspector-label-mode");
			expect(split).not.toHaveAttribute("data-topbar-secondary-label-mode");
		} finally {
			vi.useRealTimers();
		}
	});

	it("locks responsive inspector labels compact when the opening target is narrow", () => {
		vi.useFakeTimers();
		const clientWidth = vi
			.spyOn(HTMLElement.prototype, "clientWidth", "get")
			.mockImplementation(function (this: HTMLElement) {
				return this.dataset.testid === "panel-group" ? 640 : 640;
			});
		try {
			act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
			render(<SessionView sessionId="sess-1" />);

			act(() => useUiStore.getState().setInspectorOpen("sess-1", true));

			const split = screen.getByTestId("panel-group");
			expect(split).toHaveAttribute("data-inspector-label-mode", "compact");
			expect(split).toHaveAttribute("data-topbar-secondary-label-mode", "compact");
		} finally {
			clientWidth.mockRestore();
			vi.useRealTimers();
		}
	});

	it("keeps StrictMode mount from expanding, then expands on the first user toggle", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		render(
			<StrictMode>
				<SessionView sessionId="sess-1" />
			</StrictMode>,
		);

		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "collapsed");

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));

		expect(inspectorOpen("sess-1")).toBe(true);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");
	});

	it("toggles the inspector from the main-process shortcut", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
		expect(inspectorOpen("sess-1")).toBe(false);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "collapsed");

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
		expect(inspectorOpen("sess-1")).toBe(true);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");

		// The sidebar toggle is a different command — the inspector must not react.
		act(() => appShortcutBus.emit({ id: "toggle-sidebar" }));
		expect(inspectorOpen("sess-1")).toBe(true);
	});

	it("opens the changed-files diff with the diff shortcut and closes it on the second press", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		render(<SessionView sessionId="sess-1" />);

		act(() => appShortcutBus.emit({ id: "toggle-diff" }));
		expect(inspectorOpen("sess-1")).toBe(true);
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("files");
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.filesChangedOnly ?? true).toBe(true);

		act(() => appShortcutBus.emit({ id: "toggle-diff" }));
		expect(inspectorOpen("sess-1")).toBe(false);
	});

	it("shows the changed-files diff, not the full tree, when the diff shortcut finds the Files view in full-tree mode", () => {
		act(() => {
			useUiStore.getState().setInspectorOpen("sess-1", true);
			useUiStore.getState().setInspectorView("sess-1", "files");
			useUiStore.getState().setFilesChangedOnly("sess-1", false);
		});
		render(<SessionView sessionId="sess-1" />);

		act(() => appShortcutBus.emit({ id: "toggle-diff" }));

		expect(inspectorOpen("sess-1")).toBe(true);
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.filesChangedOnly).toBe(true);
	});

	it("reopens the inspector on the tab it last showed", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);
		// Mounting applies the "entering a session" defaults, so pick the tab afterwards.
		act(() => useUiStore.getState().setInspectorView("sess-1", "browser"));

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
		expect(inspectorOpen("sess-1")).toBe(false);

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
		expect(inspectorOpen("sess-1")).toBe(true);
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("browser");
	});

	it("keeps one sidebar toggle at the far right of the top bar, open or closed, after the panel switcher", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);
		const pinned = screen.getByTestId("session-pinned-actions");
		expect(screen.getAllByTestId("sidebar-toggle")).toHaveLength(1);
		const toggle = within(pinned).getByTestId("sidebar-toggle");
		expect(toggle).toHaveAttribute("aria-pressed", "true");
		// The panel header no longer carries a toggle; the switcher slot precedes the toggle.
		expect(within(screen.getByTestId("panel-inspector")).queryByTestId("sidebar-toggle")).toBeNull();
		expect(pinned.lastElementChild).toBe(toggle);

		fireEvent.click(toggle);

		expect(inspectorOpen("sess-1")).toBe(false);
		const closed = within(screen.getByTestId("session-pinned-actions")).getByTestId("sidebar-toggle");
		expect(closed).toHaveAttribute("aria-pressed", "false");
	});

	it("keeps session chrome expanded when Browser is active", () => {
		useUiStore.setState({
			isSidebarOpen: true,
			inspectorSessions: { "sess-1": { initialized: true, isOpen: true, view: "browser" } },
		});

		render(<SessionView sessionId="sess-1" />);

		const topbar = screen.getByTestId("mock-session-topbar");
		expect(topbar).toHaveAttribute("data-compact-actions", "false");
		expect(topbar.closest("[data-compact-session-chrome]")).toHaveAttribute(
			"data-compact-session-chrome",
			"false",
		);
	});

	it("never shrinks the inspector when entering Browser", async () => {
		window.localStorage.setItem("ao.inspector.widthPx", "720");
		window.localStorage.setItem("ao.workspace.browser.canvasWidthPx", "460");
		render(<SessionView sessionId="sess-1" />);
		expect(inspectorWidthVariable()).toBe("720px");

		fireEvent.click(screen.getByRole("tab", { name: "Browser" }));
		await waitFor(() => {
			expect(inspectorWidthVariable()).toBe("720px");
		});
	});

	it("restores and clamps the persisted inspector width in pixels", () => {
		window.localStorage.setItem("ao.inspector.widthPx", "240");
		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		render(<SessionView sessionId="sess-1" />);
		expect(inspectorWidthVariable()).toBe("340px");
	});

	it("resizes the inspector panel and terminal gap together once per frame while dragging", () => {
		render(<SessionView sessionId="sess-1" />);
		const handle = screen.getByTestId("inspector-resize-handle");
		expect(document.documentElement.style.getPropertyValue("--tallpine-inspector-w")).toBe("");
		const frames: FrameRequestCallback[] = [];
		const raf = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			frames.push(callback); return frames.length;
		});
		try {
			const initialWidth = inspectorWidthVariable();
			fireEvent.pointerDown(handle, { clientX: 100 });
			fireEvent.pointerMove(window, { clientX: 180 });
			fireEvent.pointerMove(window, { clientX: 200 });
			expect(inspectorWidthVariable()).toBe(initialWidth);
			expect(inspectorPanelWidthVariable()).toBe(initialWidth);
			expect(frames).toHaveLength(1);
			act(() => frames.shift()!(performance.now()));
			expect(inspectorWidthVariable()).toBe("400px");
			expect(inspectorPanelWidthVariable()).toBe("400px");
			fireEvent.pointerUp(window);
			expect(inspectorWidthVariable()).toBe("400px");
			expect(inspectorPanelWidthVariable()).toBe("400px");
		} finally { raf.mockRestore(); }
	});

	it("grows Browser into a co-work canvas while utility surfaces (Files included) stay consistent", async () => {
		render(<SessionView sessionId="sess-1" />);
		expect(screen.getByTestId("panel-group")).toHaveAttribute("data-workspace-mode", "utility");
		expect(inspectorWidthVariable()).toBe("500px");

		act(() => useUiStore.getState().setInspectorView("sess-1", "browser"));
		await waitFor(() => {
			expect(screen.getByTestId("panel-group")).toHaveAttribute("data-workspace-mode", "browser");
			expect(inspectorWidthVariable()).toBe("900px");
			expect(
				screen.getByTestId("panel-group").style.getPropertyValue("--session-inspector-max-width"),
			).toBe("min(68%, max(300px, calc(100% - 440px)))");
		});

		act(() => useUiStore.getState().setInspectorView("sess-1", "files"));
		await waitFor(() => {
			expect(screen.getByTestId("panel-group")).toHaveAttribute("data-workspace-mode", "files");
			// Files sizes like Summary/Review: opening it doesn't widen the panel.
			expect(inspectorWidthVariable()).toBe("500px");
			expect(
				screen.getByTestId("panel-group").style.getPropertyValue("--session-inspector-max-width"),
			).toBe("min(55%, max(300px, calc(100% - 560px)))");
		});

		act(() => useUiStore.getState().setInspectorView("sess-1", "summary"));
		await waitFor(() => {
			expect(screen.getByTestId("panel-group")).toHaveAttribute("data-workspace-mode", "utility");
			expect(inspectorWidthVariable()).toBe("500px");
		});
	});

	it("keeps Browser entry and utility return on one complete workspace spring", () => {
		vi.useFakeTimers();
		try {
			render(<SessionView sessionId="sess-1" />);
			fireEvent.click(screen.getByRole("tab", { name: "Browser" }));

			const split = screen.getByTestId("panel-group");
			expect(split).toHaveAttribute("data-workspace-resizing", "true");
			expect(inspectorWidthVariable()).toBe("900px");
			act(() => vi.advanceTimersByTime(300));
			expect(split).not.toHaveAttribute("data-workspace-resizing");

			fireEvent.click(screen.getByRole("tab", { name: "Summary" }));
			expect(split).toHaveAttribute("data-workspace-resizing", "true");
			expect(inspectorWidthVariable()).toBe("500px");
			act(() => vi.advanceTimersByTime(300));
			expect(split).not.toHaveAttribute("data-workspace-resizing");
		} finally {
			vi.useRealTimers();
		}
	});

	it("restores a separate user-sized Browser workspace without changing utility width", () => {
		window.localStorage.setItem("ao.workspace.browser.canvasWidthPx", "820");
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("tab", { name: "Browser" }));
		expect(inspectorWidthVariable()).toBe("820px");
		expect(useUiStore.getState().isSidebarOpen).toBe(true);

		fireEvent.click(screen.getByRole("tab", { name: "Summary" }));
		expect(inspectorWidthVariable()).toBe("500px");
	});

	it("grows each tab's width back when the window widens after a narrow tab switch", async () => {
		// A 712px split caps every profile at 300px. A tab switch re-runs the restore
		// against that cap; widening the window must bring the preferred width back
		// instead of leaving the rail (and its top bar) pinned narrow.
		let splitWidth = 712;
		const clientWidth = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(() => splitWidth);
		try {
			render(<SessionView sessionId="sess-1" />);
			fireEvent.click(screen.getByRole("tab", { name: "Browser" }));
			await waitFor(() => expect(inspectorWidthVariable()).toBe("300px"));
			fireEvent.click(screen.getByRole("tab", { name: "Summary" }));
			fireEvent.click(screen.getByRole("tab", { name: "Browser" }));
			await waitFor(() => expect(inspectorWidthVariable()).toBe("300px"));

			splitWidth = 2000;
			act(() => {
				window.dispatchEvent(new Event("resize"));
			});
			await waitFor(() => expect(inspectorWidthVariable()).toBe("900px"));

			act(() => useUiStore.getState().setInspectorView("sess-1", "files"));
			await waitFor(() => expect(inspectorWidthVariable()).toBe("500px"));
			// Nothing about the narrow window was saved as a preference.
			expect(window.localStorage.getItem("ao.inspector.widthPx")).toBeNull();
			expect(window.localStorage.getItem("ao.workspace.browser.canvasWidthPx")).toBeNull();
		} finally {
			clientWidth.mockRestore();
		}
	});

	it("brings Files back to its own floor when the window widens after a narrow switch", async () => {
		window.localStorage.setItem("ao.inspector.widthPx", "400");
		let splitWidth = 712;
		const clientWidth = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(() => splitWidth);
		try {
			render(<SessionView sessionId="sess-1" />);
			act(() => useUiStore.getState().setInspectorView("sess-1", "files"));
			await waitFor(() => expect(inspectorWidthVariable()).toBe("300px"));

			splitWidth = 2000;
			act(() => {
				window.dispatchEvent(new Event("resize"));
			});
			await waitFor(() => expect(inspectorWidthVariable()).toBe("460px"));
			act(() => useUiStore.getState().setInspectorView("sess-1", "summary"));
			await waitFor(() => expect(inspectorWidthVariable()).toBe("400px"));
		} finally {
			clientWidth.mockRestore();
		}
	});

	it("shares the utility width with Files but keeps Files at least 460px wide", async () => {
		window.localStorage.setItem("ao.inspector.widthPx", "400");
		render(<SessionView sessionId="sess-1" />);
		expect(inspectorWidthVariable()).toBe("400px");

		act(() => useUiStore.getState().setInspectorView("sess-1", "files"));
		await waitFor(() => expect(inspectorWidthVariable()).toBe("460px"));

		act(() => useUiStore.getState().setInspectorView("sess-1", "summary"));
		await waitFor(() => expect(inspectorWidthVariable()).toBe("400px"));
	});

	it("never changes the sidebar preference while browser surfaces open and close", async () => {
		render(<SessionView sessionId="sess-1" />);
		expect(useUiStore.getState().isSidebarOpen).toBe(true);

		fireEvent.click(screen.getByRole("tab", { name: "Reviews" }));
		expect(useUiStore.getState().isSidebarOpen).toBe(true);

		fireEvent.click(screen.getByRole("tab", { name: "Browser" }));
		expect(useUiStore.getState().isSidebarOpen).toBe(true);

		fireEvent.click(screen.getByRole("tab", { name: "Summary" }));
		expect(useUiStore.getState().isSidebarOpen).toBe(true);

		fireEvent.click(screen.getByRole("tab", { name: "Browser" }));
		fireEvent.click(screen.getByRole("button", { name: "open files" }));
		expect(useUiStore.getState().isSidebarOpen).toBe(true);

		fireEvent.click(screen.getByRole("tab", { name: "Browser" }));
		fireEvent.click(screen.getByTestId("sidebar-toggle"));
		await waitFor(() => expect(useUiStore.getState().isSidebarOpen).toBe(true));
	});

	it.each([[false, true], [true, false]])(
		"restores the inspector immediately when navigating from %s to %s",
		(from, to) => {
			act(() => {
				useUiStore.getState().setInspectorOpen("sess-1", from);
				useUiStore.getState().setInspectorOpen("sess-2", to);
			});
			const { rerender } = render(<SessionView sessionId="sess-1" />);
			rerender(<SessionView sessionId="sess-2" />);

			const pane = screen.getByTestId("panel-inspector");
			expect(pane).toHaveAttribute("data-state", to ? "expanded" : "collapsed");
			if (to) expect(pane).not.toHaveAttribute("hidden");
			else {
				expect(pane).toHaveAttribute("hidden");
				expect(pane).toHaveAttribute("inert");
			}
			expect(screen.getByTestId("panel-group")).not.toHaveAttribute("data-workspace-resizing");
			expect(inspectorOpen("sess-1")).toBe(from);
			expect(inspectorOpen("sess-2")).toBe(to);
		},
	);

	it("mounts the inspector in sync when navigating from an orchestrator session", () => {
		const { rerender } = render(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(false);

		act(() => useUiStore.getState().setInspectorOpen("sess-1", true));
		rerender(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");
		expect(screen.getByTestId("panel-inspector")).not.toHaveAttribute("inert");
	});

	it("expands on the first toggle after a closed worker inspector remounts", () => {
		act(() => useUiStore.getState().setInspectorOpen("sess-1", false));
		const { rerender } = render(<SessionView sessionId="sess-1" />);

		act(() => useUiStore.getState().setInspectorOpen("sess-2", false));
		rerender(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(false);

		act(() => useUiStore.getState().setInspectorOpen("sess-2", false));
		rerender(<SessionView sessionId="sess-2" />);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "collapsed");

		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));

		expect(inspectorOpen("sess-2")).toBe(true);
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-state", "expanded");
	});

	it("starts the orchestrator Browser closed and opens it with the inspector shortcut", () => {
		render(<SessionView sessionId="sess-orch" />);
		expect(inspectorOpen("sess-orch")).toBe(false);
		expect(screen.getByTestId("sidebar-toggle")).toHaveAttribute("aria-pressed", "false");
		act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
		expect(inspectorOpen("sess-orch")).toBe(true);
		expect(screen.getByTestId("sidebar-toggle")).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-view", "browser");
	});

	it("lets an orchestrator's inspector show the Plan tab but no other view", () => {
		sessionPlanState.plan = { spansTurns: true, steps: [{ text: "x", status: "pending" }] };
		try {
			render(<SessionView sessionId="sess-orch" />);
			act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
			act(() => useUiStore.getState().setInspectorView("sess-orch", "plan"));
			expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-view", "plan");
			act(() => useUiStore.getState().setInspectorView("sess-orch", "files"));
			expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-view", "plan");
			act(() => useUiStore.getState().setInspectorView("sess-orch", "browser"));
			expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-view", "browser");
		} finally {
			sessionPlanState.plan = undefined;
		}
	});

	it("keeps an orchestrator without a plan on Browser when a plan arrives after a preview reveal", () => {
		const orchestrator = workerSession("sess-orch");
		const { rerender } = render(<SessionView sessionId="sess-orch" />);
		orchestrator.previewUrl = "https://example.com";
		orchestrator.previewRevision = 1;
		rerender(<SessionView sessionId="sess-orch" />);
		browserViewState.agentBrowserActive = true;
		rerender(<SessionView sessionId="sess-orch" />);
		expect(browserUnseen("sess-orch")).toBe(false);
		sessionPlanState.plan = { spansTurns: true, steps: [{ text: "x", status: "pending" }] };
		try {
			rerender(<SessionView sessionId="sess-orch" />);
			expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-view", "browser");
		} finally {
			sessionPlanState.plan = undefined;
		}
	});

	it("opens a fresh orchestrator with a plan on Plan, not Browser", () => {
		sessionPlanState.plan = { spansTurns: true, steps: [{ text: "x", status: "pending" }] };
		try {
			render(<SessionView sessionId="sess-orch" />);
			act(() => appShortcutBus.emit({ id: "toggle-inspector" }));
			expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-view", "plan");
		} finally {
			sessionPlanState.plan = undefined;
		}
	});
});
