import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProjectSettingsEditor, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { fetchGitHubAccounts } from "../lib/github-accounts";
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

vi.mock("../lib/github-accounts", async (orig) => ({
	...(await orig<object>()),
	fetchGitHubAccounts: vi.fn().mockResolvedValue({ accounts: [{ login: "justvibedev", canPush: "yes" }, { login: "example-org", canPush: "yes" }, { login: "acme-bot", canPush: "no" }], repo: "acme/app", autoPick: "justvibedev" }),
}));

function setup(githubUser: boolean, save = vi.fn().mockResolvedValue({}), section: "general" | "repository" = "general", pinned = "", projectId: string | null = "p") {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><TooltipProvider>
		<ProjectSettingsEditor initialValues={{ ...initialValues, githubUser: pinned }} section={section} repository="git@bitbucket.org:acme/app.git" githubProjectId={projectId ?? undefined}
			capabilities={{ workflow: true, sessionPrefix: true, githubUser, localFields: section === "repository", intake: false, reviewer: true, requiredAgents: true }}
			details={[{ label: "GitHub account", value: "octo-bot" }]} modelScope={() => ""} renderAgent={() => null} save={save} onSaveState={vi.fn()} />
	</TooltipProvider></QueryClientProvider>);
	return save;
}

describe("project GitHub account settings", () => {
	it("groups repository, GitHub access and worktree setup under their own headings", () => {
		setup(true, vi.fn(), "repository");
		const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
		expect(headings).toEqual(["Repository", "GitHub access", "Worktree setup"]);
	});

	it("shows the origin read-only", () => {
		setup(true, vi.fn(), "repository");
		expect(screen.getByText("Origin")).toBeInTheDocument();
		expect(screen.getByText("git@bitbucket.org:acme/app.git")).toBeInTheDocument();
	});

	it("picks a signed-in account and saves it", async () => {
		const save = setup(true);
		await userEvent.click(await screen.findByRole("button", { name: "GitHub account" }));
		expect(await screen.findByRole("menuitem", { name: "Auto-pick (now justvibedev)" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "acme-bot (cannot push here)" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "example-org" }));
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ githubUser: "example-org" }));
	});

	it("keeps a pinned login that is no longer signed in visible", async () => {
		setup(true, vi.fn().mockResolvedValue({}), "repository", "gone-user");
		await waitFor(() => expect(screen.getByRole("button", { name: "GitHub account" })).toHaveTextContent("gone-user (not signed in)"));
	});

	it("does not call a pin not signed in when gh could not list accounts", async () => {
		vi.mocked(fetchGitHubAccounts).mockResolvedValueOnce({ accounts: [], unavailable: true });
		setup(true, vi.fn().mockResolvedValue({}), "repository", "justvibedev");
		expect(await screen.findByText(/Could not list accounts/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "GitHub account" })).toHaveTextContent(/^justvibedev$/);
	});

	it("shows a pin stored in another letter case as the listed account", async () => {
		setup(true, vi.fn().mockResolvedValue({}), "repository", "JustVibeDev");
		await waitFor(() => expect(screen.getByRole("button", { name: "GitHub account" })).toHaveTextContent("justvibedev"));
		expect(screen.getByRole("button", { name: "GitHub account" })).not.toHaveTextContent("not signed in");
	});

	it("keeps a typed login for a project with no local account list (remote host)", async () => {
		const save = setup(true, vi.fn().mockResolvedValue({}), "repository", "", null);
		await userEvent.click(screen.getByRole("button", { name: "Edit GitHub account" }));
		fireEvent.change(screen.getByRole("textbox", { name: "GitHub account" }), { target: { value: "acme-bot" } });
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ githubUser: "acme-bot" }));
	});

	it("labels an empty moved repository URL as empty, not as a value", () => {
		setup(true);
		expect(screen.getByText("Moved repository URL")).toBeInTheDocument();
		expect(screen.getByText("Not set")).toBeInTheDocument();
		expect(screen.queryByText("https://github.com/owner/repo")).toBeNull();
	});

	it("shows a moved repository URL save error inline next to the field", async () => {
		setup(true, vi.fn().mockRejectedValue(new Error("canonical repository must be on the same host as this project's origin (github.com)")));
		await userEvent.click(screen.getByRole("button", { name: "Edit Moved repository URL" }));
		fireEvent.change(screen.getByRole("textbox", { name: "Moved repository URL" }), { target: { value: "https://gitlab.com/o/r" } });
		await userEvent.keyboard("{Enter}");
		const alerts = await screen.findAllByRole("alert");
		expect(alerts.some((a) => a.textContent?.includes("same host as this project's origin"))).toBe(true);
	});

	it("hides the GitHub access section when the capability is off", () => {
		setup(false);
		expect(screen.queryByText("GitHub access")).toBeNull();
	});
});
