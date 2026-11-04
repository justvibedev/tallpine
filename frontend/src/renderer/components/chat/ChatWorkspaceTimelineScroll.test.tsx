// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import {
	cleanupChatWorkspaceTest,
	resetChatWorkspaceMocks,
} from "./ChatWorkspace.test.setup";
import {
	act,
	fireEvent,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Profiler } from "react";
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { ChatWorkspace, promptSpacerHeight, promptTopInset } from "./ChatWorkspace";
import {
	chatFixture,
	chatFixtureEmpty,
	chatFixtureLongHistory,
	chatFixtureMcpFailed,
	chatFixtureSettled,
	chatFixtureThreadError,
} from "../../lib/chat-fixture";
import type { ConversationMessage } from "../../types/conversation";
import { useUiStore } from "../../stores/ui-store";
import { renderChat as render } from "../../test/render-chat";
import {
	poll,
	idleSnapshot,
	stubGeometry,
	humanMessage,
	startingSnapshot,
	startingSteps,
	chatSession,
} from "./ChatWorkspaceTestHelpers";

let restoreTimelineGeometry: (() => void) | undefined;

/** Emulate browser geometry and scroll range without mocking the virtualizer. */
function stubVirtualTimelineGeometry(rowHeight: (index: number) => number = () => 600) {
	const bounds = HTMLElement.prototype.getBoundingClientRect;
	const height = Object.getOwnPropertyDescriptor(Element.prototype, "clientHeight")!.get!;
	const scrollHeight = Object.getOwnPropertyDescriptor(Element.prototype, "scrollHeight")!.get!;
	const offsetHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "offsetHeight")!.get!;
	const spies = [
		vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
			if (this.hasAttribute("data-index")) {
				const log = this.closest<HTMLElement>('[role="log"]');
				const offset = this.style.transform
					? Number(this.style.transform.match(/translateY\(([-\d.]+)px\)/)?.[1] ?? 0)
					: Number(this.dataset.index) * 618;
				return { ...bounds.call(this), top: 20 + offset - (log?.scrollTop ?? 0), height: rowHeight(Number(this.dataset.index)), width: 768 } as DOMRect;
			}
			if (this.classList.contains("relative") && this.style.height) {
				const log = this.closest<HTMLElement>('[role="log"]');
				return { ...bounds.call(this), top: 20 - (log?.scrollTop ?? 0), height: Number.parseFloat(this.style.height), width: 768 } as DOMRect;
			}
			return bounds.call(this);
		}),
		vi.spyOn(Element.prototype, "clientHeight", "get").mockImplementation(function (this: Element) {
			return this.getAttribute("role") === "log" ? 800 : height.call(this);
		}),
		vi.spyOn(Element.prototype, "scrollHeight", "get").mockImplementation(function (this: Element) {
			if (this.getAttribute("role") !== "log") return scrollHeight.call(this);
			const virtual = this.querySelector<HTMLElement>('.relative[style*="height"]');
			const legacyHeight = virtual ? 0 : this.querySelectorAll("[data-chat-scroll-anchor]").length * 618;
			return 20 + legacyHeight + Array.from(this.querySelectorAll<HTMLElement>('[style*="height"]'))
				.reduce((sum, node) => sum + (Number.parseFloat(node.style.height) || 0), 0);
		}),
		vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(function (this: HTMLElement) {
			return this.hasAttribute("data-index") ? rowHeight(Number(this.dataset.index)) : Number.parseFloat(this.style.height) || offsetHeight.call(this);
		}),
	];
	restoreTimelineGeometry = () => spies.forEach((spy) => spy.mockRestore());
}

beforeEach(resetChatWorkspaceMocks);

afterEach(async () => {
	restoreTimelineGeometry?.();
	restoreTimelineGeometry = undefined;
	await cleanupChatWorkspaceTest();
});

describe("ChatWorkspace timeline", () => {
	it("shows startup failure and retry for an orchestrator with no messages or turns", async () => {
		const user = userEvent.setup();
		const resume = vi.fn();
		render(
			<ChatWorkspace
				sessionRole="orchestrator"
				snapshot={{ ...chatFixtureEmpty, controller: { state: "stopped" } }}
				session={{ ...chatSession, kind: "orchestrator", provisionState: "failed", provisionError: "branch already checked out in another worktree", provisionSteps: startingSteps("running") }}
				onResumeAgent={resume}
			/>,
		);
		expect(screen.getByRole("alert")).toHaveTextContent("Session setup failed");
		expect(screen.getByTestId("orchestrator-startup-status")).toHaveTextContent("branch already checked out in another worktree");
		expect(screen.getByText("What do you want to work on?")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Retry start" }));
		expect(resume).toHaveBeenCalledOnce();
	});

	it("keeps a failed start's checklist with the failed step and Retry", async () => {
		const user = userEvent.setup();
		const resume = vi.fn();
		render(
			<ChatWorkspace
				snapshot={{ ...startingSnapshot("queued"), controller: { state: "stopped" } }}
				session={{
					...chatSession,
					provisionState: "failed",
					provisionError: "spawn mer-1: create workspace: branch already checked out",
					provisionSteps: startingSteps("running").map((step) =>
						step.id === "worktree" ? { ...step, status: "running", endedAt: undefined }
							: step.id === "agent" ? { id: "agent", status: "pending" } : step,
					),
				}}
				onResumeAgent={resume}
			/>,
		);

		const startup = screen.getByTestId("session-startup");
		expect(screen.getByRole("alert")).toHaveTextContent("Session setup failed");
		expect(startup).toHaveTextContent("branch already checked out");
		expect(startup.querySelector('[data-step="worktree"]')).toHaveAttribute("data-status", "failed");
		expect(screen.getByText("Fix clicking attachments")).toBeInTheDocument();
		expect(screen.queryByText("The agent controller stopped")).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Retry start" }));
		expect(resume).toHaveBeenCalledOnce();
	});

	it("shows connecting during the controller gap, then restores the composer when ready", async () => {
		const { rerender } = render(
			<ChatWorkspace
				snapshot={{
					...chatFixtureSettled,
					controller: { state: "stopped" },
				}}
				controllerTransitioning
				onResumeAgent={vi.fn()}
				onOpenShell={vi.fn()}
			/>,
		);

		expect(screen.queryByText("The agent controller stopped")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Resume agent" })).not.toBeInTheDocument();
		expect(screen.getByTestId("chat-conversation-panel")).toHaveAttribute("inert");
		expect(screen.queryByText("Connecting to the agent…")).not.toBeInTheDocument();
		expect(screen.queryByText("The controller is not connected")).not.toBeInTheDocument();
		expect(screen.getByText("Starting the chat agent")).toBeInTheDocument();
		expect(document.querySelector(".cursor-chat-composer[data-starting]")).not.toBeNull();
		expect(screen.getByRole("combobox", { name: "Message the agent" })).toHaveAttribute("contenteditable", "false");

		rerender(
			<ChatWorkspace
				snapshot={{ ...chatFixtureSettled, controller: { state: "connecting" } }}
				controllerTransitioning
			/>,
		);
		expect(await screen.findByText("Restoring your conversation")).toBeInTheDocument();

		rerender(<ChatWorkspace snapshot={chatFixtureEmpty} />);
		expect(screen.queryByText("Restoring your conversation")).not.toBeInTheDocument();
		expect(document.querySelector(".cursor-chat-composer[data-starting]")).toBeNull();
		expect(screen.getByRole("combobox", { name: "Message the agent" })).toHaveAttribute("contenteditable", "true");
	});

	it("docks the composer at the bottom while arriving in a chat that already has messages", () => {
		render(
			<ChatWorkspace
				snapshot={{ ...chatFixtureEmpty, controller: { state: "connecting" } }}
				session={{ ...chatSession, lastUserMessageAt: "2026-10-09T00:00:00Z" }}
				controllerTransitioning
			/>,
		);
		const placement = document.querySelector("[data-composer-placement]");
		expect(placement).toHaveAttribute("data-composer-placement", "dock");
		expect(placement).toHaveClass("justify-end");
	});

	it("fades the transcript in only when messages load during an interface switch", () => {
		const loaded = { ...chatFixtureEmpty, items: [humanMessage("hello")] };
		const navigation = render(<ChatWorkspace snapshot={chatFixtureEmpty} />);
		navigation.rerender(<ChatWorkspace snapshot={loaded} />);
		expect(document.querySelector(".chat-transcript-reveal")).toBeNull();
		navigation.unmount();
		const arrival = render(<ChatWorkspace snapshot={chatFixtureEmpty} session={chatSession} controllerTransitioning />);
		arrival.rerender(<ChatWorkspace snapshot={loaded} session={chatSession} controllerTransitioning />);
		expect(document.querySelector(".chat-transcript-reveal")).not.toBeNull();
	});

	it("centers the composer while arriving in a chat with no messages yet", () => {
		render(
			<ChatWorkspace
				snapshot={{ ...chatFixtureEmpty, controller: { state: "connecting" } }}
				session={{ ...chatSession, lastUserMessageAt: undefined }}
				controllerTransitioning
			/>,
		);
		expect(document.querySelector("[data-composer-placement]")).toHaveAttribute("data-composer-placement", "center");
	});

	it("keeps the composer quiet while leaving chat for the terminal", () => {
		render(
			<ChatWorkspace
				snapshot={{ ...chatFixtureSettled, controller: { state: "stopped" } }}
				controllerTransitioning
				newWorkDisabled
			/>,
		);
		expect(screen.queryByText("Starting the chat agent")).not.toBeInTheDocument();
		expect(document.querySelector(".cursor-chat-composer[data-starting]")).toBeNull();
	});

	it("keeps history readable while a stopped agent resumes after opening", () => {
		render(
			<ChatWorkspace
				snapshot={{
					...chatFixtureSettled,
					controller: { state: "stopped" },
				}}
				agentResuming
				onResumeAgent={vi.fn()}
				onOpenShell={vi.fn()}
			/>,
		);

		expect(screen.getByTestId("chat-conversation-panel")).not.toHaveAttribute("inert");
		expect(screen.queryByText("The agent controller stopped")).not.toBeInTheDocument();
		expect(screen.getByText("Resuming agent")).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Message the agent" })).toHaveAttribute("contenteditable", "false");
	});

	it("announces thread and tool-server failures", () => {
		const { rerender } = render(<ChatWorkspace snapshot={chatFixtureThreadError} />);
		expect(screen.getByRole("alert")).toHaveTextContent("thread hit an internal error");

		rerender(<ChatWorkspace snapshot={chatFixtureMcpFailed} />);
		// The live turn's Working row is a status too, so pick out the tool-server note.
		expect(
			screen.getAllByRole("status").find((status) => /didn’t start/.test(status.textContent ?? "")),
		).toBeInTheDocument();
	});

	it("reuses anchor measurements while scrolling and refreshes after content mutations", () => {
		useUiStore.setState({ inspectorSessions: { "tallpine-long": { isOpen: false, view: "summary" } } });
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(8)} />);
		const log = screen.getByRole("log");
		stubGeometry(log, { scrollHeight: 4000, clientHeight: 800, scrollTop: 1000 });
		const anchors = Array.from(log.querySelectorAll<HTMLElement>("[data-chat-scroll-anchor]"));
		const reads = anchors.map((anchor) => vi.spyOn(anchor, "getBoundingClientRect"));
		fireEvent.scroll(log);
		for (const read of reads) read.mockClear();
		for (let i = 0; i < 5; i++) {
			log.scrollTop += 10;
			fireEvent.scroll(log);
		}
		expect(reads.reduce((total, read) => total + read.mock.calls.length, 0)).toBe(0);
		// Same overall content height can hide a changed prompt position. Detect
		// DOM mutations too, including those queued before the observer callback.
		reads[0]!.mockReturnValue({ top: 1000, height: 20 } as DOMRect);
		anchors[0]!.setAttribute("style", "padding-top: 20px");
		fireEvent.scroll(log);
		expect(reads.every((read) => read.mock.calls.length > 0)).toBe(true);
		const marker = screen.getByRole("scrollbar", { name: "Conversation scrollbar" }).querySelector<HTMLElement>("[data-chat-scroll-marker]");
		expect(Number(marker?.dataset.scrollTarget)).toBe(1660);
	});

	it("provides an interactive conversation minimap", () => {
		useUiStore.setState({
			inspectorSessions: {
				"tallpine-long": { isOpen: false, view: "summary" },
			},
		});
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(8)} />);
		const log = screen.getByRole("log");
		const scrollbar = screen.getByRole("scrollbar", {
			name: "Conversation scrollbar",
		});
		stubGeometry(log, {
			scrollHeight: 4000,
			clientHeight: 800,
			scrollTop: 1000,
		});
		stubGeometry(scrollbar, {
			scrollHeight: 800,
			clientHeight: 800,
			scrollTop: 0,
		});

		fireEvent.scroll(log);
		expect(scrollbar).toHaveAttribute("aria-valuenow", "31");
		const markers = Array.from(
			scrollbar.querySelectorAll<HTMLElement>("[data-chat-scroll-marker]"),
		);
		expect(markers.length).toBeGreaterThan(1);
		expect(
			Number.parseFloat(markers[1]!.style.top) - Number.parseFloat(markers[0]!.style.top),
		).toBeLessThanOrEqual(8);

		fireEvent.wheel(scrollbar, { deltaY: 200 });
		expect(log.scrollTop).toBe(1200);
		expect(scrollbar).toHaveAttribute("aria-valuenow", "38");

		fireEvent.keyDown(scrollbar, { key: "End" });
		expect(log.scrollTop).toBe(3200);
		expect(scrollbar).toHaveAttribute("aria-valuenow", "100");
	});

	it("disables the conversation minimap while the inspector is open", () => {
		useUiStore.setState({
			inspectorSessions: { "tallpine-long": { isOpen: true, view: "summary" } },
		});
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(8)} />);
		const log = screen.getByRole("log");
		const scrollbar = screen.getByTestId("chat-conversation-minimap");
		stubGeometry(log, {
			scrollHeight: 4000,
			clientHeight: 800,
			scrollTop: 1000,
		});
		stubGeometry(scrollbar, {
			scrollHeight: 800,
			clientHeight: 800,
			scrollTop: 0,
		});
		fireEvent.scroll(log);

		expect(scrollbar).toHaveAttribute("aria-hidden", "true");
		expect(scrollbar).toHaveClass("pointer-events-none");
		expect(scrollbar.querySelectorAll("[data-chat-scroll-marker]")).toHaveLength(0);

		fireEvent.wheel(scrollbar, { deltaY: 200 });
		expect(log.scrollTop).toBe(1000);
	});

	it("updates the minimap interaction boundary when the inspector toggles", async () => {
		useUiStore.setState({
			inspectorSessions: { "tallpine-long": { isOpen: false, view: "summary" } },
		});
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(8)} />);
		const log = screen.getByRole("log");
		const scrollbar = screen.getByTestId("chat-conversation-minimap");
		stubGeometry(log, { scrollHeight: 4000, clientHeight: 800, scrollTop: 1000 });
		stubGeometry(scrollbar, { scrollHeight: 800, clientHeight: 800, scrollTop: 0 });
		fireEvent.scroll(log);
		expect(scrollbar.querySelectorAll("[data-chat-scroll-marker]").length).toBeGreaterThan(0);

		act(() => useUiStore.getState().setInspectorOpen("tallpine-long", true));
		await waitFor(() => expect(scrollbar).toHaveAttribute("aria-hidden", "true"));
		expect(scrollbar).toHaveAttribute("tabindex", "-1");
		fireEvent.wheel(scrollbar, { deltaY: 200 });
		expect(log.scrollTop).toBe(1000);

		act(() => useUiStore.getState().setInspectorOpen("tallpine-long", false));
		await waitFor(() => expect(scrollbar).toHaveAttribute("aria-hidden", "false"));
		expect(scrollbar).toHaveAttribute("tabindex", "0");
	});

	it("opens large histories at the latest turn without mounting offscreen messages", async () => {
		stubVirtualTimelineGeometry();
		const snapshot = chatFixtureLongHistory(250);
		const messages = snapshot.items.filter((item): item is ConversationMessage => item.kind === "message" && item.role === "user");
		messages[0]!.text = "First historical prompt";
		messages.at(-1)!.text = "Latest historical prompt";
		render(<ChatWorkspace snapshot={snapshot} />);
		expect(screen.getByText("Latest historical prompt")).toBeInTheDocument();
		expect(screen.queryByText("First historical prompt")).not.toBeInTheDocument();
		expect(screen.getByRole("log").querySelectorAll("[data-chat-scroll-anchor]").length).toBeLessThan(20);
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
	});

	it("opens at the bottom after measuring very tall latest turns", async () => {
		stubVirtualTimelineGeometry((index) => index >= 98 ? 24000 : 600);
		const snapshot = chatFixtureLongHistory(100);
		const latest = snapshot.items.find((item) => item.kind === "message" && item.role === "user" && item.turnId === "turn-h99") as ConversationMessage;
		latest.text = "Latest tall historical prompt";
		render(<ChatWorkspace snapshot={snapshot} />);
		const log = screen.getByRole("log");
		await waitFor(() => expect(log.scrollTop).toBeGreaterThanOrEqual(log.scrollHeight - log.clientHeight));
		fireEvent.scroll(log);
		expect(screen.getByText("Latest tall historical prompt")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
	});

	it("scrolls virtual history, keeps all minimap targets, and returns to the latest turn", async () => {
		stubVirtualTimelineGeometry();
		useUiStore.setState({ inspectorSessions: { "tallpine-long": { isOpen: false, view: "summary" } } });
		const snapshot = chatFixtureLongHistory(100);
		const prompts = snapshot.items.filter((item): item is ConversationMessage => item.kind === "message" && item.role === "user");
		prompts[0]!.text = "Oldest virtual prompt";
		prompts[40]!.text = "Minimap target outside the viewport";
		prompts.at(-1)!.text = "Latest virtual prompt";
		render(<ChatWorkspace snapshot={snapshot} />);
		const log = screen.getByRole("log");
		const markers = screen.getByTestId("chat-conversation-minimap").querySelectorAll("[data-chat-scroll-marker]");
		expect(markers).toHaveLength(100);
		fireEvent.pointerDown(markers[40]!, { pointerId: 1 });
		fireEvent.scroll(log);
		await waitFor(() => expect(screen.getByText("Minimap target outside the viewport")).toBeInTheDocument());
		log.scrollTop = 0;
		fireEvent.scroll(log);
		await waitFor(() => expect(screen.getByText("Oldest virtual prompt")).toBeInTheDocument());
		expect(screen.queryByText("Latest virtual prompt")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Jump to latest" }));
		fireEvent.scroll(log);
		await waitFor(() => expect(screen.getByText("Latest virtual prompt")).toBeInTheDocument());
		expect(screen.queryByText("Oldest virtual prompt")).not.toBeInTheDocument();
	});

	it("preserves the reader when loading older turns enables virtualization", async () => {
		stubVirtualTimelineGeometry();
		const full = chatFixtureLongHistory(25);
		const anchor = full.items.find((item) => item.kind === "message" && item.role === "user" && item.turnId === "turn-h13") as ConversationMessage;
		anchor.text = "Reader at virtualization boundary";
		const olderIds = new Set(full.turns.slice(0, 5).map((turn) => turn.id));
		const initial = { ...full, turns: full.turns.slice(5), items: full.items.filter((item) => !olderIds.has(item.turnId!)) };
		const view = render(<ChatWorkspace snapshot={initial} />);
		const log = screen.getByRole("log");
		log.scrollTop = 4944;
		fireEvent.scroll(log);
		view.rerender(<ChatWorkspace snapshot={full} />);
		fireEvent.scroll(log);
		expect(screen.getByText("Reader at virtualization boundary")).toBeInTheDocument();
		expect(log.scrollTop).toBe(8034);
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
	});

	it("preserves the virtual reading anchor when history is prepended and new output arrives", async () => {
		stubVirtualTimelineGeometry();
		const full = chatFixtureLongHistory(110);
		const anchor = full.items.find((item) => item.kind === "message" && item.role === "user" && item.turnId === "turn-h60") as ConversationMessage;
		anchor.text = "Preserved reader anchor";
		const olderIds = new Set(full.turns.slice(0, 10).map((turn) => turn.id));
		const initial = { ...full, turns: full.turns.slice(10), items: full.items.filter((item) => !olderIds.has(item.turnId!)) };
		const view = render(<ChatWorkspace snapshot={initial} />);
		const log = screen.getByRole("log");
		log.scrollTop = 30900;
		fireEvent.scroll(log);
		const readerRow = () => screen.getByText("Preserved reader anchor").closest<HTMLElement>("[data-chat-scroll-anchor]")!;
		await waitFor(() => expect(readerRow()).toBeInTheDocument());
		const top = readerRow().getBoundingClientRect().top;
		view.rerender(<ChatWorkspace snapshot={full} />);
		fireEvent.scroll(log);
		expect(readerRow().getBoundingClientRect().top).toBe(top);
		const scrollTop = log.scrollTop;
		const update = structuredClone(full);
		const latest = update.items.at(-1) as ConversationMessage;
		latest.text += " New provider output";
		latest.streaming = true;
		latest.revision++;
		update.latestSequence++;
		view.rerender(<ChatWorkspace snapshot={update} />);
		expect(log.scrollTop).toBe(scrollTop);
		expect(readerRow().getBoundingClientRect().top).toBe(top);
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
	});

	it("keeps the visible virtual turn anchored when a row above it grows", async () => {
		stubVirtualTimelineGeometry();
		const originalObserver = window.ResizeObserver;
		const observers: Array<{ targets: Set<Element>; callback: ResizeObserverCallback }> = [];
		class TestResizeObserver {
			targets = new Set<Element>();
			constructor(public callback: ResizeObserverCallback) { observers.push(this); }
			observe(target: Element) { this.targets.add(target); }
			unobserve(target: Element) { this.targets.delete(target); }
			disconnect() { this.targets.clear(); }
		}
		window.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
		try {
			const snapshot = chatFixtureLongHistory(100);
			const prompt = snapshot.items.find((item) => item.kind === "message" && item.role === "user" && item.turnId === "turn-h30") as ConversationMessage;
			prompt.text = "Reader below growing output";
			render(<ChatWorkspace snapshot={snapshot} />);
			const log = screen.getByRole("log");
			log.scrollTop = 18540;
			fireEvent.scroll(log);
			const reader = () => screen.getByText("Reader below growing output").closest<HTMLElement>("[data-chat-scroll-anchor]")!;
			const top = reader().getBoundingClientRect().top;
			const above = log.querySelector<HTMLElement>('[data-index="28"]')!;
			expect(above).toBeInTheDocument();
			act(() => {
				const entry = { target: above, borderBoxSize: [{ blockSize: 1200, inlineSize: 768 }] } as unknown as ResizeObserverEntry;
				for (const observer of observers) {
					if (observer.targets.has(above)) observer.callback([entry], observer as unknown as ResizeObserver);
				}
			});
			expect(log.scrollTop).toBe(19140);
			expect(reader().getBoundingClientRect().top).toBe(top);
			await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
		} finally {
			window.ResizeObserver = originalObserver;
		}
	});

	it("does not follow a streaming resize of the latest row while the reader is unpinned", async () => {
		stubVirtualTimelineGeometry((index) => index === 59 ? 120 : 600);
		const originalObserver = window.ResizeObserver;
		const observers: Array<{ targets: Set<Element>; callback: ResizeObserverCallback }> = [];
		class TestResizeObserver {
			targets = new Set<Element>();
			constructor(public callback: ResizeObserverCallback) { observers.push(this); }
			observe(target: Element) { this.targets.add(target); }
			unobserve(target: Element) { this.targets.delete(target); }
			disconnect() { this.targets.clear(); }
		}
		window.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
		try {
			const snapshot = chatFixtureLongHistory(60);
			const latestTurnId = snapshot.turns.at(-1)!.id;
			snapshot.turns.at(-1)!.state = "running";
			snapshot.items = snapshot.items.filter((item) =>
				item.turnId !== latestTurnId || item.kind === "message",
			);
			const latestAssistant = snapshot.items.findLast(
				(item) => item.kind === "message" && item.role === "assistant" && item.turnId === latestTurnId,
			);
			if (latestAssistant?.kind === "message") latestAssistant.streaming = true;
			render(<ChatWorkspace snapshot={snapshot} />);
			const log = screen.getByRole("log");
			log.scrollTop = log.scrollHeight - log.clientHeight - 150;
			fireEvent.scroll(log);
			await screen.findByRole("button", { name: "Jump to latest" });
			expect(Number.parseFloat(screen.getByTestId("chat-prompt-spacer").style.height)).toBeGreaterThan(150);
			const unpinnedScrollTop = log.scrollTop;
			const latest = log.querySelector<HTMLElement>('[data-index="59"]')!;
			expect(latest).toBeInTheDocument();

			let observedResize = false;
			act(() => {
				const entry = { target: latest, borderBoxSize: [{ blockSize: 900, inlineSize: 768 }] } as unknown as ResizeObserverEntry;
				for (const observer of observers) {
					if (observer.targets.has(latest)) {
						observedResize = true;
						observer.callback([entry], observer as unknown as ResizeObserver);
					}
				}
			});

			expect(observedResize).toBe(true);
			expect(log.scrollTop).toBe(unpinnedScrollTop);
			expect(screen.getByRole("button", { name: "Jump to latest" })).toBeInTheDocument();
			await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
		} finally {
			window.ResizeObserver = originalObserver;
		}
	});

	it.each([1200, 300])("preserves the spacer and bottom anchor during a disclosure resize to %ipx", async (height) => {
		stubVirtualTimelineGeometry();
		const originalObserver = window.ResizeObserver;
		const observers: TestResizeObserver[] = [];
		class TestResizeObserver {
			targets = new Set<Element>();
			constructor(public callback: ResizeObserverCallback) { observers.push(this); }
			observe(target: Element) { this.targets.add(target); }
			unobserve(target: Element) { this.targets.delete(target); }
			disconnect() { this.targets.clear(); }
		}
		window.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
		try {
			render(<ChatWorkspace snapshot={chatFixtureLongHistory(100)} />);
			const log = screen.getByRole("log");
			const spacer = screen.getByTestId("chat-prompt-spacer");
			log.scrollTop = log.scrollHeight - log.clientHeight;
			fireEvent.scroll(log);
			const initialSpacer = spacer.style.height;
			const initialScrollTop = log.scrollTop;
			const latest = log.querySelector<HTMLElement>('[data-index="99"]')!;
			expect(latest).toBeInTheDocument();
			// A disclosure changes row geometry without a new conversation snapshot.
			act(() => {
				const entry = { target: latest, borderBoxSize: [{ blockSize: height, inlineSize: 768 }] } as unknown as ResizeObserverEntry;
				for (const observer of observers) {
					if (observer.targets.has(latest)) observer.callback([entry], observer as unknown as ResizeObserver);
				}
			});
			expect(spacer.style.height).toBe(initialSpacer);
			// Keep the virtualizer's size compensation without an extra forced pin.
			expect(log.scrollTop).toBe(initialScrollTop + height - 600);
			await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
		} finally {
			window.ResizeObserver = originalObserver;
		}
	});

	it("does not recommit the conversation timeline when the inspector toggles", async () => {
		useUiStore.setState({
			inspectorSessions: { "tallpine-long": { isOpen: false, view: "summary" } },
		});
		const commits: number[] = [];
		render(
			<Profiler id="chat-workspace" onRender={() => commits.push(1)}>
				<ChatWorkspace snapshot={chatFixtureLongHistory(250)} />
			</Profiler>,
		);
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 0));
		});
		commits.length = 0;

		act(() => useUiStore.getState().setInspectorOpen("tallpine-long", true));

		expect(commits).toHaveLength(0);
	});

	it("keeps conversation minimap markers when the transcript fits the viewport", () => {
		useUiStore.setState({
			inspectorSessions: { "tallpine-long": { isOpen: false, view: "summary" } },
		});
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(4)} />);
		const log = screen.getByRole("log");
		const scrollbar = screen.getByTestId("chat-conversation-minimap");
		// Closing the inspector widens chat; a short history can stop overflowing.
		stubGeometry(log, {
			scrollHeight: 600,
			clientHeight: 600,
			scrollTop: 0,
		});
		stubGeometry(scrollbar, {
			scrollHeight: 600,
			clientHeight: 600,
			scrollTop: 0,
		});
		fireEvent.scroll(log);

		expect(scrollbar).not.toHaveAttribute("aria-hidden", "true");
		expect(scrollbar).not.toHaveClass("pointer-events-none");
		expect(scrollbar.querySelectorAll("[data-chat-scroll-marker]").length).toBeGreaterThan(1);
	});

	it("re-enables the conversation minimap when the inspector closes again", async () => {
		useUiStore.setState({
			inspectorSessions: { "tallpine-long": { isOpen: false, view: "summary" } },
		});
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(8)} />);
		const log = screen.getByRole("log");
		const scrollbar = screen.getByTestId("chat-conversation-minimap");
		stubGeometry(log, {
			scrollHeight: 4000,
			clientHeight: 800,
			scrollTop: 1000,
		});
		stubGeometry(scrollbar, {
			scrollHeight: 800,
			clientHeight: 800,
			scrollTop: 0,
		});
		fireEvent.scroll(log);

		expect(scrollbar.querySelectorAll("[data-chat-scroll-marker]").length).toBeGreaterThan(0);
		fireEvent.wheel(scrollbar, { deltaY: 200 });
		expect(log.scrollTop).toBe(1200);

		act(() => {
			useUiStore.setState({
				inspectorSessions: { "tallpine-long": { isOpen: true, view: "summary" } },
			});
		});
		await waitFor(() => {
			expect(scrollbar).toHaveAttribute("aria-hidden", "true");
		});
		fireEvent.wheel(scrollbar, { deltaY: 200 });
		expect(log.scrollTop).toBe(1200);

		act(() => {
			useUiStore.setState({
				inspectorSessions: { "tallpine-long": { isOpen: false, view: "summary" } },
			});
		});
		await waitFor(() => {
			expect(scrollbar).not.toHaveAttribute("aria-hidden", "true");
			expect(scrollbar.querySelectorAll("[data-chat-scroll-marker]").length).toBeGreaterThan(0);
		});
		fireEvent.wheel(scrollbar, { deltaY: 200 });
		expect(log.scrollTop).toBe(1400);
	});

	it("previews the request and response for a hovered conversation marker", () => {
		useUiStore.setState({
			inspectorSessions: {
				"tallpine-long": { isOpen: false, view: "summary" },
			},
		});
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(4)} />);
		const log = screen.getByRole("log");
		const scrollbar = screen.getByRole("scrollbar", {
			name: "Conversation scrollbar",
		});
		stubGeometry(log, {
			scrollHeight: 2400,
			clientHeight: 600,
			scrollTop: 0,
		});
		stubGeometry(scrollbar, {
			scrollHeight: 600,
			clientHeight: 600,
			scrollTop: 0,
		});
		fireEvent.scroll(log);

		const markers = Array.from(
			scrollbar.querySelectorAll<HTMLElement>("[data-chat-scroll-marker]"),
		);
		expect(markers.length).toBeGreaterThan(2);
		fireEvent.pointerEnter(markers[0]!);

		const preview = screen.getByRole("tooltip");
		expect(preview).toHaveTextContent("Wire the snapshot endpoint into the handler (round 1)");
		expect(preview).toHaveTextContent("Done. conversation now returns the durable snapshot");
		expect(markers[0]!.querySelector(".chat-scroll-marker")).toHaveClass(
			"chat-scroll-marker-active",
		);
		expect(markers[1]!.querySelector(".chat-scroll-marker")).toHaveClass(
			"chat-scroll-marker-adjacent",
		);

		fireEvent.pointerLeave(scrollbar);
		expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
	});

	it("limits conversation minimap navigation to human prompts", () => {
		const snapshot = structuredClone(chatFixtureLongHistory(2));
		snapshot.items.splice(1, 0, {
			kind: "activity",
			id: "loose-status",
			sequence: 1.5,
			revision: 1,
			activityKind: "system",
			status: "completed",
			summary: "Automatic compaction completed",
			createdAt: "2026-08-08T00:00:00Z",
		});
		useUiStore.setState({
			inspectorSessions: {
				[snapshot.sessionId]: { isOpen: false, view: "summary" },
			},
		});
		render(<ChatWorkspace snapshot={snapshot} />);
		const log = screen.getByRole("log");
		const scrollbar = screen.getByRole("scrollbar", {
			name: "Conversation scrollbar",
		});
		stubGeometry(log, {
			scrollHeight: 1800,
			clientHeight: 600,
			scrollTop: 0,
		});
		stubGeometry(scrollbar, {
			scrollHeight: 600,
			clientHeight: 600,
			scrollTop: 0,
		});
		fireEvent.scroll(log);

		const markers = Array.from(
			scrollbar.querySelectorAll<HTMLElement>("[data-chat-scroll-marker]"),
		);
		expect(markers).toHaveLength(2);
		fireEvent.pointerEnter(markers[1]!);
		expect(screen.getByRole("tooltip")).not.toHaveTextContent("Automatic compaction completed");
	});

	it("fades the welcome heading out in place on the first send instead of popping it off", () => {
		vi.useFakeTimers();
		try {
			const snapshot = idleSnapshot(chatFixtureEmpty);
			const view = render(<ChatWorkspace snapshot={snapshot} />);
			expect(screen.getAllByText("What do you want to work on?")).toHaveLength(1);

			const localEchos = [{ clientMessageId: "first", text: "hello", createdAt: "2026-09-09T00:00:00Z" }];
			view.rerender(<ChatWorkspace snapshot={snapshot} localEchos={localEchos} />);

			// The real heading is gone, a hidden copy fades where it stood, and then it is removed.
			expect(screen.queryByRole("heading", { name: "What do you want to work on?" })).not.toBeInTheDocument();
			const leaving = screen.getByText("What do you want to work on?");
			expect(leaving).toHaveAttribute("aria-hidden", "true");
			expect(leaving).toHaveClass("chat-welcome-leaving");

			act(() => {
				vi.advanceTimersByTime(200);
			});
			expect(screen.queryByText("What do you want to work on?")).not.toBeInTheDocument();
		} finally {
			vi.useRealTimers();
		}
	});

	it("centers an empty-chat welcome heading above a realistic starter prompt", () => {
		const random = vi.spyOn(Math, "random").mockReturnValue(0);
		render(<ChatWorkspace snapshot={chatFixtureEmpty} />);
		expect(screen.queryByRole("log")).not.toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "What do you want to work on?" })).toHaveClass("font-normal");
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute(
			"aria-placeholder",
			"Fix a failing test in this project",
		);
		expect(
			screen
				.getByTestId("chat-conversation-panel")
				.querySelector("[data-composer-placement='center']"),
		).not.toBeNull();
		random.mockRestore();
	});

	it("docks the composer once the conversation has content", () => {
		render(<ChatWorkspace snapshot={chatFixture} />);
		expect(
			screen
				.getByTestId("chat-conversation-panel")
				.querySelector("[data-composer-placement='dock']"),
		).not.toBeNull();
	});

	it("keeps a turn as one block, positioned by its first item", () => {
		// The fixture's automation relay carries sequence 8, in the middle of turn-2's
		// items. Reading strictly by sequence would split turn-2 around it; the rule is
		// that a turn takes the position of its first item and stays contiguous.
		render(<ChatWorkspace snapshot={chatFixture} />);
		const log = screen.getByRole("log");
		const text = log.textContent ?? "";
		const question = text.indexOf("Run the backend tests");
		const answer = text.indexOf("Tests are still running");
		const relay = text.indexOf("Checks failed on the base branch");

		expect(question).toBeGreaterThan(-1);
		expect(answer).toBeGreaterThan(question);
		expect(relay).toBeGreaterThan(answer);

		const relayCard = screen.getByText(/Checks failed on the base branch/).closest(".cursor-chat-origin-message");
		expect(relayCard).toHaveClass("border-l-logo-accent/60");
		expect(relayCard?.querySelector("svg")).toHaveClass("text-logo-accent");
	});

	it("offers Jump to latest only once the reader has scrolled away from the bottom", async () => {
		const user = userEvent.setup();
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(8)} />);
		const log = screen.getByRole("log");

		expect(screen.queryByRole("button", { name: /jump to latest/i })).not.toBeInTheDocument();

		stubGeometry(log, {
			scrollHeight: 4000,
			clientHeight: 800,
			scrollTop: 100,
		});
		log.dispatchEvent(new Event("scroll"));
		const jump = await screen.findByRole("button", {
			name: /jump to latest/i,
		});
		expect(jump).toHaveAttribute("title", "Jump to latest");
		expect(jump).not.toHaveTextContent("Jump to latest");
		expect(jump).toHaveClass("rounded-full", "size-12", "bg-raised", "dark:bg-raised");
		expect(jump).not.toHaveClass("dark:bg-transparent");
		expect(jump).not.toHaveClass("dark:hover:bg-input/30");

		// Taking the jump re-arms following, so the control retires itself.
		await user.click(jump);
		expect(log.scrollTop).toBe(4000);
		await waitFor(() =>
			expect(screen.queryByRole("button", { name: /jump to latest/i })).not.toBeInTheDocument(),
		);
	});

	it("does not yank a reader who scrolled up when the next poll arrives", async () => {
		const snapshot = chatFixtureLongHistory(8);
		const { rerender } = render(<ChatWorkspace snapshot={snapshot} />);
		const log = screen.getByRole("log");

		stubGeometry(log, {
			scrollHeight: 4000,
			clientHeight: 800,
			scrollTop: 1200,
		});
		log.dispatchEvent(new Event("scroll"));
		await screen.findByRole("button", { name: /jump to latest/i });

		rerender(<ChatWorkspace snapshot={{ ...poll(snapshot), latestSequence: 999 }} />);
		expect(log.scrollTop).toBe(1200);
	});

	it("follows new output while the reader is already at the bottom", () => {
		const snapshot = chatFixtureLongHistory(8);
		const { rerender } = render(<ChatWorkspace snapshot={snapshot} />);
		const log = screen.getByRole("log");
		stubGeometry(log, {
			scrollHeight: 4000,
			clientHeight: 800,
			scrollTop: 0,
		});

		rerender(<ChatWorkspace snapshot={{ ...poll(snapshot), latestSequence: 999 }} />);
		expect(log.scrollTop).toBe(4000);
	});

	it("keeps a trailing spacer so a dropped prompt can sit near the top", async () => {
		const snapshot = chatFixtureSettled;
		const { rerender } = render(<ChatWorkspace snapshot={snapshot} />);
		const log = screen.getByRole("log");
		const spacer = screen.getByTestId("chat-prompt-spacer");
		const anchors = log.querySelectorAll<HTMLElement>("[data-chat-scroll-anchor]");
		const anchor = anchors[anchors.length - 1];
		expect(anchor).toBeTruthy();

		const contentHeight = 240;
		Object.defineProperty(spacer, "offsetHeight", {
			configurable: true,
			get: () => Number.parseFloat(spacer.style.height || "0") || 0,
		});
		Object.defineProperty(log, "clientHeight", {
			configurable: true,
			value: 800,
		});
		Object.defineProperty(log, "scrollHeight", {
			configurable: true,
			get: () => contentHeight + (Number.parseFloat(spacer.style.height || "0") || 0),
		});
		Object.defineProperty(log, "scrollTop", {
			configurable: true,
			writable: true,
			value: 0,
		});
		log.getBoundingClientRect = () =>
			({
				x: 0,
				y: 0,
				top: 0,
				left: 0,
				bottom: 800,
				right: 480,
				width: 480,
				height: 800,
				toJSON() {
					return this;
				},
			}) as DOMRect;
		anchor!.getBoundingClientRect = () =>
			({
				x: 0,
				y: 48,
				top: 48,
				left: 0,
				bottom: 96,
				right: 480,
				width: 480,
				height: 48,
				toJSON() {
					return this;
				},
			}) as DOMRect;

		await act(async () => {
			rerender(
				<ChatWorkspace
					snapshot={{
						...snapshot,
						latestSequence: snapshot.latestSequence + 1,
					}}
				/>,
			);
		});

		expect(Number.parseFloat(spacer.style.height)).toBe(
			promptSpacerHeight({
				viewportHeight: 800,
				contentHeightWithoutSpacer: contentHeight,
				anchorOffset: 48,
				topInset: promptTopInset(800),
			}),
		);
		expect(log.scrollTop).toBe(log.scrollHeight);
	});

	it("survives a poll without disturbing what the reader opened", async () => {
		const user = userEvent.setup();
		const snapshot = chatFixtureLongHistory(3);
		const { rerender } = render(<ChatWorkspace snapshot={snapshot} />);

		// A run of tool calls is collapsed; opening one is local state that a poll must
		// not reset, which is what remounting the subtree on every refetch would do.
		const run = screen.getAllByRole("button", { expanded: false })[0]!;
		await user.click(run);
		expect(run).toHaveAttribute("aria-expanded", "true");

		rerender(<ChatWorkspace snapshot={poll(snapshot)} />);
		expect(run).toHaveAttribute("aria-expanded", "true");
	});

	it("preserves an expanded activity disclosure after its virtual row unmounts", async () => {
		const user = userEvent.setup();
		stubVirtualTimelineGeometry();
		const snapshot = chatFixtureLongHistory(60);
		const view = render(<ChatWorkspace snapshot={snapshot} />);
		const log = screen.getByRole("log");
		const jumpTo = async (index: number) => {
			log.scrollTop = index * 636;
			fireEvent.scroll(log);
			await waitFor(() => expect(log.querySelector(`[data-index="${index}"]`)).toBeInTheDocument());
		};

		await jumpTo(20);
		const turn = log.querySelector<HTMLElement>('[data-index="20"]')!;
		// The settled turn's first disclosure is its Worked accordion, held above the virtualizer too.
		const disclosure = within(turn).getAllByRole("button", { expanded: false })[0]!;
		await user.click(disclosure);
		expect(disclosure).toHaveAttribute("aria-expanded", "true");

		await jumpTo(50);
		await waitFor(() => expect(log.querySelector('[data-index="20"]')).not.toBeInTheDocument());
		await jumpTo(20);
		const returnedDisclosure = within(log.querySelector<HTMLElement>('[data-index="20"]')!)
			.getAllByRole("button", { expanded: true })[0]!;
		expect(returnedDisclosure).toHaveAttribute("aria-expanded", "true");
		view.unmount();
	});
});
