import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProjectSettingsEditor, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { TooltipProvider } from "./ui/tooltip";

const initialValues: ProjectSettingsDraft = {
	displayName: "Project", defaultBranch: "main", sessionPrefix: "ao", githubUser: "",
	workerAgent: "codex", orchestratorAgent: "claude-code", reviewerHarness: "",
	workerModel: "", orchestratorModel: "", reviewerModel: "",
	workerMode: "", orchestratorMode: "", reviewerMode: "",
	workerEffort: "", orchestratorEffort: "", reviewerEffort: "",
	workerPermissions: "auto", orchestratorPermissions: "auto", reviewerPermissions: "",
	autoReview: true, intakeEnabled: false, intakeRepo: "", intakeAssignee: "",
};

function setup(githubUser: boolean, save = vi.fn().mockResolvedValue({})) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><TooltipProvider>
		<ProjectSettingsEditor initialValues={initialValues} section="general"
			capabilities={{ workflow: true, sessionPrefix: true, githubUser, intake: false, reviewer: true, requiredAgents: true }}
			details={[{ label: "GitHub account", value: "octo-bot" }]} modelScope={() => ""} renderAgent={() => null} save={save} onSaveState={vi.fn()} />
	</TooltipProvider></QueryClientProvider>);
	return save;
}

describe("project GitHub account settings", () => {
	it("shows the picked account read-only and saves the override", async () => {
		const save = setup(true);
		expect(screen.getByText("octo-bot")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Edit GitHub account override" }));
		fireEvent.change(screen.getByRole("textbox", { name: "GitHub account override" }), { target: { value: "acme-bot" } });
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ githubUser: "acme-bot" }));
	});

	it("shows a canonical repo URL save error inline next to the field", async () => {
		setup(true, vi.fn().mockRejectedValue(new Error("canonical repository must be on the same host as this project's origin (github.com)")));
		await userEvent.click(screen.getByRole("button", { name: "Edit Canonical repo URL" }));
		fireEvent.change(screen.getByRole("textbox", { name: "Canonical repo URL" }), { target: { value: "https://gitlab.com/o/r" } });
		await userEvent.keyboard("{Enter}");
		const alerts = await screen.findAllByRole("alert");
		expect(alerts.some((a) => a.textContent?.includes("same host as this project's origin"))).toBe(true);
	});

	it("hides the override field when the capability is off", () => {
		setup(false);
		expect(screen.queryByRole("button", { name: "Edit GitHub account override" })).toBeNull();
	});
});
