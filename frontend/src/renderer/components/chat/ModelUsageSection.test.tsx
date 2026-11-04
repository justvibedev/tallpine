import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ModelUsageSection } from "./ModelUsageSection";

let state: { models: unknown[] } = { models: [] };
const asked: unknown[][] = [];
vi.mock("../../hooks/useModelUsage", () => ({
	useModelUsage: (...args: unknown[]) => { asked.push(args); return state; },
}));

describe("ModelUsageSection", () => {
	beforeEach(() => { asked.length = 0; });

	it("shows nothing until a turn reported usage", () => {
		state = { models: [] };
		const { container } = render(<ModelUsageSection sessionId="s1" />);
		expect(container).toBeEmptyDOMElement();
	});

	it("lists each model's four counters for the session it belongs to", () => {
		state = { models: [{ model: "claude-sonnet-5-5", inputTokens: 12, cacheCreationTokens: 2_000, cacheReadTokens: 3_000_000, outputTokens: 40, credits: 0, turns: 3 }] };
		render(<ModelUsageSection sessionId="s1" hostId="h1" />);
		expect(asked[0]).toEqual(["s1", "h1"]);
		expect(screen.getByText("claude-sonnet-5-5")).toBeInTheDocument();
		expect(screen.getByText("Cache read")).toBeInTheDocument();
		expect(screen.getByText(/3(\.0)?M/)).toBeInTheDocument();
	});

	it("adds a credits column only when a model reported credits", () => {
		state = { models: [{ model: "qwen3-coder-next", inputTokens: 0, cacheCreationTokens: 0, cacheReadTokens: 0, outputTokens: 0, credits: 1.5, turns: 2 }] };
		render(<ModelUsageSection sessionId="s1" />);
		expect(screen.getByText("Credits")).toBeInTheDocument();
		expect(screen.getByText("1.50")).toBeInTheDocument();
	});
});
