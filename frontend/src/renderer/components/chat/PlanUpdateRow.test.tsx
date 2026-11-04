import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PlanUpdateRow } from "./PlanUpdateRow";

describe("PlanUpdateRow", () => {
	it("names the update and its progress", () => {
		render(<PlanUpdateRow update={{ done: 9, total: 10 }} onOpen={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Plan updated · 9/10" })).toBeInTheDocument();
	});

	it("opens the Plan tab when clicked", async () => {
		const onOpen = vi.fn();
		render(<PlanUpdateRow update={{ done: 9, total: 10 }} onOpen={onOpen} />);
		await userEvent.click(screen.getByRole("button", { name: "Plan updated · 9/10" }));
		expect(onOpen).toHaveBeenCalledTimes(1);
	});
});
