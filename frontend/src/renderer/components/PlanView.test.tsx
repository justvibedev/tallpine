import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ConversationPlan } from "../types/conversation";
import { PlanView } from "./PlanView";

const plan: ConversationPlan = {
	spansTurns: true,
	steps: [
		{ text: "Done one", status: "completed" },
		{ text: "Parked", status: "pending" },
		{ text: "Working", status: "in_progress" },
		{ text: "Done two", status: "completed" },
	],
};

describe("PlanView", () => {
	it("lists open steps first, in progress before not started", () => {
		render(<PlanView plan={plan} />);
		const items = screen.getAllByRole("listitem").map((item) => item.getAttribute("aria-label"));
		expect(items).toEqual(["Working — in progress", "Parked — not started"]);
	});

	it("folds completed steps under a count until toggled", () => {
		render(<PlanView plan={plan} />);
		expect(screen.queryByLabelText("Done one — done")).toBeNull();
		fireEvent.click(screen.getByRole("button", { name: /2 done/ }));
		expect(screen.getByLabelText("Done one — done")).toBeInTheDocument();
		expect(screen.getByLabelText("Done two — done")).toBeInTheDocument();
	});

	it("has no toggle when nothing is finished", () => {
		render(<PlanView plan={{ spansTurns: true, steps: [{ text: "Only", status: "pending" }] }} />);
		expect(screen.queryByRole("button")).toBeNull();
	});
});

describe("PlanView ownership", () => {
	it("names whose plan it is", () => {
		render(<PlanView plan={plan} sessionTitle="Fix login" />);
		expect(screen.getByText("Fix login’s plan")).toBeInTheDocument();
	});

	it("explains the list when everything is done or empty", () => {
		render(<PlanView plan={{ spansTurns: true, steps: [{ text: "A", status: "completed" }] }} />);
		expect(screen.getByText(/project backlog is in the left rail/)).toBeInTheDocument();
		expect(screen.queryByLabelText("A — done")).toBeNull();
	});

	it("omits the hint while work is open", () => {
		render(<PlanView plan={plan} />);
		expect(screen.queryByText(/project backlog is in the left rail/)).toBeNull();
	});
});
