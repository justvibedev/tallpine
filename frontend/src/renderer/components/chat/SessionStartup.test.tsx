import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SessionProvisionStep } from "../../types/workspace";
import { SessionStartup } from "./SessionStartup";

const steps = (...pairs: Array<[SessionProvisionStep["id"], SessionProvisionStep["status"], string?]>): SessionProvisionStep[] =>
	pairs.map(([id, status, startedAt]) => ({ id, status, ...(startedAt ? { startedAt } : {}) }) as SessionProvisionStep);

describe("SessionStartup", () => {
	afterEach(() => vi.useRealTimers());

	it("shows one plain-language label per step", () => {
		render(<SessionStartup failed={false} agentName="Claude Code" steps={steps(["fetch", "done"], ["worktree", "done"], ["setup", "pending"], ["agent", "running"])} />);
		const items = within(screen.getByTestId("session-startup")).getAllByRole("listitem");
		expect(items[0]).toHaveTextContent("Getting the latest code");
		expect(items[1]).toHaveTextContent("Workspace created");
		expect(items[2]).toHaveTextContent("Running your setup script");
		expect(items[3]).toHaveTextContent("Getting Claude Code ready");
	});

	it("labels a running workspace step as preparing and shimmers only the running label", () => {
		render(<SessionStartup failed={false} agentName="Codex" steps={steps(["worktree", "running"], ["agent", "pending"])} />);
		const items = screen.getAllByRole("listitem");
		expect(items[0]).toHaveTextContent("Preparing your workspace");
		expect(items[0].querySelector(".chat-working-shimmer")).toBeInTheDocument();
		expect(items[1].querySelector(".chat-working-shimmer")).not.toBeInTheDocument();
	});

	it("staggers the steps of a fresh start and shows a reopened one at once", () => {
		const now = new Date("2026-10-29T10:00:00Z");
		vi.useFakeTimers({ now });
		const { unmount } = render(<SessionStartup failed={false} agentName="Codex" steps={steps(["worktree", "running", now.toISOString()], ["agent", "pending"])} />);
		const fresh = screen.getAllByRole("listitem");
		expect(fresh[0]).toHaveClass("session-startup-step-in");
		expect(fresh[1].style.animationDelay).toBe("90ms");
		unmount();

		render(<SessionStartup failed={false} agentName="Codex" steps={steps(["fetch", "done"], ["agent", "running"])} />);
		for (const item of screen.getAllByRole("listitem")) expect(item).not.toHaveClass("session-startup-step-in");
	});
});
