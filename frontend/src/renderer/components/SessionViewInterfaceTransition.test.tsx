import {
	closeShellTerminalMock,
	interfaceTransitionMock,
	interfaceTransitionState,
	settingsState,
	reviewGetMock,
	chatSurfaceWorkState,
	chooseSessionAction,
	shellTerminalsState,
	workerSession,
	testInterfaceTransition,
	unsafeChatLeaveDialog,
	confirmUnsafeChatLeave,
	render,
} from "./SessionView.test.setup";
import { resetSessionViewState } from "./SessionView.test.reset";
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
} from "vitest";
import { SessionView } from "./SessionView";
import { useUiStore } from "../stores/ui-store";
import { setChatDraftBoundary } from "../lib/chat-draft-boundary";
import { chatDraftScopeKey } from "../lib/chat-drafts";
import { useFileAttachments, type FileAttachment } from "../hooks/useFileAttachments";

describe("SessionView", () => {
	beforeEach(() => {
		resetSessionViewState();
	});

	it("preserves attachment work started after the switch confirmation", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const transition = testInterfaceTransition("preserve-after-confirmation", "requested");
		let admitSwitch!: (response: { transition: typeof transition }) => void;
		interfaceTransitionMock.start.mockReturnValueOnce(
			new Promise((resolve) => {
				admitSwitch = resolve;
			}),
		);
		const session = workerSession("sess-1");
		session.mode = "chat";
		act(() => setChatDraftBoundary(session.id, "composer", "persistence-failed"));
		const view = render(<SessionView sessionId={session.id} />);

		await chooseSessionAction("Switch to terminal UI");
		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		await confirmUnsafeChatLeave();
		await waitFor(() => expect(interfaceTransitionMock.start).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(screen.getByTestId("terminal-center")).toBeInTheDocument());
		expect(screen.queryByTestId("chat-surface")).not.toBeInTheDocument();

		let finishStaging!: (attachments: FileAttachment[]) => void;
		const staging = renderHook(() =>
			useFileAttachments({
				initialKey: chatDraftScopeKey({
					sessionId: session.id,
					incarnation: "2026-08-26T09:30:00.000Z",
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
				new File([new Uint8Array(8).fill(1)], "after-confirmation.txt", {
					type: "text/plain",
				}),
			]);
		});
		await waitFor(() => expect(finishStaging).toBeTypeOf("function"));
		await act(async () => admitSwitch({ transition }));

		interfaceTransitionState.status = {
			supported: true,
			targetMode: "tui",
			transition: { ...transition, phase: "completed" },
		};
		view.rerender(<SessionView sessionId={session.id} />);
		expect(staging.result.current.preparing).toBe(true);

		await act(async () => {
			finishStaging([
				{
					id: "after-confirmation",
					mimeType: "text/plain",
					bytes: 8,
					name: "after-confirmation.txt",
					stagedPath: ".tallpine/attachments/after-confirmation.txt",
				},
			]);
			await pending;
		});
		expect(staging.result.current.attachments.map((attachment) => attachment.name)).toEqual([
			"after-confirmation.txt",
		]);
		act(() => setChatDraftBoundary(session.id, "composer", undefined));
	});

	it("unlocks Chat when a pending Chat-to-Terminal request is rejected", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession("sess-1");
		session.mode = "chat";
		let rejectSwitch!: (error: Error) => void;
		interfaceTransitionMock.start.mockReturnValueOnce(
			new Promise((_resolve, reject) => {
				rejectSwitch = reject;
			}),
		);
		render(<SessionView sessionId={session.id} />);

		await chooseSessionAction("Switch to terminal UI");
		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		await waitFor(() => expect(screen.getByTestId("terminal-center")).toBeInTheDocument());
		expect(screen.queryByTestId("chat-surface")).not.toBeInTheDocument();
		await act(async () => rejectSwitch(new Error("switch rejected")));
		expect(interfaceTransitionMock.refreshStatus).toHaveBeenCalledTimes(1);
		await waitFor(() =>
			expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "false"),
		);
	});

	it("keeps Chat locked when a rejected start reconciles to a durable transition", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const transition = testInterfaceTransition("admitted-before-response-loss", "requested");
		interfaceTransitionMock.start.mockRejectedValueOnce(new TypeError("connection closed"));
		interfaceTransitionMock.refreshStatus.mockResolvedValueOnce({
			supported: true,
			targetMode: "tui",
			transition,
		});
		const session = workerSession("sess-1");
		session.mode = "chat";
		const view = render(<SessionView sessionId={session.id} />);

		await chooseSessionAction("Switch to terminal UI");
		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		await waitFor(() => expect(interfaceTransitionMock.refreshStatus).toHaveBeenCalledTimes(1));
		expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "true");

		interfaceTransitionState.status = {
			supported: true,
			targetMode: "tui",
			transition: { ...transition, phase: "failed" },
		};
		view.rerender(<SessionView sessionId={session.id} />);
		await waitFor(() =>
			expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "false"),
		);
	});

	it("does not let a stale reconciliation response clear a transition already observed by CDC", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const transition = testInterfaceTransition("cdc-wins-reconciliation-race", "requested");
		interfaceTransitionMock.start.mockRejectedValueOnce(new TypeError("connection closed"));
		let finishRefresh!: (status: { supported: boolean; targetMode: "tui" }) => void;
		interfaceTransitionMock.refreshStatus.mockReturnValueOnce(
			new Promise((resolve) => {
				finishRefresh = resolve;
			}),
		);
		const session = workerSession("sess-1");
		session.mode = "chat";
		const view = render(<SessionView sessionId={session.id} />);

		await chooseSessionAction("Switch to terminal UI");
		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		await waitFor(() => expect(interfaceTransitionMock.refreshStatus).toHaveBeenCalledTimes(1));
		interfaceTransitionState.status = { supported: true, targetMode: "tui", transition };
		await act(async () => {
			view.rerender(<SessionView sessionId={session.id} />);
			await Promise.resolve();
		});

		await act(async () => finishRefresh({ supported: true, targetMode: "tui" }));
		expect(screen.queryByTestId("chat-surface")).not.toBeInTheDocument();
		expect(screen.getByTestId("terminal-center")).toBeInTheDocument();

		interfaceTransitionState.status = {
			supported: true,
			targetMode: "tui",
			transition: { ...transition, phase: "failed" },
		};
		view.rerender(<SessionView sessionId={session.id} />);
		await waitFor(() =>
			expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "false"),
		);
	});

	it("retains confirmed attachment capture until CDC resolves an unreadable start outcome", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const transition = testInterfaceTransition("admitted-before-status-outage", "requested");
		interfaceTransitionMock.start.mockRejectedValueOnce(new TypeError("connection closed"));
		interfaceTransitionMock.refreshStatus.mockRejectedValueOnce(new TypeError("daemon unavailable"));
		const session = workerSession("sess-1");
		session.mode = "chat";
		let finishStaging!: (attachments: FileAttachment[]) => void;
		const staging = renderHook(() =>
			useFileAttachments({
				initialKey: chatDraftScopeKey({
					sessionId: session.id,
					incarnation: "2026-08-26T10:30:00.000Z",
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
				new File([new Uint8Array(8).fill(1)], "captured-through-outage.txt", {
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
		await waitFor(() => expect(interfaceTransitionMock.refreshStatus).toHaveBeenCalledTimes(1));
		expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "true");
		expect(staging.result.current.preparing).toBe(true);

		interfaceTransitionState.status = {
			supported: true,
			targetMode: "tui",
			transition,
		};
		view.rerender(<SessionView sessionId={session.id} />);
		expect(screen.queryByTestId("chat-surface")).not.toBeInTheDocument();
		expect(screen.getByTestId("terminal-center")).toBeInTheDocument();

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
					id: "discarded-after-outage",
					mimeType: "text/plain",
					bytes: 8,
					name: "captured-through-outage.txt",
					stagedPath: ".tallpine/attachments/captured-through-outage.txt",
				},
			]);
			await pending;
		});
		expect(staging.result.current.attachments).toEqual([]);
		act(() => setChatDraftBoundary(session.id, "composer", undefined));
	});

	it("preserves pending attachments when starting the confirmed interface switch rejects", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		interfaceTransitionMock.start.mockRejectedValueOnce(new Error("switch failed"));
		const session = workerSession("sess-1");
		session.mode = "chat";
		let finishStaging!: (attachments: FileAttachment[]) => void;
		const staging = renderHook(() =>
			useFileAttachments({
				initialKey: chatDraftScopeKey({
					sessionId: session.id,
					incarnation: "2026-08-26T10:00:00.000Z",
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
				new File([new Uint8Array(8).fill(1)], "preserved-after-rejection.txt", {
					type: "text/plain",
				}),
			]);
		});
		await waitFor(() => expect(finishStaging).toBeTypeOf("function"));
		act(() => setChatDraftBoundary(session.id, "composer", "pending-attachments"));
		render(<SessionView sessionId={session.id} />);

		await chooseSessionAction("Switch to terminal UI");
		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		await confirmUnsafeChatLeave();
		await waitFor(() => expect(interfaceTransitionMock.start).toHaveBeenCalledTimes(1));
		expect(staging.result.current.preparing).toBe(true);

		await act(async () => {
			finishStaging([
				{
					id: "preserved-after-rejection",
					mimeType: "text/plain",
					bytes: 8,
					name: "preserved-after-rejection.txt",
					stagedPath: ".tallpine/attachments/preserved-after-rejection.txt",
				},
			]);
			await pending;
		});
		expect(staging.result.current.attachments).toHaveLength(1);
		act(() => setChatDraftBoundary(session.id, "composer", undefined));
	});

	it("preserves pending attachments when an admitted switch later fails", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const transition = testInterfaceTransition("preserve-after-late-failure", "requested");
		interfaceTransitionMock.start.mockResolvedValueOnce({ transition });
		const session = workerSession("sess-1");
		session.mode = "chat";
		let finishStaging!: (attachments: FileAttachment[]) => void;
		const staging = renderHook(() =>
			useFileAttachments({
				initialKey: chatDraftScopeKey({
					sessionId: session.id,
					incarnation: "2026-08-26T11:00:00.000Z",
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
				new File([new Uint8Array(8).fill(1)], "preserved-after-failure.txt", {
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
		await waitFor(() => expect(interfaceTransitionMock.start).toHaveBeenCalledTimes(1));
		session.mode = "tui";
		interfaceTransitionState.status = {
			supported: true,
			targetMode: "chat",
			transition: { ...transition, phase: "failed" },
		};
		view.rerender(<SessionView sessionId={session.id} />);
		expect(staging.result.current.preparing).toBe(true);

		await act(async () => {
			finishStaging([
				{
					id: "preserved-after-failure",
					mimeType: "text/plain",
					bytes: 8,
					name: "preserved-after-failure.txt",
					stagedPath: ".tallpine/attachments/preserved-after-failure.txt",
				},
			]);
			await pending;
		});
		expect(staging.result.current.attachments).toHaveLength(1);
		act(() => setChatDraftBoundary(session.id, "composer", undefined));
	});

	it("cancels hidden attachment work after a confirmed mixed-risk leave without resurrection", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const transition = testInterfaceTransition("mixed-risk-complete", "requested");
		interfaceTransitionMock.start.mockResolvedValueOnce({ transition });
		const session = workerSession("sess-1");
		session.mode = "chat";
		const draftKey = chatDraftScopeKey({
			sessionId: session.id,
			incarnation: "2026-08-27T09:00:00.000Z",
		});
		let finishStaging!: (attachments: FileAttachment[]) => void;
		const staging = renderHook(() =>
			useFileAttachments({
				initialKey: draftKey,
				prepareAttachments: () =>
					new Promise<FileAttachment[]>((resolve) => {
						finishStaging = resolve;
					}),
			}),
		);
		let pending!: Promise<unknown>;
		act(() => {
			pending = staging.result.current.addFiles([
				new File([new Uint8Array(8).fill(1)], "mixed-late.txt", {
					type: "text/plain",
				}),
			]);
		});
		await waitFor(() => expect(finishStaging).toBeTypeOf("function"));
		act(() => {
			// Inline edit registers first, so the legacy single-kind snapshot hides
			// the composer's pending attachment work behind a failed draft save.
			setChatDraftBoundary(session.id, "inline-edit", "persistence-failed");
			setChatDraftBoundary(session.id, "composer", "pending-attachments");
		});
		const view = render(<SessionView sessionId={session.id} />);

		await chooseSessionAction("Switch to terminal UI");
		await userEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		const dialog = await unsafeChatLeaveDialog();
		const warning = dialog.textContent ?? "";
		expect(warning).toContain("could not be saved locally");
		expect(warning).toContain("Attachments are still being saved");
		fireEvent.click(within(dialog).getByRole("button", { name: "Leave chat" }));
		await waitFor(() => expect(interfaceTransitionMock.start).toHaveBeenCalledTimes(1));
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
			targetMode: "tui",
			policy: "interrupt",
			historyPolicy: "strict",
		});
		expect(staging.result.current.preparing).toBe(true);
		interfaceTransitionState.status = {
			supported: true,
			targetMode: "tui",
			transition: { ...transition, phase: "completed" },
		};
		view.rerender(<SessionView sessionId={session.id} />);
		await waitFor(() => expect(staging.result.current.preparing).toBe(false));

		view.unmount();
		staging.unmount();
		await act(async () => {
			finishStaging([
				{
					id: "mixed-discarded-late",
					mimeType: "text/plain",
					bytes: 8,
					name: "mixed-late.txt",
					stagedPath: ".tallpine/attachments/mixed-late.txt",
				},
			]);
			await pending;
		});
		const replacement = renderHook(() => useFileAttachments({ initialKey: draftKey }));
		expect(replacement.result.current.attachments).toEqual([]);
		expect(replacement.result.current.preparing).toBe(false);

		replacement.unmount();
		act(() => {
			setChatDraftBoundary(session.id, "inline-edit", undefined);
			setChatDraftBoundary(session.id, "composer", undefined);
		});
	});

	it.each([
		["a busy controller", { controllerBusy: true, hasRunningTurn: false, queuedTurnCount: 0 }],
		["a running turn", { controllerBusy: false, hasRunningTurn: true, queuedTurnCount: 0 }],
		["accepted queued work", { controllerBusy: false, hasRunningTurn: false, queuedTurnCount: 1 }],
	] as const)("asks for policy when Chat reports %s but the session projection is idle", async (_label, work) => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "idle";
		session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };
		Object.assign(chatSurfaceWorkState, work);

		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "report chat work" }));
		await chooseSessionAction("Switch to terminal UI");

		expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();
	});

	it("does not apply one Chat session's reported work to another selected session", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		for (const sessionId of ["sess-1", "sess-2"]) {
			const session = workerSession(sessionId);
			session.mode = "chat";
			session.status = "idle";
			session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };
		}
		chatSurfaceWorkState.queuedTurnCount = 1;

		const view = render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "report chat work" }));
		view.rerender(<SessionView sessionId="sess-2" />);
		chatSurfaceWorkState.queuedTurnCount = 0;
		fireEvent.click(screen.getByRole("button", { name: "report chat work" }));
		await chooseSessionAction("Switch to terminal UI");

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({ targetMode: "tui", policy: "drain", historyPolicy: "strict" });
	});

	it.each([
		["Finish work, then switch", "drain"],
		["Stop now and switch", "interrupt"],
	] as const)("maps explicit busy Chat consent through the %s action", async (buttonName, policy) => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "working";
		session.activity = { state: "active", lastActivityAt: "2026-08-06T00:00:00Z" };

		render(<SessionView sessionId="sess-1" />);

		await chooseSessionAction("Switch to terminal UI");
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: new RegExp(`^${buttonName}`) }));

		expect(interfaceTransitionMock.start).toHaveBeenCalledOnce();
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({ targetMode: "tui", policy, historyPolicy: "strict" });
	});

	it("disables Chat input while an accepted Chat-to-Terminal drain starts, runs, or settles", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "working";
		session.activity = { state: "active", lastActivityAt: "2026-08-06T00:00:00Z" };

		const view = render(<SessionView sessionId="sess-1" />);
		const chatSurface = () => screen.getByTestId("chat-surface");
		expect(chatSurface()).toHaveAttribute("data-new-work-disabled", "false");

		await chooseSessionAction("Switch to terminal UI");
		fireEvent.click(screen.getByRole("button", { name: /^Finish work, then switch/ }));
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({ targetMode: "tui", policy: "drain", historyPolicy: "strict" });

		interfaceTransitionState.starting = true;
		view.rerender(<SessionView sessionId="sess-1" />);
		expect(chatSurface()).toHaveAttribute("data-new-work-disabled", "true");

		interfaceTransitionState.starting = false;
		interfaceTransitionState.status = {
			supported: true,
			targetMode: "tui",
			transition: {
				id: "switch-chat-to-tui",
				sessionId: "sess-1",
				sourceMode: "chat",
				targetMode: "tui",
				policy: "drain",
				historyPolicy: "strict",
				phase: "draining",
				createdAt: "2026-08-06T00:00:00Z",
				updatedAt: "2026-08-06T00:00:01Z",
			},
		};
		view.rerender(<SessionView sessionId="sess-1" />);
		expect(chatSurface()).toHaveAttribute("data-new-work-disabled", "true");

		interfaceTransitionState.status.transition!.phase = "completed";
		interfaceTransitionState.settling = true;
		view.rerender(<SessionView sessionId="sess-1" />);
		expect(screen.queryByTestId("chat-surface")).not.toBeInTheDocument();
		expect(screen.getByTestId("terminal-center")).toBeInTheDocument();

		interfaceTransitionState.settling = false;
		view.rerender(<SessionView sessionId="sess-1" />);
		expect(chatSurface()).toHaveAttribute("data-new-work-disabled", "false");
	});

	it("shows the chat surface on the click itself for an idle terminal session, before the daemon answers", async () => {
		const session = workerSession("sess-1");
		session.mode = "tui";
		session.status = "idle";
		session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };
		interfaceTransitionState.status = { supported: true, targetMode: "chat" };
		interfaceTransitionMock.start.mockReturnValueOnce(new Promise(() => {}));
		render(<SessionView sessionId="sess-1" />);
		expect(screen.queryByTestId("chat-surface")).not.toBeInTheDocument();

		await chooseSessionAction("Switch to chat UI");

		expect(interfaceTransitionMock.start).toHaveBeenCalledWith(expect.objectContaining({ targetMode: "chat", policy: "drain" }));
		expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "true");
		expect(screen.queryByTestId("terminal-center")).not.toBeInTheDocument();
	});

	it("shows the chat surface the moment a terminal-to-chat switch is requested", () => {
		const session = workerSession("sess-1");
		session.mode = "tui";
		interfaceTransitionState.status = { supported: true, targetMode: "chat" };
		interfaceTransitionState.starting = true;
		interfaceTransitionState.startingPolicy = "interrupt";
		interfaceTransitionState.startingTarget = "chat";
		render(<SessionView sessionId="sess-1" />);
		expect(screen.getByTestId("chat-surface")).toHaveAttribute("data-transitioning", "true");
		expect(screen.queryByTestId("terminal-center")).not.toBeInTheDocument();
	});

	it("shows the terminal the moment a chat-to-terminal switch is requested", () => {
		const session = workerSession("sess-1");
		session.mode = "chat";
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		interfaceTransitionState.starting = true;
		interfaceTransitionState.startingPolicy = "interrupt";
		interfaceTransitionState.startingTarget = "tui";
		render(<SessionView sessionId="sess-1" />);
		expect(screen.getByTestId("terminal-center")).toBeInTheDocument();
		expect(screen.queryByTestId("chat-surface")).not.toBeInTheDocument();
	});

	it("keeps the current surface while a switch waits for the running turn to finish", () => {
		const session = workerSession("sess-1");
		session.mode = "chat";
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		interfaceTransitionState.starting = true;
		interfaceTransitionState.startingPolicy = "drain";
		interfaceTransitionState.startingTarget = "tui";
		render(<SessionView sessionId="sess-1" />);
		expect(screen.getByTestId("chat-surface")).toBeInTheDocument();
		expect(screen.queryByTestId("terminal-center")).not.toBeInTheDocument();
	});

	it("discards one session's switch consent dialog when navigating to another session", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		for (const sessionId of ["sess-1", "sess-2"]) {
			const session = workerSession(sessionId);
			session.mode = "chat";
			session.status = "working";
			session.activity = { state: "active", lastActivityAt: "2026-08-06T00:00:00Z" };
		}
		const view = render(<SessionView sessionId="sess-1" />);

		await chooseSessionAction("Switch to terminal UI");
		expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();

		view.rerender(<SessionView sessionId="sess-2" />);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();

		view.rerender(<SessionView sessionId="sess-1" />);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("does not let a completed switch close another session's newer consent dialog", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		for (const sessionId of ["sess-1", "sess-2"]) {
			const session = workerSession(sessionId);
			session.mode = "chat";
			session.status = "working";
			session.activity = { state: "active", lastActivityAt: "2026-08-06T00:00:00Z" };
		}
		let finishFirstSwitch!: () => void;
		interfaceTransitionMock.start.mockImplementationOnce(
			() => new Promise<void>((resolve) => {
				finishFirstSwitch = resolve;
			}),
		);
		const view = render(<SessionView sessionId="sess-1" />);

		await chooseSessionAction("Switch to terminal UI");
		fireEvent.click(screen.getByRole("button", { name: /^Stop now and switch/ }));
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
			targetMode: "tui",
			policy: "interrupt",
			historyPolicy: "strict",
		});

		view.rerender(<SessionView sessionId="sess-2" />);
		await chooseSessionAction("Switch to terminal UI");
		expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();

		await act(async () => finishFirstSwitch());

		expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();
	});

	it("discards switch consent when the requested target changes", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		const session = workerSession("sess-1");
		session.mode = "chat";
		session.status = "working";
		session.activity = { state: "active", lastActivityAt: "2026-08-06T00:00:00Z" };
		const view = render(<SessionView sessionId="sess-1" />);

		await chooseSessionAction("Switch to terminal UI");
		expect(screen.getByRole("dialog", { name: "Switch to Terminal UI?" })).toBeInTheDocument();

		interfaceTransitionState.status = { supported: true, targetMode: "chat" };
		view.rerender(<SessionView sessionId="sess-1" />);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();

		interfaceTransitionState.status = { supported: true, targetMode: "tui" };
		view.rerender(<SessionView sessionId="sess-1" />);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("checks only the selected session when deciding whether to show the policy dialog", async () => {
		interfaceTransitionState.status = { supported: true, targetMode: "chat" };
		const selected = workerSession("sess-1");
		selected.status = "idle";
		selected.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };
		const other = workerSession("sess-2");
		other.status = "working";
		other.activity = { state: "active", lastActivityAt: "2026-08-06T00:00:00Z" };

		render(<SessionView sessionId="sess-1" />);

		await chooseSessionAction("Switch to chat UI");

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({ targetMode: "chat", policy: "drain", historyPolicy: "strict" });
	});

	it("does not resurrect an acknowledged recovery notice after the session view remounts", async () => {
		const transition = {
			id: "transition-recovered",
			sessionId: "sess-1",
			sourceMode: "chat" as const,
			targetMode: "tui" as const,
			policy: "drain" as const,
			historyPolicy: "strict" as const,
			phase: "recovery_required" as const,
			errorCode: "DAEMON_RESTARTED",
			errorDetail: "Tallpine recovered the session in its last committed mode.",
			createdAt: "2026-08-12T10:00:00Z",
			updatedAt: "2026-08-12T10:01:00Z",
			completedAt: "2026-08-12T10:01:00Z",
		};
		interfaceTransitionState.status = { supported: true, targetMode: "chat", transition };
		interfaceTransitionMock.acknowledgeNotice.mockImplementation(async () => {
			Object.assign(transition, { noticeAcknowledgedAt: "2026-08-13T08:00:00Z" });
		});

		const view = render(<SessionView sessionId="sess-1" />);
		expect(screen.getByText(transition.errorDetail)).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Dismiss interface switch message" }));
		await waitFor(() =>
			expect(interfaceTransitionMock.acknowledgeNotice).toHaveBeenCalledWith("transition-recovered"),
		);

		view.unmount();
		render(<SessionView sessionId="sess-1" />);
		expect(screen.queryByText(transition.errorDetail)).not.toBeInTheDocument();
	});

	it.each([
		["Retry switch to Chat UI", "TARGET_HISTORY_UNSETTLED", "strict"],
		["Use provider history and switch", "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH", "provider_history"],
	] as const)("requires fresh busy-work consent for %s", (action, errorCode, historyPolicy) => {
		const transition = {
			id: "transition-history-unsettled",
			sessionId: "sess-1",
			sourceMode: "tui" as const,
			targetMode: "chat" as const,
			policy: "interrupt" as const,
			historyPolicy: "strict" as const,
			phase: "failed" as const,
			errorCode,
			errorDetail: "Interface switch failed (Tallpine-2L): target history is not settled.",
			createdAt: "2026-08-25T09:00:00Z",
			updatedAt: "2026-08-25T09:00:01Z",
			completedAt: "2026-08-25T09:00:01Z",
		};
		interfaceTransitionState.status = { supported: true, targetMode: "chat", transition };

		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: action }));
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();
		const dialog = screen.getByRole("dialog", { name: "Switch to Chat UI?" });
		fireEvent.click(within(dialog).getByRole("button", { name: /^Finish work, then switch/ }));

		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
			targetMode: "chat",
			policy: "drain",
			historyPolicy,
		});
	});

	it("does not carry provider-history consent to another session after navigation", async () => {
		interfaceTransitionState.status = {
			supported: true,
			targetMode: "chat",
			transition: {
				id: "legacy-session-one",
				sessionId: "sess-1",
				sourceMode: "tui",
				targetMode: "chat",
				policy: "interrupt",
				historyPolicy: "strict",
				phase: "failed",
				errorCode: "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH",
				createdAt: "2026-09-12T00:00:00Z",
				updatedAt: "2026-09-12T00:00:00Z",
			},
		};
		const view = render(<SessionView sessionId="sess-1" />);
		await userEvent.click(screen.getByRole("button", { name: "Use provider history and switch" }));
		expect(screen.getByRole("dialog", { name: "Switch to Chat UI?" })).toBeInTheDocument();
		expect(interfaceTransitionMock.start).not.toHaveBeenCalled();

		interfaceTransitionState.status = { supported: true, targetMode: "chat" };
		view.rerender(<SessionView sessionId="sess-2" />);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		await chooseSessionAction("Switch to chat UI");
		await userEvent.click(screen.getByRole("button", { name: /^Finish work, then switch/ }));
		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
			targetMode: "chat",
			policy: "drain",
			historyPolicy: "strict",
		});
	});

	it("uses explicit provider-history recovery with the safe drain default", () => {
		const session = workerSession("sess-1");
		session.status = "idle";
		delete session.activity;
		const transition = {
			id: "transition-legacy-history",
			sessionId: "sess-1",
			sourceMode: "tui" as const,
			targetMode: "chat" as const,
			policy: "interrupt" as const,
			historyPolicy: "strict" as const,
			phase: "failed" as const,
			errorCode: "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH",
			errorDetail: "Interface switch failed (Tallpine-2L): legacy text mismatch.",
			createdAt: "2026-08-25T09:00:00Z",
			updatedAt: "2026-08-25T09:00:01Z",
			completedAt: "2026-08-25T09:00:01Z",
		};
		interfaceTransitionState.status = { supported: true, targetMode: "chat", transition };

		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "Use provider history and switch" }));

		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
			targetMode: "chat",
			policy: "drain",
			historyPolicy: "provider_history",
		});
	});

	it("retries persisted provider-history consent after daemon recovery", () => {
		const session = workerSession("sess-1");
		session.status = "idle";
		delete session.activity;
		interfaceTransitionState.status = {
			supported: true,
			targetMode: "chat",
			transition: {
				id: "transition-provider-restart",
				sessionId: "sess-1",
				sourceMode: "tui",
				targetMode: "chat",
				policy: "drain",
				historyPolicy: "provider_history",
				phase: "recovery_required",
				errorCode: "DAEMON_RESTARTED",
				errorDetail: "Tallpine restored Terminal after the daemon restarted.",
				createdAt: "2026-08-25T09:00:00Z",
				updatedAt: "2026-08-25T09:00:01Z",
				completedAt: "2026-08-25T09:00:01Z",
			},
		};

		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "Use provider history and switch" }));

		expect(interfaceTransitionMock.start).toHaveBeenCalledWith({
			targetMode: "chat",
			policy: "drain",
			historyPolicy: "provider_history",
		});
	});

	it("announces a rejected recovery attempt in the persistent Tallpine-2L notice", () => {
		interfaceTransitionState.startError = "Provider history recovery is no longer available.";
		interfaceTransitionState.status = {
			supported: true,
			targetMode: "chat",
			transition: {
				id: "transition-legacy-history",
				sessionId: "sess-1",
				sourceMode: "tui",
				targetMode: "chat",
				policy: "drain",
				historyPolicy: "strict",
				phase: "failed",
				errorCode: "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH",
				errorDetail: "Interface switch failed (Tallpine-2L).",
				createdAt: "2026-08-25T09:00:00Z",
				updatedAt: "2026-08-25T09:00:01Z",
			},
		};

		render(<SessionView sessionId="sess-1" />);
		const [announcement] = screen.getAllByRole("alert");
		expect(screen.getAllByRole("alert")).toHaveLength(1);
		expect(announcement).toHaveTextContent("Interface switch failed (Tallpine-2L).");
		expect(announcement).toHaveTextContent(
			"Recovery attempt failed: Provider history recovery is no longer available.",
		);
	});

	it.each([undefined, "A newer start request was refused."])(
		"shows unconfirmed target shutdown through SessionView while keeping Terminal input fenced (%s)",
		(startError) => {
			workerSession("sess-1").mode = "tui";
			interfaceTransitionState.startError = startError;
			const errorDetail =
				"Tallpine could not confirm the target controller stopped. Restart Tallpine to retry shutdown before restoring the original interface. target still running";
			interfaceTransitionState.status = {
				supported: true,
				targetMode: "chat",
				transition: {
					id: "transition-target-stop-unconfirmed",
					sessionId: "sess-1",
					sourceMode: "tui",
					targetMode: "chat",
					policy: "drain",
					historyPolicy: "strict",
					phase: "target_starting",
					errorCode: "TARGET_STOP_UNCONFIRMED",
					errorDetail,
					createdAt: "2026-08-23T17:00:00Z",
					updatedAt: "2026-08-23T17:01:00Z",
				},
			};

			render(<SessionView sessionId="sess-1" />);

			const alert = screen.getByRole("status", { name: /^Interface switch needs attention/ });
			expect(alert).toHaveAttribute("aria-label", expect.stringContaining("Interface switch needs attention"));
			expect(alert).toHaveAttribute("aria-label", expect.stringContaining(errorDetail));
			expect(alert).toHaveAttribute("aria-live", "polite");
			expect(within(alert).queryByRole("button")).not.toBeInTheDocument();
			expect(screen.getByTestId("terminal-center")).toHaveAttribute("data-agent-input-disabled", "true");
			expect(screen.getByRole("status", { name: /^Interface switch needs attention/ }).querySelector(".animate-spin")).toBeNull();
			expect(screen.queryByRole("button", { name: "Cancel switch to Chat UI" })).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Retry switch to Chat UI" })).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Stay in Terminal" })).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Use provider history and switch" })).not.toBeInTheDocument();
			expect(interfaceTransitionMock.start).not.toHaveBeenCalled();
			expect(interfaceTransitionMock.acknowledgeNotice).not.toHaveBeenCalled();
		},
	);

	it("returns to the source terminal while a failed Chat switch mode refetch settles", () => {
		const session = workerSession("sess-1");
		// The workspace cache observed the transition's intermediate mode commit,
		// but the durable transition already says the target failed and rolled back.
		session.mode = "chat";
		const transition = {
			id: "transition-failed",
			sessionId: "sess-1",
			sourceMode: "tui" as const,
			targetMode: "chat" as const,
			policy: "drain" as const,
			historyPolicy: "strict" as const,
			phase: "failed" as const,
			errorCode: "TARGET_HISTORY_UNSETTLED",
			errorDetail: "The native conversation history did not settle.",
			createdAt: "2026-08-23T17:00:00Z",
			updatedAt: "2026-08-23T17:01:00Z",
			completedAt: "2026-08-23T17:01:00Z",
		};
		interfaceTransitionState.status = { supported: true, targetMode: "chat", transition };

		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByText("terminal center")).toBeInTheDocument();
		expect(screen.queryByTestId("chat-surface")).not.toBeInTheDocument();
		expect(screen.getByText(transition.errorDetail)).toBeInTheDocument();
	});

	it.each([
		["worker", "sess-1"],
		["orchestrator", "sess-orch"],
	] as const)("offers no interface switch for %s sessions when Chat UI is unsupported", (_label, sessionId) => {
		interfaceTransitionState.status = { supported: false, targetMode: "chat", reasonCode: "CHAT_UNSUPPORTED" };
		const session = workerSession(sessionId);
		session.mode = "tui";
		session.status = "idle";
		session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };

		render(<SessionView sessionId={sessionId} />);

		expect(screen.queryByRole("button", { name: "Switch to chat UI" })).not.toBeInTheDocument();
	});

	it.each([
		["before its status loads", undefined, false],
		["once terminated", { supported: false, targetMode: "chat", reasonCode: "SESSION_TERMINATED" }, true],
	] as const)("offers no interface switch for a harness outside the Chat list %s", (_label, status, terminated) => {
		settingsState.chatHarnesses = ["claude-code", "codex"];
		interfaceTransitionState.status = status;
		const session = workerSession("sess-1");
		session.provider = "kiro";
		session.mode = "tui";
		if (terminated) session.isTerminated = true;

		render(<SessionView sessionId="sess-1" />);

		expect(screen.queryByRole("button", { name: "Switch to chat UI" })).not.toBeInTheDocument();
	});

	it("offers the interface switch for a harness in the Chat list", async () => {
		settingsState.chatHarnesses = ["claude-code", "codex", "kiro"];
		interfaceTransitionState.status = { supported: true, targetMode: "chat" };
		const session = workerSession("sess-1");
		session.provider = "kiro";
		session.mode = "tui";

		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByRole("button", { name: "Switch to chat UI" })).toBeInTheDocument();
	});

	it("shows the switch button when the adapter only reports a generic unsupported reason", async () => {
		interfaceTransitionState.status = { supported: false, targetMode: "chat", reasonCode: "INTERFACE_HANDOFF_UNSUPPORTED" };
		const session = workerSession("sess-1");
		session.mode = "tui";
		session.status = "idle";
		session.activity = { state: "idle", lastActivityAt: "2026-08-06T00:00:00Z" };

		render(<SessionView sessionId="sess-1" />);

		// An unsupported switch stays visible but disabled, with the reason as its tooltip.
		expect(screen.getByRole("button", { name: "Switch to chat UI" })).toBeDisabled();
	});

	it("walks backward through auxiliary terminals before returning to the permanent terminal", () => {
		shellTerminalsState.data = [
			{
				handleId: "sh-a",
				sessionId: "sess-1",
				title: "first shell",
				workingDir: "/p",
				createdAt: "2026-07-24T00:00:00Z",
			},
			{
				handleId: "sh-b",
				sessionId: "sess-1",
				title: "second shell",
				workingDir: "/p",
				createdAt: "2026-07-24T00:01:00Z",
			},
		];
		const view = render(<SessionView sessionId="sess-1" />);

		fireEvent.click(screen.getByRole("button", { name: "select second shell" }));
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("sh-b");

		fireEvent.click(screen.getByRole("button", { name: "close second shell" }));
		expect(closeShellTerminalMock).toHaveBeenCalledWith("sh-b", expect.any(Object));
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("sh-a");
		expect(useUiStore.getState().activeShellTerminalHandleId).toBe("sh-a");

		shellTerminalsState.data = shellTerminalsState.data.filter((shell) => shell.handleId !== "sh-b");
		view.rerender(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "close first shell" }));
		expect(closeShellTerminalMock).toHaveBeenCalledWith("sh-a", expect.any(Object));
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("worker");
		expect(useUiStore.getState().activeShellTerminalHandleId).toBeNull();
	});

	it("uses the stored reviewer harness for the reviewer tab icon when no latest run is current", async () => {
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
			data: { reviewerHandleId: "review-sess-1", reviewerHarness: "codex", reviews: [], runs: [] },
			error: undefined,
		});

		render(<SessionView sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByTestId("reviewer-harness")).toHaveTextContent("codex"));
	});

	it("keeps the reviewer terminal reachable from a Chat session", async () => {
		const worker = workerSession("sess-1");
		worker.mode = "chat";
		worker.prs = [{
			url: "https://github.com/acme/repo/pull/7",
			number: 7,
			state: "open",
			ci: "passing",
			review: "none",
			mergeability: "mergeable",
			reviewComments: false,
			updatedAt: "2026-06-15T00:00:00Z",
		}];
		reviewGetMock.mockResolvedValueOnce({
			data: { reviewerHandleId: "review-sess-1", reviewerHarness: "codex", reviews: [], runs: [] },
			error: undefined,
		});

		render(<SessionView sessionId="sess-1" />);
		await screen.findByRole("button", { name: "Reviewer" });
		fireEvent.click(screen.getByRole("button", { name: "Reviewer" }));
		// The chat surface stays mounted; the reviewer pane renders inside it.
		expect(screen.getByTestId("chat-surface")).toBeInTheDocument();
		expect(screen.getByTestId("terminal-target")).toHaveTextContent("reviewer");
		// Selecting the chat tab returns to the chat timeline.
		fireEvent.click(screen.getByRole("button", { name: "select chat tab" }));
		expect(screen.queryByTestId("terminal-target")).not.toBeInTheDocument();
		expect(screen.getByTestId("chat-surface")).toBeInTheDocument();
	});

	it("returns to worker Chat after review feedback is sent", () => {
		const worker = workerSession("sess-1");
		worker.mode = "chat";
		worker.prs = [{
			url: "https://github.com/acme/repo/pull/7",
			number: 7,
			state: "open",
			ci: "passing",
			review: "none",
			mergeability: "mergeable",
			reviewComments: false,
			updatedAt: "2026-06-15T00:00:00Z",
		}];

		render(<SessionView sessionId="sess-1" />);
		fireEvent.click(screen.getByRole("button", { name: "open reviewer chat" }));
		expect(screen.getByTestId("reviewer-chat-surface")).toHaveTextContent("review-1");

		fireEvent.click(screen.getByRole("button", { name: "send review feedback" }));
		expect(screen.queryByTestId("reviewer-chat-surface")).not.toBeInTheDocument();
		expect(screen.getByTestId("chat-surface")).toBeInTheDocument();
	});
});
