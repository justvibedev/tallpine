import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserSitePermissionEntry, BrowserSitePermissionPrompt } from "../../../shared/browser-site-permissions";
import { BrowserSitePermissionBar } from "./BrowserSitePermissionBar";
import { BrowserSiteSettingsMenu } from "./BrowserSiteSettingsMenu";
import { TooltipProvider } from "../ui/tooltip";
import type { BrowserPanelViewState } from "./browser-types";

function viewState(fields: { sitePermissionPrompt?: BrowserSitePermissionPrompt; sitePermissions?: BrowserSitePermissionEntry[] }) {
	return {
		viewId: "1:sess",
		navState: { viewId: "1:sess", url: "https://meet.example/room", title: "", canGoBack: false, canGoForward: false, isLoading: false, ...fields },
		t: (key: string, options?: Record<string, string>) => (options ? `${key} ${JSON.stringify(options)}` : key),
	} as unknown as BrowserPanelViewState;
}

afterEach(() => vi.restoreAllMocks());

describe("BrowserSitePermissionBar", () => {
	it("stays hidden while the page waits on nothing", () => {
		render(<BrowserSitePermissionBar v={viewState({})} />);
		expect(screen.queryByTestId("browser-site-permission-bar")).toBeNull();
	});

	it("names the site and the devices, and sends Allow and Block for the prompt", async () => {
		const respond = vi.spyOn(window.tallpine!.browser, "respondSitePermission");
		const prompt = { id: "p1", origin: "https://meet.example", kinds: ["camera", "microphone"] } as BrowserSitePermissionPrompt;
		render(<BrowserSitePermissionBar v={viewState({ sitePermissionPrompt: prompt })} />);

		const text = screen.getByTestId("browser-site-permission-bar").textContent ?? "";
		expect(text).toContain("browser.sitePermissions.prompt");
		expect(text).toContain('"site":"meet.example"');
		expect(text).toContain("browser.sitePermissions.kind.camera, browser.sitePermissions.kind.microphone");

		await userEvent.click(screen.getByRole("button", { name: "browser.sitePermissions.allow" }));
		expect(respond).toHaveBeenLastCalledWith({ viewId: "1:sess", promptId: "p1", allow: true });
		await userEvent.click(screen.getByRole("button", { name: "browser.sitePermissions.block" }));
		expect(respond).toHaveBeenLastCalledWith({ viewId: "1:sess", promptId: "p1", allow: false });
	});

	it("asks before a mailto: link opens another app", async () => {
		const respond = vi.spyOn(window.tallpine!.browser, "respondSitePermission");
		const prompt = { id: "p2", origin: "https://meet.example", kinds: [], externalUrl: "mailto:team@example.com" };
		render(<BrowserSitePermissionBar v={viewState({ sitePermissionPrompt: prompt })} />);

		expect(screen.getByTestId("browser-site-permission-bar").textContent).toContain("mailto:team@example.com");
		await userEvent.click(screen.getByRole("button", { name: "browser.sitePermissions.open" }));
		expect(respond).toHaveBeenLastCalledWith({ viewId: "1:sess", promptId: "p2", allow: true });
	});
});

describe("BrowserSiteSettingsMenu", () => {
	it("is absent until the site has a remembered answer", () => {
		render(<BrowserSiteSettingsMenu v={viewState({})} />);
		expect(screen.queryByRole("button", { name: "browser.sitePermissions.settings" })).toBeNull();
	});

	it("lists remembered answers and resets one or all of them", async () => {
		const reset = vi.spyOn(window.tallpine!.browser, "resetSitePermission");
		const remembered: BrowserSitePermissionEntry[] = [
			{ kind: "camera", decision: "allow" },
			{ kind: "notifications", decision: "block" },
		];
		render(
			<TooltipProvider>
				<BrowserSiteSettingsMenu v={viewState({ sitePermissions: remembered })} />
			</TooltipProvider>,
		);

		await userEvent.click(screen.getByRole("button", { name: "browser.sitePermissions.settings" }));
		expect(screen.getByText('browser.sitePermissions.settingsFor {"site":"meet.example"}')).toBeTruthy();
		expect(screen.getByText("browser.sitePermissions.blocked")).toBeTruthy();
		await userEvent.click(screen.getByText("browser.sitePermissions.kind.camera"));
		expect(reset).toHaveBeenLastCalledWith({ viewId: "1:sess", kind: "camera" });

		await userEvent.click(screen.getByRole("button", { name: "browser.sitePermissions.settings" }));
		await userEvent.click(screen.getByText("browser.sitePermissions.resetAll"));
		expect(reset).toHaveBeenLastCalledWith({ viewId: "1:sess" });
	});
});
