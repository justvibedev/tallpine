import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { UpdatePreferences, UpdateStatus } from "../../../main/update-settings";
import { tallpineBridge } from "../../lib/bridge";
import { TooltipProvider } from "../ui/tooltip";
import { UpdateReadyButton } from "../UpdateReadyButton";
import { AppUpdateRows } from "./AppUpdateRows";

vi.mock("../../lib/bridge", () => ({
	tallpineBridge: {
		appUpdates: {
			getStatus: vi.fn(),
			getPreferences: vi.fn(),
			setSettings: vi.fn(async () => undefined),
			check: vi.fn(async () => undefined),
			install: vi.fn(async () => true),
			installWhenIdle: vi.fn(async () => true),
			cancelInstallWhenIdle: vi.fn(async () => undefined),
			onStatus: vi.fn(() => () => undefined),
		},
	},
}));

const bridge = vi.mocked(tallpineBridge.appUpdates);

function givenUpdates(status: UpdateStatus, preferences: UpdatePreferences | null) {
	bridge.getStatus.mockResolvedValue(status);
	bridge.getPreferences.mockResolvedValue(preferences);
}

beforeEach(() => {
	vi.clearAllMocks();
});

describe("AppUpdateRows", () => {
	it("is on by default and saves the user's opt-out", async () => {
		givenUpdates({ state: "idle" }, { settings: { enabled: true, channel: "stable" }, locked: false });
		render(<AppUpdateRows />);
		const toggle = await screen.findByTestId("app-update-toggle");
		expect(toggle).toHaveAttribute("aria-checked", "true");
		expect(toggle).toBeEnabled();
		await userEvent.click(toggle);
		expect(bridge.setSettings).toHaveBeenCalledWith({ enabled: false, channel: "stable" });
	});

	it("is read-only and says so when managed config decides", async () => {
		givenUpdates({ state: "disabled", reason: "managed" }, { settings: { enabled: false, channel: "stable" }, locked: true, pinnedVersion: "1.2.0" });
		render(<AppUpdateRows />);
		const toggle = await screen.findByTestId("app-update-toggle");
		expect(toggle).toBeDisabled();
		expect(toggle).toHaveAttribute("aria-checked", "false");
		expect(screen.getByTestId("agent-policy-lock")).toBeInTheDocument();
		expect(screen.getByText("Held at version 1.2.0.")).toBeInTheDocument();
		await waitFor(() => expect(screen.getByText("Turned off by your organization.")).toBeInTheDocument());
	});

	it("explains that local builds do not update", async () => {
		givenUpdates({ state: "disabled", reason: "not-a-release" }, { settings: { enabled: true, channel: "stable" }, locked: false });
		render(<AppUpdateRows />);
		expect(await screen.findByText("Not available on development or local builds.")).toBeInTheDocument();
		expect(screen.queryByTestId("app-update-check")).toBeNull();
	});

	it("on a release install shows when it last checked and checks GitHub on Check now", async () => {
		givenUpdates({ state: "idle", checkedAt: Date.now() - 5 * 60_000 }, { settings: { enabled: true, channel: "stable" }, locked: false });
		render(<AppUpdateRows />);
		expect(await screen.findByText(/^Up to date\. Last checked /)).toBeInTheDocument();
		await userEvent.click(screen.getByTestId("app-update-check"));
		expect(bridge.check).toHaveBeenCalledOnce();
	});

	it("shows download progress, then offers Restart to update instead of Check now", async () => {
		givenUpdates({ state: "downloading", version: "0.13.24", percent: 40 }, { settings: { enabled: true, channel: "stable" }, locked: false });
		const { unmount } = render(<AppUpdateRows />);
		expect(await screen.findByText("Downloading 0.13.24… 40%")).toBeInTheDocument();
		expect(screen.queryByTestId("app-update-check")).toBeNull();
		unmount();

		givenUpdates({ state: "downloaded", version: "0.13.24" }, { settings: { enabled: true, channel: "stable" }, locked: false });
		render(<AppUpdateRows />);
		await userEvent.click(await screen.findByTestId("app-update-restart"));
		expect(bridge.install).toHaveBeenCalledOnce();
		expect(screen.queryByTestId("app-update-check")).toBeNull();
	});

	it("disables Check now while a check runs and hides it when the user turned updates off", async () => {
		givenUpdates({ state: "checking" }, { settings: { enabled: true, channel: "stable" }, locked: false });
		const { unmount } = render(<AppUpdateRows />);
		expect(await screen.findByTestId("app-update-check")).toBeDisabled();
		unmount();

		givenUpdates({ state: "disabled", reason: "user" }, { settings: { enabled: false, channel: "stable" }, locked: false });
		render(<AppUpdateRows />);
		await screen.findByTestId("app-update-toggle");
		expect(screen.queryByTestId("app-update-check")).toBeNull();
	});
});

describe("UpdateReadyButton", () => {
	it("stays hidden until an update is staged", async () => {
		givenUpdates({ state: "downloading", version: "1.1.0" }, null);
		render(<TooltipProvider><UpdateReadyButton /></TooltipProvider>);
		await waitFor(() => expect(bridge.getStatus).toHaveBeenCalled());
		expect(screen.queryByTestId("update-ready")).toBeNull();
	});

	it("restarts into the staged update", async () => {
		givenUpdates({ state: "downloaded", version: "1.1.0" }, null);
		render(<TooltipProvider><UpdateReadyButton /></TooltipProvider>);
		await userEvent.click(await screen.findByTestId("update-ready"));
		expect(bridge.install).toHaveBeenCalledOnce();
	});

	it("offers restart when idle, then shows the pending state with cancel", async () => {
		givenUpdates({ state: "downloaded", version: "1.1.0" }, null);
		const { unmount } = render(<TooltipProvider><UpdateReadyButton /></TooltipProvider>);
		await userEvent.click(await screen.findByTestId("update-when-idle"));
		expect(bridge.installWhenIdle).toHaveBeenCalledOnce();
		expect(bridge.install).not.toHaveBeenCalled();
		unmount();

		givenUpdates({ state: "downloaded", version: "1.1.0", restartWhenIdle: true }, null);
		render(<TooltipProvider><UpdateReadyButton /></TooltipProvider>);
		expect(await screen.findByText("Will restart when agents are idle")).toBeTruthy();
		expect(screen.queryByTestId("update-ready")).toBeNull();
		await userEvent.click(screen.getByTestId("update-cancel-idle"));
		expect(bridge.cancelInstallWhenIdle).toHaveBeenCalledOnce();
	});
});
