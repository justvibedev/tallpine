import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProjectSettingsEditor, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { TooltipProvider } from "./ui/tooltip";

const initialValues: ProjectSettingsDraft = {
	displayName: "Project", defaultBranch: "main", sessionPrefix: "ao",
	workerAgent: "codex", orchestratorAgent: "claude-code", reviewerHarness: "",
	workerModel: "", orchestratorModel: "", reviewerModel: "",
	workerMode: "", orchestratorMode: "", reviewerMode: "",
	workerEffort: "", orchestratorEffort: "", reviewerEffort: "",
	workerPermissions: "auto", orchestratorPermissions: "auto", reviewerPermissions: "",
	autoReview: true, intakeEnabled: false, intakeRepo: "", intakeAssignee: "",
};

function setup(cloud: boolean, save = vi.fn().mockResolvedValue({})) {
	const onSaveState = vi.fn();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><TooltipProvider>
		<ProjectSettingsEditor initialValues={initialValues} section="general"
			capabilities={{ workflow: true, sessionPrefix: !cloud, intake: !cloud, reviewer: true, requiredAgents: !cloud, requiredBranch: cloud, nameLimit: cloud ? 120 : 100, runtimeDefaults: cloud }}
			details={[]} modelScope={() => ""} renderAgent={() => null} save={save} onSaveState={onSaveState} />
	</TooltipProvider></QueryClientProvider>);
	return { save, onSaveState };
}

async function edit(label: string, value: string) {
	await userEvent.click(screen.getByRole("button", { name: `Edit ${label}` }));
	fireEvent.change(screen.getByRole("textbox", { name: label }), { target: { value } });
	await userEvent.keyboard("{Enter}");
}

describe.each([false, true])("shared settings page, Cloud=%s", (cloud) => {
	it("uses the same inline fields and autosave, with supported workflow fields", async () => {
		const { save } = setup(cloud);
		expect(screen.getByRole("button", { name: "Edit Project name" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Edit Default branch" })).toBeInTheDocument();
		expect(Boolean(screen.queryByRole("button", { name: "Edit Session prefix" }))).toBe(!cloud);
		await edit("Project name", "Renamed project");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ displayName: "Renamed project", defaultBranch: "main" }));
		await edit("Default branch", "develop");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ displayName: "Renamed project", defaultBranch: "develop" }));
	});

	it("validates a blank name and recovers after editing without a write", async () => {
		const { save, onSaveState } = setup(cloud);
		await edit("Project name", "   ");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Project name is required." })));
		expect(save).not.toHaveBeenCalled();
		await edit("Project name", "Valid name");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", dirty: false, error: undefined })));
	});

	it("waits for writes, retains failed drafts and retries without duplicate autosaves", async () => {
		let reject!: (error: Error) => void;
		const save = vi.fn().mockReturnValue(new Promise((_resolve, fail) => { reject = fail; }));
		const { onSaveState } = setup(cloud, save);
		await edit("Project name", "Pending name");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(screen.getByRole("button", { name: "Edit Project name" })).toBeDisabled();
		fireEvent.submit(document.getElementById("project-settings-form")!);
		expect(save).toHaveBeenCalledTimes(1);
		await act(async () => reject(new Error("Write failed")));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", dirty: true, error: "Write failed" })));
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 750)); });
		expect(save).toHaveBeenCalledTimes(1);
		expect(screen.getByText("Pending name")).toBeInTheDocument();
		save.mockResolvedValue({});
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", dirty: false, error: undefined })));
	});
});

describe("default branch switch from the notice slot", () => {
	it("is saved with the next edit of another field instead of being reverted by a stale draft", async () => {
		const save = vi.fn().mockResolvedValue({});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={client}><TooltipProvider>
			<ProjectSettingsEditor initialValues={{ ...initialValues, defaultBranch: "master" }} section="general"
				capabilities={{ workflow: true, sessionPrefix: true, intake: true, requiredAgents: true, requiredBranch: false, nameLimit: 100, runtimeDefaults: false, reviewer: true }}
				details={[]} modelScope={() => ""} renderAgent={() => null} save={save}
				generalTop={({ setDefaultBranch }) => <button type="button" onClick={() => setDefaultBranch("main")}>switch</button>} />
		</TooltipProvider></QueryClientProvider>);
		await userEvent.click(screen.getByRole("button", { name: "switch" }));
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ defaultBranch: "main" }));
		await edit("Project name", "Renamed");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ displayName: "Renamed", defaultBranch: "main" }));
	});
});

describe("managed (MDM) locks", () => {
	function setupAgents(managedKeys: string[], section: "general" | "agents" = "agents") {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><TooltipProvider>
			<ProjectSettingsEditor initialValues={initialValues} section={section} managedKeys={new Set(managedKeys)}
				capabilities={{ workflow: true, sessionPrefix: true, intake: true, reviewer: true, requiredAgents: true }}
				details={[]} modelScope={() => ""}
				renderAgent={({ role }) => <button type="button" data-testid={`agent-${role}`}>{role}</button>}
				save={vi.fn().mockResolvedValue({})} />
		</TooltipProvider></QueryClientProvider>);
	}

	it("makes a locked role read-only with the organization note and leaves the others editable", () => {
		setupAgents(["project.worker"]);
		expect(screen.getAllByTestId("agent-policy-lock")).toHaveLength(1);
		expect(screen.getByTestId("agent-worker")).toBeDisabled();
		expect(screen.getByTestId("agent-orchestrator")).toBeEnabled();
		// The reviewer role and the stronger reviewer both render an agent picker.
		for (const picker of screen.getAllByTestId("agent-reviewer")) expect(picker).toBeEnabled();
	});

	it("makes the issue-intake section read-only with the note when project.trackerIntake is locked", () => {
		setupAgents(["project.trackerIntake"], "general");
		expect(screen.getAllByTestId("agent-policy-lock")).toHaveLength(1);
		const locked = screen.getByTestId("agent-policy-lock").closest("fieldset");
		expect(locked).toBeDisabled();
		expect(locked?.querySelectorAll("button, input").length).toBeGreaterThan(0);
	});

	it("shows no note when nothing is managed", () => {
		setupAgents([]);
		expect(screen.queryByTestId("agent-policy-lock")).toBeNull();
		expect(screen.getByTestId("agent-worker")).toBeEnabled();
	});
});
