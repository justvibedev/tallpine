import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";
import { InstructionsAndSkillsLsp, withPluginRoles } from "./InstructionsAndSkillsLsp";

type Lsp = components["schemas"]["ControllersProjectLSP"];
const roles = (on: string[]) => (["worker", "orchestrator", "reviewer"] as const).map((role) => ({ role, on: on.includes(role), set: false, app: false, default: false, effects: [] }));
const lsp: Lsp = {
	recommendations: [{ language: "go", files: 40, pluginId: "gopls-lsp@claude-plugins-official", pluginInstalled: false, pluginInstall: "claude plugin install gopls-lsp@claude-plugins-official", binary: "gopls", binaryInstalled: false, binaryProblem: "", binaryInstall: "go install golang.org/x/tools/gopls@latest", binaryInstallable: true, toolchainInstall: "", toolchainInstallable: false, roles: roles([]) }],
	mismatches: [{ pluginId: "swift-lsp@claude-plugins-official", language: "swift", roles: ["worker", "orchestrator"] }],
};

describe("InstructionsAndSkillsLsp", () => {
	it("shows install commands and enables the recommendation for workers and reviewers only on click", () => {
		const onChange = vi.fn();
		render(<InstructionsAndSkillsLsp values={{} as never} lsp={lsp} onChange={onChange} />);
		expect(screen.getByText("go install golang.org/x/tools/gopls@latest")).toBeTruthy();
		expect(onChange).not.toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: /Enable for workers and reviewers/ }));
		expect(onChange).toHaveBeenCalledWith({ claudePlugins: { worker: { "gopls-lsp@claude-plugins-official": true }, reviewer: { "gopls-lsp@claude-plugins-official": true } } });
	});
	it("turns a mismatched plugin off for the roles it is on for", () => {
		const onChange = vi.fn();
		render(<InstructionsAndSkillsLsp values={{} as never} lsp={lsp} onChange={onChange} />);
		fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
		expect(onChange).toHaveBeenCalledWith({ claudePlugins: { worker: { "swift-lsp@claude-plugins-official": false }, orchestrator: { "swift-lsp@claude-plugins-official": false } } });
	});
	it("hides the mismatch row once the draft switches the plugin off", () => {
		const off = { claudePlugins: { worker: { "swift-lsp@claude-plugins-official": false }, orchestrator: { "swift-lsp@claude-plugins-official": false } } };
		render(<InstructionsAndSkillsLsp values={off as never} lsp={lsp} onChange={vi.fn()} />);
		expect(screen.queryByTestId("lsp-mismatch-swift")).toBeNull();
	});
	it("warns when an installed language server cannot work", () => {
		const rec = { ...lsp.recommendations[0], binaryInstalled: true, binaryProblem: "no-go" };
		render(<InstructionsAndSkillsLsp values={{} as never} lsp={{ ...lsp, recommendations: [rec] }} onChange={vi.fn()} />);
		expect(screen.getByTestId("lsp-binary-problem-go").textContent).toContain("Go toolchain not found");
		expect(screen.queryByText(/gopls found/)).toBeNull();
	});
	it("withPluginRoles keeps other plugins' switches", () => {
		expect(withPluginRoles({ worker: { a: true } }, ["worker"], "b", false)).toEqual({ worker: { a: true, b: false } });
	});
});
