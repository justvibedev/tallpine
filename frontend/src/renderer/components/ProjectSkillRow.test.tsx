import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { ProjectSkillsSettings } from "./ProjectSkillsSettings";

const { getMock, postMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, POST: postMock, PUT: putMock }, apiErrorMessage: (e: { message?: string } | undefined) => e?.message ?? "request failed" }));

const h = [{ harness: "claude-code", status: "native" }, { harness: "codex", status: "native" }];
const skill = (over: Record<string, unknown>) => ({ description: "", scope: "repo", convention: "agents", harnesses: h, deselected: false, removable: false, ...over });
let skills: Record<string, unknown>;
let config: Record<string, unknown>;

beforeEach(() => {
	skills = {
		projectId: "p", workspace: "/repo", agentsMd: true, claudeMd: true, harnesses: ["claude-code"], offersEnabled: true, pendingCommit: [], offers: [], instructions: [], collisions: [],
		skills: [
			skill({ name: "keep", path: "/repo/.agents/skills/keep" }),
			skill({ name: "keep", convention: "claude", path: "/repo/.claude/skills/keep", removable: true }),
			skill({ name: "drop", path: "/repo/.agents/skills/drop", deselected: true }),
			skill({ name: "mine", scope: "user", path: "/home/u/.agents/skills/mine" }),
		],
	};
	config = { autoReview: true, skillsDeselected: ["drop"] };
	getMock.mockReset().mockImplementation(async (path: string) => path === "/api/v1/projects/{id}/skills" ? { data: skills } : { data: { status: "ok", project: { id: "p", name: "Example", config } } });
	putMock.mockReset().mockResolvedValue({ data: { status: "ok" } });
	postMock.mockReset().mockResolvedValue({ data: { localOnly: false } });
});

const page = () => render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}><ProjectSkillsSettings projectId="p" /></QueryClientProvider>);

it("shows a checkbox per source skill, checked unless deselected, and none for account skills", async () => {
	page();
	expect(await screen.findByRole("checkbox", { name: "Make keep visible to other agents" })).toBeChecked();
	expect(screen.getByRole("checkbox", { name: "Make drop visible to other agents" })).not.toBeChecked();
	expect(screen.queryByRole("checkbox", { name: /mine/ })).not.toBeInTheDocument();
	expect(screen.getAllByRole("checkbox")).toHaveLength(2);
});

it("persists a deselected skill in the project config without dropping other settings", async () => {
	page();
	await userEvent.click(await screen.findByRole("checkbox", { name: "Make keep visible to other agents" }));
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body.config).toEqual({ autoReview: true, skillsDeselected: ["drop", "keep"] });
});

it("reselecting removes the name from the config", async () => {
	page();
	await userEvent.click(await screen.findByRole("checkbox", { name: "Make drop visible to other agents" }));
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body.config).toEqual({ autoReview: true });
});

it("Remove link deletes the link by its checkout-relative path, then stops offering the skill", async () => {
	page();
	await userEvent.click(await screen.findByRole("button", { name: "Remove link" }));
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/remove", { params: { path: { id: "p" } }, body: { path: ".claude/skills/keep" } }));
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body.config.skillsDeselected).toEqual(["drop", "keep"]);
	// Only the one removable row has the button.
	expect(screen.getAllByRole("button", { name: "Remove link" })).toHaveLength(1);
});

it("a failed removal shows the error and does not deselect the skill", async () => {
	postMock.mockResolvedValue({ error: { message: "not a link or copy created by the bridge" } });
	page();
	await userEvent.click(await screen.findByRole("button", { name: "Remove link" }));
	expect(await screen.findByRole("alert")).toHaveTextContent("not a link or copy created by the bridge");
	expect(putMock).not.toHaveBeenCalled();
});

it("says a removal is local only when the link is still on the session base branch", async () => {
	postMock.mockResolvedValue({ data: { localOnly: true, branch: "main" } });
	page();
	await userEvent.click(await screen.findByRole("button", { name: "Remove link" }));
	expect(await screen.findByText(/Removed keep in your project folder only\. It is still on main/)).toBeInTheDocument();
});

it("shows no local-only warning when the removal is complete", async () => {
	page();
	await userEvent.click(await screen.findByRole("button", { name: "Remove link" }));
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(screen.queryByText(/project folder only/)).not.toBeInTheDocument();
});
