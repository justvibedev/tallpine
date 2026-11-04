import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { useSkillsOfferStore } from "../stores/skills-offer-store";
import { ProjectSkillsSettings } from "./ProjectSkillsSettings";
import { RestartOrchestratorButton } from "./RestartOrchestratorButton";
import { SkillsOfferDialogHost } from "./SkillsOfferDialog";

const { getMock, postMock, restartMock, navigateMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn(), restartMock: vi.fn(), navigateMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, POST: postMock }, apiErrorMessage: (e: { message?: string } | undefined) => e?.message ?? "request failed" }));
vi.mock("../lib/restart-orchestrator", () => ({ restartProjectOrchestrator: restartMock }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));

const SHA = "abcdef1234567890abcdef1234567890abcdef12";
const offer = { id: "claude-md", harness: "claude-code", items: [{ path: "CLAUDE.md", kind: "file", content: "@AGENTS.md\n" }] };
const skillsOffer = { id: "claude-skills", harness: "claude-code", items: [{ path: ".claude/skills/qa", kind: "link", target: "../../.codex/skills/qa" }] };
let skills: Record<string, unknown>;
const body = (over: Record<string, unknown> = {}) => ({
	projectId: "p", workspace: "/repo", agentsMd: true, claudeMd: false, harnesses: ["claude-code"], instructions: [], skills: [], collisions: [],
	offersEnabled: true, pendingCommit: [], offers: [offer], commitTarget: { remote: "origin", branch: "main", needsPush: true }, ...over,
});
type Routes = Record<string, (init: { body?: unknown; params: { path: Record<string, string> } }) => unknown>;
let routes: Routes;

beforeEach(() => {
	skills = body();
	getMock.mockReset().mockImplementation(async () => ({ data: skills }));
	restartMock.mockReset().mockResolvedValue(undefined);
	routes = {
		"/api/v1/projects/{id}/skills/offers/{offerId}/apply": (init) => {
			const path = init.params.path.offerId === "claude-md" ? "CLAUDE.md" : ".claude/skills/qa";
			return { data: { created: [{ path, mode: path === "CLAUDE.md" ? "file" : "symlink" }] } };
		},
		"/api/v1/projects/{id}/skills/commit": () => ({ data: { sha: SHA, remote: "origin", branch: "main", needsPush: true, pushed: true, localUpdated: true } }),
	};
	postMock.mockReset().mockImplementation(async (path: string, init: { body?: unknown; params: { path: Record<string, string> } }) => routes[path](init));
	useSkillsOfferStore.setState({ shown: new Set(), active: new Set(), reviewing: null });
});

const client = () => new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
const settings = () => render(<QueryClientProvider client={client()}><ProjectSkillsSettings projectId="p" /></QueryClientProvider>);
const posted = (path: string) => postMock.mock.calls.filter((c) => c[0] === path);

it("creates, commits only the created paths, pushes to the session base, then offers a restart", async () => {
	skills = body({ offers: [offer, skillsOffer] });
	settings();
	const button = await screen.findByRole("button", { name: "Create, commit and push to origin/main" });
	expect(screen.getByText("Commits only: CLAUDE.md, .claude/skills/qa")).toBeInTheDocument();
	expect(postMock).not.toHaveBeenCalled();
	await userEvent.click(button);
	expect(await screen.findByText("Committed abcdef12 and pushed to origin/main.")).toBeInTheDocument();
	expect(posted("/api/v1/projects/{id}/skills/offers/{offerId}/apply")).toHaveLength(2);
	const commits = posted("/api/v1/projects/{id}/skills/commit");
	expect(commits).toHaveLength(1);
	expect(commits[0][1].body).toEqual({ paths: ["CLAUDE.md", ".claude/skills/qa"] });
	expect(screen.getByText(/New workers pick the files up automatically/)).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Restart orchestrator now" }));
	expect(restartMock).toHaveBeenCalledOnce();
	expect(restartMock.mock.calls[0][0]).toMatchObject({ projectId: "p", navigate: navigateMock });
});

it("keeps the commit when the push is refused and offers to push it as a branch for a PR", async () => {
	routes["/api/v1/projects/{id}/skills/commit"] = () => ({ data: { sha: SHA, remote: "origin", branch: "main", needsPush: true, pushed: false, pushError: "git push: protected branch hook declined", localUpdated: false, sideBranch: "vo/agent-files-abcdef12" } });
	routes["/api/v1/projects/{id}/skills/push-branch"] = () => ({ data: { remote: "origin", branch: "vo/agent-files-abcdef12" } });
	settings();
	await userEvent.click(await screen.findByRole("button", { name: "Create, commit and push to origin/main" }));
	expect(await screen.findByRole("alert")).toHaveTextContent("Pushing to origin/main was refused: git push: protected branch hook declined");
	expect(screen.getByText("The commit abcdef12 is kept locally on vo/agent-files-abcdef12.")).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Restart orchestrator now" })).not.toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Push as a branch for a PR" }));
	expect(await screen.findByRole("status")).toHaveTextContent("Pushed to origin/vo/agent-files-abcdef12. Open a pull request into main");
	expect(posted("/api/v1/projects/{id}/skills/push-branch")[0][1].body).toEqual({ sha: SHA });
});

it("names the side branch that keeps the commit when the local default branch could not take it", async () => {
	routes["/api/v1/projects/{id}/skills/commit"] = () => ({ data: { sha: SHA, remote: "origin", branch: "main", needsPush: true, pushed: false, pushError: "rejected", localUpdated: false, sideBranch: "vo/agent-files-abcdef12" } });
	settings();
	await userEvent.click(await screen.findByRole("button", { name: "Create, commit and push to origin/main" }));
	expect(await screen.findByText("The commit abcdef12 is kept locally on vo/agent-files-abcdef12.")).toBeInTheDocument();
});

it("never commits when creating a file fails", async () => {
	routes["/api/v1/projects/{id}/skills/offers/{offerId}/apply"] = () => ({ error: { message: "disk full" } });
	settings();
	await userEvent.click(await screen.findByRole("button", { name: "Create, commit and push to origin/main" }));
	expect(await screen.findByRole("alert")).toHaveTextContent("disk full");
	expect(posted("/api/v1/projects/{id}/skills/commit")).toHaveLength(0);
});

it("commits without pushing when sessions start from the local branch", async () => {
	skills = body({ commitTarget: { remote: "origin", branch: "master", needsPush: false } });
	routes["/api/v1/projects/{id}/skills/commit"] = () => ({ data: { sha: SHA, remote: "origin", branch: "master", needsPush: false, pushed: false, localUpdated: true } });
	settings();
	await userEvent.click(await screen.findByRole("button", { name: "Create and commit to master" }));
	expect(await screen.findByText("Committed abcdef12 to master.")).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Restart orchestrator now" })).toBeInTheDocument();
});

it("the review dialog commits exactly the listed uncommitted files when nothing is left to create", async () => {
	skills = body({ offers: [], pendingCommit: ["CLAUDE.md", ".claude/skills/qa"] });
	const commit = routes["/api/v1/projects/{id}/skills/commit"];
	// The commit empties the pending list, so the refetch that follows it does too.
	routes["/api/v1/projects/{id}/skills/commit"] = (init) => { skills = body({ offers: [], pendingCommit: [] }); return commit(init); };
	render(<QueryClientProvider client={client()}><SkillsOfferDialogHost /></QueryClientProvider>);
	act(() => useSkillsOfferStore.getState().openReview("p"));
	const dialog = await screen.findByTestId("skills-offer-dialog");
	expect(within(dialog).queryByRole("button", { name: "Create all without committing" })).not.toBeInTheDocument();
	await userEvent.click(await within(dialog).findByRole("button", { name: "Commit and push to origin/main" }));
	await waitFor(() => expect(posted("/api/v1/projects/{id}/skills/commit")).toHaveLength(1));
	expect(posted("/api/v1/projects/{id}/skills/commit")[0][1].body).toEqual({ paths: ["CLAUDE.md", ".claude/skills/qa"] });
	expect(posted("/api/v1/projects/{id}/skills/offers/{offerId}/apply")).toHaveLength(0);
	// The result stays after the refetch empties the pending list.
	await waitFor(() => expect(getMock.mock.calls.length).toBeGreaterThan(1));
	expect(await within(dialog).findByText("Committed abcdef12 and pushed to origin/main.")).toBeInTheDocument();
});

it("the restart button replaces the orchestrator through the board's restart path", async () => {
	render(<QueryClientProvider client={client()}><RestartOrchestratorButton projectId="p" /></QueryClientProvider>);
	await userEvent.click(screen.getByRole("button", { name: "Restart orchestrator now" }));
	expect(restartMock).toHaveBeenCalledWith(expect.objectContaining({ projectId: "p", navigate: navigateMock }));
});
