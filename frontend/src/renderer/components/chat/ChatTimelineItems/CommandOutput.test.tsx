import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { type ConversationActivity } from "../../../types/conversation";
import { CommandOutput } from "./CommandActivity";

function activity(output: string): ConversationActivity {
	return { id: "a1", kind: "command", status: "completed", detail: { output } } as unknown as ConversationActivity;
}

describe("CommandOutput preview", () => {
	it("renders short output in full", () => {
		render(<CommandOutput activity={activity("one\ntwo")} />);
		expect(screen.getByText(/one/).textContent).toBe("one\ntwo");
		expect(screen.queryByText("Showing the end of this large output.")).toBeNull();
	});

	it("keeps only the last 400 lines of newline-heavy output", () => {
		const lines = Array.from({ length: 1000 }, (_, i) => `l${i}`);
		const { container } = render(<CommandOutput activity={activity(lines.join("\n"))} />);
		const text = container.querySelector("pre")?.textContent ?? "";
		expect(text.split("\n")).toHaveLength(400);
		expect(text.endsWith("l999")).toBe(true);
		expect(screen.getByText("Showing the end of this large output.")).toBeTruthy();
	});

	it("bounds one very long line to its tail", () => {
		const { container } = render(<CommandOutput activity={activity("x".repeat(200_000) + "END")} />);
		const text = container.querySelector("pre")?.textContent ?? "";
		expect(text.length).toBeLessThanOrEqual(64 * 1024);
		expect(text.endsWith("END")).toBe(true);
	});
});
