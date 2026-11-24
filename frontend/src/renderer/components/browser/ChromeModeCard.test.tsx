import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ChromeSessionState } from "../../../shared/chrome-mode";
import { tallpineBridge } from "../../lib/bridge";
import { ChromeModeCard } from "./ChromeModeCard";

type Change = { sessionId: string; state: ChromeSessionState };

function stubBridge(initial: ChromeSessionState) {
	let emit: (change: Change) => void = () => undefined;
	const chromeState = vi.spyOn(tallpineBridge.browser, "chromeState").mockResolvedValue(initial);
	const chromeShow = vi.spyOn(tallpineBridge.browser, "chromeShow").mockResolvedValue({ shown: true });
	vi.spyOn(tallpineBridge.browser, "onChromeStateChanged").mockImplementation((listener) => {
		emit = listener;
		return () => undefined;
	});
	const openExternal = vi.spyOn(tallpineBridge.app, "openExternal").mockResolvedValue(undefined);
	return { chromeState, chromeShow, openExternal, emit: (change: Change) => act(() => emit(change)) };
}

afterEach(() => vi.restoreAllMocks());

function cleanupAndRender(state: ChromeSessionState) {
	cleanup();
	vi.restoreAllMocks();
	stubBridge(state);
	render(<ChromeModeCard sessionId="s1" />);
}

describe("ChromeModeCard", () => {
	it("shows the session's Chrome tab and raises its window", async () => {
		const bridge = stubBridge({ running: true, activeTab: { title: "Sign in - Google Accounts", url: "https://accounts.google.com/" } });
		render(<ChromeModeCard sessionId="s1" />);
		expect(screen.getByText("This session browses in Chrome")).toBeInTheDocument();
		expect(await screen.findByText("Sign in - Google Accounts")).toBeInTheDocument();
		expect(screen.getByText("https://accounts.google.com/")).toBeInTheDocument();
		expect(bridge.chromeState).toHaveBeenCalledWith("s1");
		await userEvent.click(screen.getByRole("button", { name: "Show Chrome window" }));
		expect(bridge.chromeShow).toHaveBeenCalledWith("s1");
	});

	it("follows state changes of its own session only", async () => {
		const bridge = stubBridge({ running: false, activeTab: null });
		render(<ChromeModeCard sessionId="s1" />);
		expect(await screen.findByText("No Chrome tab yet. One opens on the agent's next browser command.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show Chrome window" })).toBeDisabled();
		bridge.emit({ sessionId: "s2", state: { running: true, activeTab: { title: "Other", url: "https://other.example/" } } });
		expect(screen.queryByText("Other")).not.toBeInTheDocument();
		bridge.emit({ sessionId: "s1", state: { running: true, activeTab: { title: "Mine", url: "https://mine.example/" } } });
		expect(screen.getByText("Mine")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show Chrome window" })).toBeEnabled();
	});

	it("on a Google sign-in page, says to sign in through Sign in to sites, not the agent's window (B854)", async () => {
		stubBridge({ running: true, activeTab: { title: "Sign in", url: "https://accounts.google.com/v3/signin/identifier?continue=https://analytics.google.com/" } });
		render(<ChromeModeCard sessionId="s1" />);
		expect(await screen.findByTestId("browser-chrome-mode-google-sign-in")).toHaveTextContent("Click Sign in to sites and sign in in the Chrome that opens.");
		cleanupAndRender({ running: true, activeTab: { title: "Example", url: "https://example.com/" } });
		expect(await screen.findByText("Example")).toBeInTheDocument();
		expect(screen.queryByTestId("browser-chrome-mode-google-sign-in")).not.toBeInTheDocument();
	});

	it("offers the Chrome download when Chrome is not installed", async () => {
		const bridge = stubBridge({ running: false, activeTab: null, error: { code: "BROWSER_CHROME_NOT_FOUND", message: "raw" } });
		render(<ChromeModeCard sessionId="s1" />);
		expect(await screen.findByText("Google Chrome is not installed.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Get Google Chrome" }));
		expect(bridge.openExternal).toHaveBeenCalledWith("https://www.google.com/chrome/");
	});

	it("asks the person to quit whatever holds the Chrome profile", async () => {
		stubBridge({ running: false, activeTab: null, error: { code: "BROWSER_CHROME_PROFILE_IN_USE", message: "raw" } });
		render(<ChromeModeCard sessionId="s1" />);
		expect(await screen.findByRole("alert")).toHaveTextContent("Another program is using Tallpine's Chrome profile. Quit it, then run the command again.");
	});

	it("opens Chrome for signing in to sites, then Done brings the agent's Chrome back (B854)", async () => {
		stubBridge({ running: true, activeTab: null });
		const signingIn: ChromeSessionState = { running: false, signingIn: true, activeTab: null };
		const signIn = vi.spyOn(tallpineBridge.browser, "chromeSignIn").mockResolvedValue(signingIn);
		const done = vi.spyOn(tallpineBridge.browser, "chromeSignInDone").mockResolvedValue({ running: true, signingIn: false, activeTab: null });
		render(<ChromeModeCard sessionId="s1" />);
		await userEvent.click(await screen.findByRole("button", { name: "Sign in to sites" }));
		expect(signIn).toHaveBeenCalledWith("s1");
		expect(await screen.findByText("Signing in to sites")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Show Chrome window" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Done" }));
		expect(done).toHaveBeenCalledWith("s1");
		expect(await screen.findByText("This session browses in Chrome")).toBeInTheDocument();
	});

	it("shows the signing-in state another card started", async () => {
		const bridge = stubBridge({ running: true, activeTab: null });
		render(<ChromeModeCard sessionId="s1" />);
		expect(await screen.findByRole("button", { name: "Sign in to sites" })).toBeEnabled();
		bridge.emit({ sessionId: "s1", state: { running: false, signingIn: true, activeTab: null } });
		expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
	});
});
