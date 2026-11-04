import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ContextBreakdown, formatShare } from "./ContextBreakdown";
import type { ContextBreakdownData } from "../../hooks/useContextBreakdown";

const data: ContextBreakdownData = {
	available: true,
	totalTokens: 50_000,
	maxTokens: 200_000,
	autoCompactThreshold: 140_000,
	categories: [
		{ id: "system_prompt", name: "System prompt", tokens: 6_000, kind: "used" },
		{ id: "messages", name: "Messages", tokens: 30_000, kind: "used" },
		{ id: "skills", name: "Skills", tokens: 0, kind: "used" },
		{ id: "mcp_tools_deferred", name: "MCP tools (deferred)", tokens: 900, kind: "deferred" },
		{ id: "free_space", name: "Free space", tokens: 110_000, kind: "free" },
		{ id: "autocompact_buffer", name: "Autocompact buffer", tokens: 40_000, kind: "buffer" },
	],
	messages: { toolCalls: 4_000, toolResults: 20_000, assistant: 3_000, user: 3_000 },
	memoryFiles: [
		{ path: "/repo/CLAUDE.md", type: "Project", tokens: 1_200 },
		{ path: "/home/u/.claude/CLAUDE.md", type: "User", tokens: 300 },
	],
};

describe("ContextBreakdown", () => {
	it("shows each category the agent reported with tokens and its share of the window", () => {
		render(<ContextBreakdown data={data} />);
		const section = screen.getByLabelText("What fills the context");
		expect(within(section).getByText("System prompt").parentElement).toHaveTextContent("6K3.0%");
		expect(within(section).getByText("Messages").parentElement).toHaveTextContent("30K15%");
		expect(within(section).getByText("Free space").parentElement).toHaveTextContent("110K55%");
		expect(within(section).getByText("Compaction reserve").parentElement).toHaveTextContent("40K20%");
		expect(within(section).getByText("Compacts at").parentElement).toHaveTextContent("140K70%");
	});

	it("shows one Compacts at, the resolved one, and the agent's figure only when it differs", () => {
		const { rerender } = render(<ContextBreakdown data={data} resolved={{ threshold: 400_000, capacity: 1_000_000 }} />);
		expect(screen.getAllByText("Compacts at")).toHaveLength(1);
		expect(screen.getByText("Compacts at").parentElement).toHaveTextContent("400K40%");
		expect(screen.getByText("Agent compacts at").parentElement).toHaveTextContent("140K14%");
		rerender(<ContextBreakdown data={data} resolved={{ threshold: 140_000, capacity: 200_000 }} />);
		expect(screen.queryByText("Agent compacts at")).toBeNull();
		expect(screen.getAllByText("Compacts at")).toHaveLength(1);
	});

	it("splits the messages row into the parts the agent reported", () => {
		render(<ContextBreakdown data={data} />);
		expect(screen.getByText("Tool results").parentElement).toHaveTextContent("20K10%");
		expect(screen.getByText("Tool calls")).toBeInTheDocument();
		// A part the agent did not report is not invented.
		expect(screen.queryByText("Attachments")).toBeNull();
	});

	it("does not show a split of the messages row that contradicts the row itself", () => {
		// Parts sum to 21,6K against a 5,5K row: as a live Claude Code session reported it.
		render(<ContextBreakdown data={{ ...data, categories: [{ id: "messages", name: "Messages", tokens: 5_507, kind: "used" }], messages: { toolCalls: 43, toolResults: 28, attachments: 21_493, assistant: 7, user: 23 } }} />);
		expect(screen.getByText("Messages")).toBeInTheDocument();
		expect(screen.queryByText("Tool results")).toBeNull();
		expect(screen.queryByText("Attachments")).toBeNull();
	});

	it("omits categories that cost nothing and keeps deferred tools out of the window", () => {
		render(<ContextBreakdown data={data} />);
		expect(screen.queryByText("Skills")).toBeNull();
		const notInWindow = screen.getByText("Not in the window (loaded on demand)").parentElement as HTMLElement;
		expect(within(notInWindow).getByText("MCP tools (deferred)")).toBeInTheDocument();
	});

	it("lists instruction files largest first with their own counts", () => {
		render(<ContextBreakdown data={data} />);
		const files = screen.getByText("Instruction files").parentElement as HTMLElement;
		const rows = within(files).getAllByRole("term").map((n) => n.textContent);
		expect(rows).toEqual(["repo/CLAUDE.md", ".claude/CLAUDE.md"]);
	});

	it("falls back to the agent's own name for a row it has no translation for", () => {
		render(<ContextBreakdown data={{ ...data, categories: [{ id: "future_row", name: "Brand new row", tokens: 5_000, kind: "used" }] }} />);
		expect(screen.getByText("Brand new row")).toBeInTheDocument();
	});

	it("draws nothing without a window or without categories", () => {
		const { container, rerender } = render(<ContextBreakdown data={{ ...data, maxTokens: 0 }} />);
		expect(container).toBeEmptyDOMElement();
		rerender(<ContextBreakdown data={{ ...data, categories: [] }} />);
		expect(container).toBeEmptyDOMElement();
	});
});

describe("formatShare", () => {
	it("never rounds a sliver to nothing and keeps one decimal below 10%", () => {
		expect(formatShare(1, 100_000)).toBe("<0.1%");
		expect(formatShare(450, 10_000)).toBe("4.5%");
		expect(formatShare(15_000, 100_000)).toBe("15%");
		expect(formatShare(0, 100)).toBe("0%");
		expect(formatShare(5, 0)).toBe("0%");
	});
});
