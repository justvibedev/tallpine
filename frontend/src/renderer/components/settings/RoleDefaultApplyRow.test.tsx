import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RoleDefaultApplyRow } from "./RoleDefaultApplyRow";

type Drift = { projectId: string; name: string; current: { agent: string; model: string; effort: string }; change: "clear" | "set" };
const { state, apply, hook } = vi.hoisted(() => {
	const state = {
		projects: [] as Drift[],
		failed: [] as { projectId: string; error: string }[],
	};
	const apply = vi.fn(async (ids: string[]) => ({ role: "orchestrator", applied: ids, failed: state.failed }));
	const hook = vi.fn((role: string, _enabled: boolean) => ({
		report: { role, default: { agent: "claude-code", model: "claude-opus-5-5", effort: "medium" }, projects: state.projects },
		loading: false,
		loadError: undefined,
		apply,
		applying: false,
		applyError: undefined,
		resetApply: () => undefined,
	}));
	return { state, apply, hook };
});
vi.mock("../../hooks/useRoleDefaultApply", () => ({ useRoleDefaultApply: hook }));

beforeEach(() => {
	state.projects = [];
	state.failed = [];
	apply.mockClear();
	hook.mockClear();
});

describe("RoleDefaultApplyRow", () => {
	it("reads nothing until the owner opens the dialog", () => {
		render(<RoleDefaultApplyRow role="orchestrator" disabled={false} />);
		expect(hook).not.toHaveBeenCalled();
	});

	it("lists the projects that differ with what they run now, and applies to exactly those", async () => {
		state.projects = [
			{ projectId: "tower-wars", name: "Tower Wars", current: { agent: "claude-code", model: "claude-opus-5-5", effort: "high" }, change: "clear" },
			{ projectId: "web", name: "Web", current: { agent: "codex", model: "", effort: "" }, change: "clear" },
		];
		const user = userEvent.setup();
		render(<RoleDefaultApplyRow role="orchestrator" disabled={false} />);
		await user.click(screen.getByRole("button", { name: "Review projects" }));
		expect(hook).toHaveBeenCalledWith("orchestrator", true);
		expect(screen.getByRole("dialog", { name: "Apply the default to all projects?" })).toHaveTextContent("Claude Code · claude-opus-5-5 · medium");
		const list = screen.getByTestId("apply-default-orchestrator-projects");
		expect(list).toHaveTextContent("Tower Wars");
		expect(list).toHaveTextContent("claude-opus-5-5 · high");
		expect(list).toHaveTextContent("agent default · agent default");
		await user.click(screen.getByRole("button", { name: "Apply" }));
		expect(apply).toHaveBeenCalledWith(["tower-wars", "web"]);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("says so when every project already uses the default, and offers nothing to apply", async () => {
		const user = userEvent.setup();
		render(<RoleDefaultApplyRow role="worker" disabled={false} />);
		await user.click(screen.getByRole("button", { name: "Review projects" }));
		expect(screen.getByText("Every project already uses this default.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
	});

	it("keeps the dialog open and names each project it could not change", async () => {
		state.projects = [{ projectId: "locked", name: "Locked", current: { agent: "claude-code", model: "x", effort: "" }, change: "clear" }];
		state.failed = [{ projectId: "locked", error: "managed by policy" }];
		const user = userEvent.setup();
		render(<RoleDefaultApplyRow role="reviewer" disabled={false} />);
		await user.click(screen.getByRole("button", { name: "Review projects" }));
		await user.click(screen.getByRole("button", { name: "Apply" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("locked was not changed: managed by policy");
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});
});
