import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { ProjectSkillsSettings } from "./ProjectSkillsSettings";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, POST: postMock }, apiErrorMessage: () => "request failed" }));

const harness = (claude: string, codex: string) => [
	{ harness: "claude-code", status: claude }, { harness: "codex", status: codex }, { harness: "kiro", status: "unverified" },
];
const response = () => ({
	projectId: "p", workspace: "/repo", agentsMd: true, claudeMd: false, harnesses: ["claude-code"], offersEnabled: true, pendingCommit: [],
	instructions: [{ harness: "claude-code", status: "missing" }, { harness: "codex", status: "native" }],
	offers: [
		{ id: "claude-md", harness: "claude-code", items: [{ path: "CLAUDE.md", kind: "file", content: "@AGENTS.md\n" }] },
		{ id: "claude-skills", harness: "claude-code", items: [{ path: ".claude/skills/qa", kind: "link", target: "../../.codex/skills/qa" }] },
	],
	skills: [
		{ name: "qa", description: "Regression QA", scope: "repo", convention: "codex", path: "/repo/.codex/skills/qa", harnesses: harness("missing", "missing") },
		{ name: "cloudflare", description: "", scope: "user", convention: "agents", path: "/home/u/.agents/skills/cloudflare", harnesses: harness("missing", "native") },
	],
	collisions: [{ name: "qa", winner: "/repo/.codex/skills/qa", loser: "/home/u/.claude/skills/qa", reason: "x" }],
});

function renderPage() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}><ProjectSkillsSettings projectId="p" /></QueryClientProvider>);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: response() });
	postMock.mockReset().mockResolvedValue({ data: { created: [{ path: "CLAUDE.md", mode: "file" }] } });
});

it("lists project and account skills with what each harness sees", async () => {
	renderPage();
	const qa = (await screen.findByText("qa")).closest(".settings-row-bar") as HTMLElement;
	expect(within(qa).getByText("Regression QA")).toBeInTheDocument();
	expect(within(qa).getByText("/repo/.codex/skills/qa")).toBeInTheDocument();
	expect(within(qa).getByText("Claude Code: can't see it")).toBeInTheDocument();
	const cf = screen.getByText("cloudflare").closest(".settings-row-bar") as HTMLElement;
	expect(within(cf).getByText("Codex: sees it")).toBeInTheDocument();
	expect(screen.getByText("CLAUDE.md: can't see it")).toBeInTheDocument();
	expect(screen.getByText(/exists in several places/)).toBeInTheDocument();
});

it("shows what an offer will create and applies it only on click", async () => {
	renderPage();
	expect(await screen.findByText("Create CLAUDE.md that imports AGENTS.md")).toBeInTheDocument();
	expect(screen.getByText("qa: link .claude/skills/qa to .codex/skills/qa (same project, no copy)")).toBeInTheDocument();
	expect(postMock).not.toHaveBeenCalled();
	await userEvent.click(screen.getAllByRole("button", { name: "Create files only" })[0]);
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/offers/{offerId}/apply", { params: { path: { id: "p", offerId: "claude-md" } } }));
	expect(await screen.findByRole("status")).toHaveTextContent("Created, not committed yet.");
});

it("hides the offers section when nothing is missing", async () => {
	getMock.mockResolvedValue({ data: { ...response(), offers: [] } });
	renderPage();
	await screen.findByText("qa");
	expect(screen.queryByText("Make this project visible to agents")).not.toBeInTheDocument();
});

it("reports a load failure", async () => {
	getMock.mockResolvedValue({ error: { message: "boom" } });
	renderPage();
	expect(await screen.findByRole("alert")).toHaveTextContent("request failed");
});

it("reports applied-but-uncommitted files as pending, not as seen", async () => {
	getMock.mockResolvedValue({ data: { ...response(), offers: [], pendingCommit: ["CLAUDE.md"], instructions: [{ harness: "claude-code", status: "pending" }],
		skills: [{ name: "qa", description: "QA", scope: "repo", convention: "claude", path: "/repo/.claude/skills/qa", harnesses: harness("pending", "missing") }] } });
	renderPage();
	expect(await screen.findByText("CLAUDE.md: not committed yet")).toBeInTheDocument();
	expect(screen.getByText("Claude Code: not committed yet")).toBeInTheDocument();
	expect(screen.getByRole("status")).toHaveTextContent("Not committed yet, so sessions can't see them: CLAUDE.md");
});
