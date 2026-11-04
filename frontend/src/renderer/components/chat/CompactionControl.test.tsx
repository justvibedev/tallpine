import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CompactionControl, type CompactionControlProps } from "./CompactionControl";

function setup(over: Partial<CompactionControlProps> = {}) {
	const props: CompactionControlProps = {
		pendingRestart: false,
		onChange: vi.fn(),
		onSetDefault: vi.fn(),
		onRestart: vi.fn(),
		...over,
	};
	render(<CompactionControl {...props} />);
	return props;
}

describe("CompactionControl", () => {
	it("says the change applies now when the running agent already follows it", () => {
		setup({ value: "40%" });
		expect(screen.getByRole("status")).toHaveTextContent("Applies now");
		expect(screen.queryByRole("button", { name: /Restart now/ })).toBeNull();
	});

	it("says it applies after restart and offers a one-click restart that keeps history", async () => {
		const props = setup({ value: "40%", pendingRestart: true });
		expect(screen.getByRole("status")).toHaveTextContent("Applies after restart");
		await userEvent.click(screen.getByRole("button", { name: "Restart now (keeps history)" }));
		expect(props.onRestart).toHaveBeenCalledTimes(1);
	});

	it("shows a busy agent as such instead of a generic error", () => {
		setup({ pendingRestart: true, restartBusy: true });
		expect(screen.getByRole("alert")).toHaveTextContent("The agent is working. Restart when it is idle.");
	});

	it("changes this session's window from the select, including 40%", async () => {
		const props = setup();
		await userEvent.selectOptions(screen.getByRole("combobox"), "40%");
		expect(props.onChange).toHaveBeenCalledWith("40%");
	});

	it("takes any whole percent from 20 to 95 through Custom, and nothing else", async () => {
		const props = setup();
		await userEvent.selectOptions(screen.getByRole("combobox"), "custom");
		const input = screen.getByRole("textbox");
		await userEvent.type(input, "19{Enter}");
		expect(props.onChange).not.toHaveBeenCalled();
		expect(input).toHaveAttribute("aria-invalid", "true");
		await userEvent.clear(input);
		await userEvent.type(input, "45{Enter}");
		expect(props.onChange).toHaveBeenCalledWith("45%");
	});

	it("keeps a saved off-step percent selectable as its own option", () => {
		setup({ value: "45%" });
		expect(screen.getByRole("combobox")).toHaveValue("45%");
	});

	it("offers Set as default only for a choice that differs from the default", async () => {
		const props = setup({ value: "40%", defaultChoice: "70%" });
		await userEvent.click(screen.getByRole("button", { name: "Set as default" }));
		expect(props.onSetDefault).toHaveBeenCalledWith("40%");
	});

	it("hides Set as default when the session follows the default or already matches it", () => {
		const { rerender } = render(<CompactionControl pendingRestart={false} onChange={vi.fn()} onSetDefault={vi.fn()} />);
		expect(screen.queryByRole("button", { name: "Set as default" })).toBeNull();
		rerender(<CompactionControl pendingRestart={false} value="40%" defaultChoice="40%" onChange={vi.fn()} onSetDefault={vi.fn()} />);
		expect(screen.queryByRole("button", { name: "Set as default" })).toBeNull();
	});
});
