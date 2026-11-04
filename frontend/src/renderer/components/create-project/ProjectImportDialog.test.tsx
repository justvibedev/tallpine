import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ProjectImportDialog } from "./ProjectImportDialog";
import type { ImportValidationResult, ProjectGitHubRepository } from "./create-project-types";

vi.mock("../../lib/bridge", () => ({
	tallpineBridge: {
		app: {
			getCachedGitHubOwners: vi.fn().mockResolvedValue([{ login: "username", avatarUrl: "" }]),
			refreshGitHubOwners: vi.fn().mockResolvedValue([{ login: "username", avatarUrl: "" }]),
			checkGitHubRepositoryAvailability: vi.fn().mockResolvedValue({ available: true }),
		},
	},
}));

// A committed-or-not repo with no remote: init/commit are required, a remote never is.
const validation = {
	importKind: "project",
	isValid: true,
	blockingErrors: [],
	root: { repoPath: "/repo/plain", isRepo: false, hasCommit: false, hasOrigin: false, isEmptyFolder: false, needsGitInit: true, requiredActions: ["git_init", "git_commit"], blockingErrors: [] },
	nextStep: "prepare_git",
} as ImportValidationResult;

let approved: string[] = [];

function Harness() {
	const [actions, setActions] = useState<string[]>(["git_init", "git_commit"]);
	const [repo, setRepo] = useState<ProjectGitHubRepository | null>(null);
	approved = actions;
	return (
		<ProjectImportDialog
			approvedActions={actions} disabled={false} events={[]} githubRepository={repo} isPreparingGit={false}
			onBack={vi.fn()} onChangeApprovedActions={setActions} onChangeFolder={vi.fn()} onChangeGitHubRepository={setRepo}
			onChangeRemote={vi.fn()} onContinue={vi.fn()} onContinueProject={vi.fn()} onOpenChange={vi.fn()} onTryWorkspace={vi.fn()}
			open remoteUrl="" shake={false} step="prepare_git" validation={validation}
		/>
	);
}

describe("ProjectImportDialog optional remote", () => {
	beforeEach(() => { approved = []; });

	it("does not require a remote: no remote form and Continue is enabled", () => {
		render(<Harness />);
		expect(screen.queryByLabelText("Owner")).toBeNull();
		expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
		expect(approved).toEqual(["git_init", "git_commit"]);
	});

	it("opting in approves the remote action and shows the repository form; opting out removes it", async () => {
		const user = userEvent.setup();
		render(<Harness />);
		const toggle = screen.getByRole("switch", { name: "Also create a remote repository (optional)" });
		await user.click(toggle);
		expect(approved).toEqual(["git_init", "git_commit", "create_remote_repository"]);
		expect(await screen.findByLabelText("Owner")).toBeInTheDocument();
		await user.click(toggle);
		expect(approved).toEqual(["git_init", "git_commit"]);
		expect(screen.queryByLabelText("Owner")).toBeNull();
	});
});
