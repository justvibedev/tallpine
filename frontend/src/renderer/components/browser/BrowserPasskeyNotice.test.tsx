import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { BrowserPasskeyNotice } from "./BrowserPasskeyNotice";
import type { BrowserPanelViewState } from "./browser-types";

function viewState(passkeyPending: boolean | undefined, openCurrentPageExternally = vi.fn(async (_url?: string) => undefined), passkeyExternalUrl?: string) {
	return {
		navState: { viewId: "1:sess", url: "https://accounts.example/signin", title: "", canGoBack: false, canGoForward: false, isLoading: false, passkeyPending, passkeyExternalUrl },
		openCurrentPageExternally,
		t: (key: string) => key,
	} as unknown as BrowserPanelViewState;
}

describe("BrowserPasskeyNotice", () => {
	it("stays hidden while no passkey request is pending", () => {
		render(<BrowserPasskeyNotice v={viewState(undefined)} />);
		expect(screen.queryByTestId("browser-passkey-notice")).toBeNull();
	});

	it("offers the default browser while the page awaits a passkey", async () => {
		const open = vi.fn(async (_url?: string) => undefined);
		render(<BrowserPasskeyNotice v={viewState(true, open, "https://app.example/dash")} />);

		expect(screen.getByTestId("browser-passkey-notice").textContent).toContain("browser.passkey.message");
		await userEvent.click(screen.getByRole("button", { name: "browser.passkey.openExternal" }));
		expect(open).toHaveBeenCalledWith("https://app.example/dash");
	});

	it("opens Settings → Browser profiles to import a sign-in", async () => {
		const openGlobalSettings = vi.fn();
		useUiStore.setState({ openGlobalSettings });
		render(<BrowserPasskeyNotice v={viewState(true)} />);
		await userEvent.click(screen.getByRole("button", { name: "browser.passkey.importSignIn" }));
		expect(openGlobalSettings).toHaveBeenCalledWith("browserProfiles");
	});

	it("hides on dismiss and shows again for the next passkey request", async () => {
		const { rerender } = render(<BrowserPasskeyNotice v={viewState(true)} />);
		await userEvent.click(screen.getByRole("button", { name: "browser.passkey.dismiss" }));
		expect(screen.queryByTestId("browser-passkey-notice")).toBeNull();

		rerender(<BrowserPasskeyNotice v={viewState(true)} />);
		expect(screen.queryByTestId("browser-passkey-notice")).toBeNull();

		rerender(<BrowserPasskeyNotice v={viewState(false)} />);
		rerender(<BrowserPasskeyNotice v={viewState(true)} />);
		expect(screen.getByTestId("browser-passkey-notice")).not.toBeNull();
	});
});
