import { act, render, renderHook, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserHandoffChange, BrowserHandoffState } from "../../../shared/browser-handoff";
import { tallpineBridge } from "../../lib/bridge";
import { useBrowserHandoffReveal } from "../session-view/useBrowserHandoffReveal";
import { BrowserHandoffNotice } from "./BrowserHandoffNotice";

const handoff: BrowserHandoffState = { id: "h1", sessionId: "s1", reason: "Enter the code from your phone", deadline: 0 };

function stubBridge(initial: BrowserHandoffState | null) {
	let emit: (change: BrowserHandoffChange) => void = () => undefined;
	const handoffState = vi.spyOn(tallpineBridge.browser, "handoffState").mockResolvedValue(initial);
	const handoffContinue = vi.spyOn(tallpineBridge.browser, "handoffContinue").mockResolvedValue(true);
	vi.spyOn(tallpineBridge.browser, "onHandoffChanged").mockImplementation((listener) => {
		emit = listener;
		return () => undefined;
	});
	return { handoffState, handoffContinue, emit: (change: BrowserHandoffChange) => act(() => emit(change)) };
}

afterEach(() => vi.restoreAllMocks());

describe("BrowserHandoffNotice", () => {
	it("shows the agent's reason and hands the browser back on Continue", async () => {
		const bridge = stubBridge(handoff);
		render(<BrowserHandoffNotice sessionId="s1" />);
		expect(await screen.findByText("Enter the code from your phone")).toBeInTheDocument();
		expect(screen.getByText("The agent is waiting for you")).toBeInTheDocument();
		expect(screen.getByText(/Do this in the page below/)).toBeInTheDocument();
		expect(bridge.handoffState).toHaveBeenCalledWith("s1");
		await userEvent.click(screen.getByRole("button", { name: "Continue" }));
		expect(bridge.handoffContinue).toHaveBeenCalledWith("s1", "h1");
		bridge.emit({ sessionId: "s1", state: null });
		expect(screen.queryByTestId("browser-handoff-notice")).not.toBeInTheDocument();
	});

	it("a Google sign-in handoff points at the plain Chrome window, not the agent's (B854)", async () => {
		stubBridge({ ...handoff, reason: "Sign in to Google", signIn: true });
		render(<BrowserHandoffNotice sessionId="s1" chrome />);
		expect(await screen.findByText(/Sign in in the Google Chrome window that just opened, without agent control/)).toBeInTheDocument();
		expect(screen.queryByText(/Do this in Google Chrome/)).not.toBeInTheDocument();
	});

	it("follows its own session's handoffs only and names Chrome in Chrome mode", async () => {
		const bridge = stubBridge(null);
		render(<BrowserHandoffNotice sessionId="s1" chrome />);
		bridge.emit({ sessionId: "s2", state: { ...handoff, sessionId: "s2" } });
		expect(screen.queryByTestId("browser-handoff-notice")).not.toBeInTheDocument();
		bridge.emit({ sessionId: "s1", state: handoff });
		expect(screen.getByText(/Do this in Google Chrome/)).toBeInTheDocument();
	});
});

describe("useBrowserHandoffReveal", () => {
	it("opens the panel once per handoff while enabled", async () => {
		const bridge = stubBridge(null);
		const reveal = vi.fn();
		// revealBrowserPanel is a fresh callback whenever panel state changes.
		const { rerender } = renderHook(({ enabled }) => useBrowserHandoffReveal("s1", enabled, () => reveal()), { initialProps: { enabled: true } });
		await act(async () => undefined);
		expect(reveal).not.toHaveBeenCalled();
		bridge.emit({ sessionId: "s1", state: handoff });
		expect(reveal).toHaveBeenCalledTimes(1);
		rerender({ enabled: true });
		expect(reveal).toHaveBeenCalledTimes(1);
		rerender({ enabled: false });
		bridge.emit({ sessionId: "s1", state: { ...handoff, id: "h2" } });
		expect(reveal).toHaveBeenCalledTimes(1);
		rerender({ enabled: true });
		expect(reveal).toHaveBeenCalledTimes(2);
	});
});
