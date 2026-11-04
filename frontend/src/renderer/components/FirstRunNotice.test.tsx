import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const getUiSettings = vi.fn();
const setUiSettings = vi.fn();

vi.mock("../lib/bridge", () => ({
	tallpineBridge: {
		app: { openExternal: vi.fn() },
		uiSettings: {
			get: (...args: unknown[]) => getUiSettings(...args),
			set: (...args: unknown[]) => setUiSettings(...args),
		},
	},
}));

import { useFirstRunNoticeStore } from "../stores/first-run-notice-store";
import { FirstRunNotice } from "./FirstRunNotice";

describe("FirstRunNotice", () => {
	beforeEach(() => {
		getUiSettings.mockReset();
		setUiSettings.mockReset();
		setUiSettings.mockResolvedValue({});
		useFirstRunNoticeStore.setState({ loaded: false, accepted: false });
	});

	it("shows when unacknowledged, and hides and persists after acknowledging", async () => {
		getUiSettings.mockResolvedValue({ firstRunNoticeAccepted: false });
		render(<FirstRunNotice />);
		const button = await screen.findByRole("button", { name: "I understand" });
		await act(async () => {
			await userEvent.click(button);
		});
		expect(setUiSettings).toHaveBeenCalledWith({ firstRunNoticeAccepted: true });
		expect(screen.queryByRole("button", { name: "I understand" })).toBeNull();
	});

	it("is skipped when already accepted (including managed pre-accept, which main reports as accepted)", async () => {
		getUiSettings.mockResolvedValue({ firstRunNoticeAccepted: true });
		render(<FirstRunNotice />);
		await act(async () => {});
		expect(useFirstRunNoticeStore.getState().loaded).toBe(true);
		expect(screen.queryByRole("button", { name: "I understand" })).toBeNull();
	});
});
