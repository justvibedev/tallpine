import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { PhoneOnboardingNotice } from "./PhoneOnboardingNotice";

describe("PhoneOnboardingNotice", () => {
	beforeEach(() => window.localStorage.clear());

	it("opens the Connections settings page", () => {
		const calls: unknown[] = [];
		useUiStore.setState({ openGlobalSettings: ((s?: unknown) => { calls.push(s); }) as never });
		render(<PhoneOnboardingNotice />);
		fireEvent.click(screen.getByRole("button", { name: "Connect your phone" }));
		expect(calls).toEqual(["connections"]);
	});

	it("stays hidden once dismissed", () => {
		const first = render(<PhoneOnboardingNotice />);
		fireEvent.click(screen.getByRole("button", { name: "Dismiss phone setup tip" }));
		expect(screen.queryByTestId("phone-onboarding-notice")).toBeNull();
		first.unmount();
		render(<PhoneOnboardingNotice />);
		expect(screen.queryByTestId("phone-onboarding-notice")).toBeNull();
	});
});
