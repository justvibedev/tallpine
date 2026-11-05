import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderCombobox } from "./AgentModelCombobox.test.helpers";

describe("AgentModelCombobox unavailable models", () => {
	it("disables a model the installed CLI is too old for and explains the fix", async () => {
		const { onChange } = renderCombobox([
			{ id: "claude-sonnet-5-5", label: "Sonnet 5.5" },
			{
				id: "claude-opus-5-5",
				label: "Opus 5.5",
				requiresVersion: "2.1.280",
				unavailableReason: "Requires Claude Code 2.1.280 or newer (installed: 2.1.239). Run `claude update`, then try again.",
			},
		], { compact: true });
		await userEvent.click(screen.getByRole("button", { name: "Worker model" }));
		const gated = screen.getByRole("menuitem", { name: /Opus 5\.5/ });
		expect(gated).toHaveAttribute("aria-disabled", "true");
		expect(gated).toHaveTextContent("Requires Claude Code 2.1.280 or newer");
		await userEvent.click(gated);
		expect(onChange).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("menuitem", { name: "Sonnet 5.5" }));
		expect(onChange).toHaveBeenCalledWith("claude-sonnet-5-5");
	});
});
