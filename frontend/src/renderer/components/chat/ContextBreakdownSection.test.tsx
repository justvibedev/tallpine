import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ContextBreakdownSection } from "./ContextBreakdownSection";

let state: { breakdown?: unknown; loading: boolean } = { loading: true };
const asked: unknown[][] = [];
vi.mock("../../hooks/useContextBreakdown", () => ({
	useContextBreakdown: (...args: unknown[]) => { asked.push(args); return state; },
}));

describe("ContextBreakdownSection", () => {
	beforeEach(() => { asked.length = 0; });

	it("says it is counting while it waits and invents no numbers", () => {
		state = { loading: true };
		render(<ContextBreakdownSection sessionId="s1" />);
		expect(screen.getByRole("status")).toHaveTextContent("Counting what fills the context");
		expect(screen.queryByText(/%/)).toBeNull();
	});

	it("shows nothing when the agent cannot give a breakdown (totals above stay)", () => {
		state = { loading: false, breakdown: undefined };
		const { container } = render(<ContextBreakdownSection sessionId="s1" />);
		expect(container).toBeEmptyDOMElement();
	});

	it("asks for the session it belongs to", () => {
		state = { loading: false, breakdown: { available: true, maxTokens: 100, totalTokens: 10, categories: [{ name: "Messages", id: "messages", tokens: 10, kind: "used" }] } };
		render(<ContextBreakdownSection sessionId="s1" hostId="h1" />);
		expect(asked[0]).toEqual(["s1", "h1"]);
		expect(screen.getByText("Messages")).toBeInTheDocument();
	});
});
