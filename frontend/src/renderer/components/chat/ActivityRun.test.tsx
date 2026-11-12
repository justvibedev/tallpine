import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { ActivityRun } from "./ActivityRun";

function tool(
	id: string,
	providerItemId: string,
	summary: string,
	parentProviderItemId?: string,
): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId,
		sequence: Number(id.replace(/\D/g, "")),
		revision: 0,
		activityKind: "mcp_tool",
		status: "completed",
		summary,
		detail: { parentProviderItemId },
		createdAt: "2026-08-04T00:00:00Z",
	};
}

describe("ActivityRun nested agents", () => {
	it("keeps child-agent work under its parent and collapsed by default", async () => {
		const user = userEvent.setup();
		render(
			<ActivityRun
				activities={[
					tool("activity-1", "agent-tool", "Delegate repository scan"),
					tool("activity-2", "child-tool", "Search source files", "agent-tool"),
				]}
			/>,
		);

		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		expect(await screen.findByRole("button", { name: /Subagent 1 step/i })).toHaveAttribute(
			"aria-expanded",
			"false",
		);
		expect(screen.queryByText("Search source files")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: /Subagent 1 step/i }));
		expect(await screen.findByText("Search source files")).toBeInTheDocument();
	});

	it("keeps malformed provider cycles visible at the top level", async () => {
		const user = userEvent.setup();
		render(
			<ActivityRun
				activities={[
					tool("activity-1", "tool-a", "First", "tool-b"),
					tool("activity-2", "tool-b", "Second", "tool-a"),
				]}
			/>,
		);
		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		expect(await screen.findByText("First")).toBeInTheDocument();
		expect(await screen.findByText("Second")).toBeInTheDocument();
	});
});

describe("ActivityRun in a running turn", () => {
	const calls = [
		tool("activity-1", "tool-a", "Read package.json"),
		tool("activity-2", "tool-b", "Search for SEC-164"),
	];

	it("shows each call while the turn runs and folds once it settles", async () => {
		const { rerender } = render(<ActivityRun activities={calls} live />);
		expect(screen.getByRole("button", { name: /Ran 2 tool calls/i })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Read package.json")).toBeInTheDocument();
		expect(screen.getByText("Search for SEC-164")).toBeInTheDocument();

		rerender(<ActivityRun activities={calls} live={false} />);
		expect(screen.getByRole("button", { name: /Ran 2 tool calls/i })).toHaveAttribute("aria-expanded", "false");
		await waitFor(() => expect(screen.queryByText("Read package.json")).not.toBeInTheDocument());
	});

	it("keeps the reader's choice over the live default", async () => {
		const user = userEvent.setup();
		const { rerender } = render(<ActivityRun activities={calls} live />);
		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		await waitFor(() => expect(screen.queryByText("Read package.json")).not.toBeInTheDocument());

		rerender(<ActivityRun activities={calls} live={false} />);
		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		rerender(<ActivityRun activities={calls} live />);
		rerender(<ActivityRun activities={calls} live={false} />);
		expect(screen.getByText("Read package.json")).toBeInTheDocument();
	});

	it("shows a working subagent's steps while the turn runs", () => {
		render(
			<ActivityRun
				live
				activities={[
					tool("activity-1", "agent-tool", "Delegate repository scan"),
					{ ...tool("activity-2", "child-tool", "Search source files", "agent-tool"), status: "running" },
				]}
			/>,
		);
		expect(screen.getByRole("button", { name: /Subagent 1 step/i })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Search source files")).toBeInTheDocument();
	});
});

describe("ActivityRun live cap", () => {
	const many = (n: number) => Array.from({ length: n }, (_, i) => tool(`activity-${i + 1}`, `t${i + 1}`, `Call ${i + 1}`));

	it("shows only the newest rows while live, with an earlier expander", async () => {
		const user = userEvent.setup();
		render(<ActivityRun activities={many(8)} live />);
		expect(screen.queryByText("Call 1")).not.toBeInTheDocument();
		expect(screen.queryByText("Call 3")).not.toBeInTheDocument();
		expect(screen.getByText("Call 4")).toBeInTheDocument();
		expect(screen.getByText("Call 8")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "3 earlier" }));
		expect(await screen.findByText("Call 1")).toBeInTheDocument();
	});

	it("keeps failures and diffs visible among folded rows", () => {
		const calls = many(8);
		calls[0] = { ...calls[0]!, status: "failed" };
		calls[1] = { ...calls[1]!, activityKind: "file_change" };
		render(<ActivityRun activities={calls} live />);
		expect(screen.getByText("Call 1")).toBeInTheDocument();
		expect(screen.getByText("Call 2")).toBeInTheDocument();
		expect(screen.queryByText("Call 3")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "1 earlier" })).toBeInTheDocument();
	});

	it("does not cap a settled run", async () => {
		const user = userEvent.setup();
		render(<ActivityRun activities={many(8)} live={false} />);
		await user.click(screen.getByRole("button", { name: /tool calls/i }));
		expect(await screen.findByText("Call 1")).toBeInTheDocument();
		expect(screen.queryByText(/earlier/)).not.toBeInTheDocument();
	});
});
