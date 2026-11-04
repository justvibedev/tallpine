import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { ArtifactsView } from "./ArtifactsView";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	workspaces: [] as WorkspaceSummary[],
	agents: [] as Array<Record<string, unknown>>,
	navigate: vi.fn(),
	post: vi.fn(),
	get: vi.fn(),
	hideShellTopbar: true,
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => mocks.navigate }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	useWorkspaceQuery: () => ({ data: mocks.workspaces, isLoading: false, error: null }),
}));
vi.mock("../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: mocks.agents } }) }));
vi.mock("../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/api-client")>(),
	apiClient: { POST: mocks.post, GET: mocks.get },
}));
vi.mock("../lib/platform", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/platform")>(),
	hidesShellTopbar: () => mocks.hideShellTopbar,
}));

const codexReady = {
	id: "codex",
	label: "Codex",
	installation: { state: "installed", freshness: "fresh" },
	authentication: { state: "authorized", freshness: "fresh" },
	effectiveReadiness: "ready",
	usageCount: 0,
	lastUsedAt: null,
};

function session(id: string, title: string, files: Array<{ name: string; updatedAt: string; kind?: "html" | "markdown" | "file" }>): WorkspaceSession {
	return {
		id,
		title,
		workspaceId: "ws",
		workspaceName: "ws",
		provider: "claude-code",
		kind: "worker",
		status: "idle",
		updatedAt: "2026-10-01T00:00:00Z",
		prs: [],
		outputType: files.length > 0 ? "artifact" : "none",
		artifactFiles: files.map((file) => ({
			kind: file.kind ?? "markdown",
			name: file.name,
			path: file.name,
			size: 1,
			updatedAt: file.updatedAt,
			...(file.kind === "html" ? { previewUrl: `http://preview/${file.name}` } : {}),
		})),
	} as WorkspaceSession;
}

const withArtifacts: WorkspaceSummary[] = [
	{
		id: "ao",
		name: "tallpine",
		path: "/code/tallpine",
		sessions: [
			session("tallpine-1", "Design the Artifacts entry point", [{ name: "artifacts-user-flow.html", updatedAt: "2026-10-11T10:00:00Z", kind: "html" }]),
			session("tallpine-2", "Review pricing", [{ name: "pricing.md", updatedAt: "2026-10-10T10:00:00Z" }]),
		],
	},
	{
		id: STANDALONE_WORKSPACE_ID,
		name: "Standalone",
		path: "",
		sessions: [session("standalone-1", "Compare caching options", [{ name: "caching-options.md", updatedAt: "2026-10-08T10:00:00Z" }])],
	},
];

const withoutArtifacts: WorkspaceSummary[] = [
	{ id: "ao", name: "tallpine", path: "/code/tallpine", sessions: [] },
	{ id: "px0", name: "px0", path: "/code/px0", sessions: [] },
	{ id: STANDALONE_WORKSPACE_ID, name: "Standalone", path: "", sessions: [] },
];

function renderView() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ArtifactsView />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("ArtifactsView", () => {
	beforeEach(() => {
		mocks.workspaces = withArtifacts;
		mocks.agents = [codexReady];
		mocks.hideShellTopbar = true;
		mocks.navigate.mockReset();
		mocks.post.mockReset();
		mocks.get.mockReset();
		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().clearGlobalToast();
	});

	it("lists artifacts grouped by project, newest group first, with counts", () => {
		renderView();

		expect(screen.getByRole("heading", { level: 1, name: "Artifacts" })).not.toHaveClass("sr-only");
		const sections = screen.getAllByRole("region");
		expect(sections.map((section) => section.getAttribute("aria-label"))).toEqual(["tallpine", "Scratchpad"]);
		const project = within(sections[0]);
		expect(project.getByRole("heading", { level: 2 })).toHaveTextContent("tallpine2");
		expect(project.getAllByRole("listitem").map((row) => row.textContent)).toEqual([
			expect.stringContaining("artifacts-user-flow.html"),
			expect.stringContaining("pricing.md"),
		]);
		expect(project.getByText("Design the Artifacts entry point")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /team/i })).not.toBeInTheDocument();
	});

	it("searches by file name and session title, then clears filters", async () => {
		const user = userEvent.setup();
		renderView();

		await user.type(screen.getByRole("searchbox", { name: "Search artifacts" }), "pricing");
		expect(screen.getByText("pricing.md")).toBeInTheDocument();
		expect(screen.queryByText("artifacts-user-flow.html")).not.toBeInTheDocument();
		expect(screen.queryByRole("region", { name: "Scratchpad" })).not.toBeInTheDocument();

		await user.clear(screen.getByRole("searchbox", { name: "Search artifacts" }));
		await user.type(screen.getByRole("searchbox", { name: "Search artifacts" }), "no such thing");
		expect(screen.getByText("No matches")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Clear filters" }));
		expect(screen.getByRole("searchbox", { name: "Search artifacts" })).toHaveValue("");
		expect(screen.getAllByRole("region")).toHaveLength(2);
	});

	it("filters to one project's group", async () => {
		const user = userEvent.setup();
		renderView();

		await user.click(screen.getByRole("button", { name: "Filter by project" }));
		await user.click(await screen.findByRole("menuitem", { name: /Scratchpad/ }));

		expect(screen.getAllByRole("region").map((section) => section.getAttribute("aria-label"))).toEqual(["Scratchpad"]);
	});

	it("opens the source session and hands it the clicked artifact", async () => {
		const user = userEvent.setup();
		renderView();

		await user.click(screen.getByRole("button", { name: /artifacts-user-flow\.html/ }));

		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "ao", sessionId: "tallpine-1" } });
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({
			sessionId: "tallpine-1",
			path: "artifacts-user-flow.html",
			source: "artifact",
		});
	});

	it("opens a Scratchpad artifact on the standalone session route", async () => {
		const user = userEvent.setup();
		renderView();

		await user.click(screen.getByRole("button", { name: /caching-options\.md/ }));

		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "standalone-1" } });
	});

	it("offers New artifact beside the filters with the same Scratchpad and project menu", async () => {
		const user = userEvent.setup();
		mocks.post.mockResolvedValue({ data: { session: { id: "standalone-7" } } });
		renderView();

		await user.click(screen.getByRole("button", { name: "New artifact" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Scratchpad", "tallpine"]);
		await user.click(screen.getByRole("menuitem", { name: "Scratchpad" }));

		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "standalone-7" } }));
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/sessions", {
			body: { kind: "worker", requestedOutput: "artifact", displayName: "New artifact", harness: "codex" },
		});
	});

	it("disables New artifact when no agent is ready", () => {
		mocks.agents = [{ ...codexReady, installation: { state: "not_installed", freshness: "fresh" } }];
		renderView();

		expect(screen.getByRole("button", { name: "New artifact" })).toBeDisabled();
	});

	it("shows one Create artifact action and no filters when nothing exists", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		renderView();

		expect(screen.getByRole("heading", { name: "Create your first artifact" })).toBeInTheDocument();
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		expect(screen.queryByRole("combobox")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Scratchpad", "tallpine", "px0"]);
	});

	it("starts a Scratchpad artifact session without a project id and opens it", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.post.mockResolvedValue({ data: { session: { id: "standalone-9" } } });
		renderView();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		await user.click(screen.getByRole("menuitem", { name: "Scratchpad" }));

		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "standalone-9" } }));
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/sessions", {
			body: { kind: "worker", requestedOutput: "artifact", displayName: "New artifact", harness: "codex" },
		});
	});

	it("starts a project artifact session with the project's worker agent", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.get.mockResolvedValue({ data: { status: "ok", project: { agent: "codex", config: { worker: { agent: "claude-code" } } } } });
		mocks.post.mockResolvedValue({ data: { session: { id: "px0-3" } } });
		renderView();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		await user.click(screen.getByRole("menuitem", { name: "px0" }));

		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "px0", sessionId: "px0-3" } }));
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/sessions", {
			body: { kind: "worker", requestedOutput: "artifact", displayName: "New artifact", projectId: "px0", harness: "claude-code" },
		});
	});

	it("shows the daemon error and stays on the screen when the start fails", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.post.mockResolvedValue({ error: { error: "conflict", code: "AGENT_NOT_READY", message: "Agent is not ready" } });
		renderView();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		await user.click(screen.getByRole("menuitem", { name: "Scratchpad" }));

		await waitFor(() => expect(useUiStore.getState().globalToast).toMatchObject({ title: "Agent is not ready" }));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(screen.getByRole("button", { name: "Create artifact" })).toBeEnabled();
	});

	it("points to agent settings when no agent is ready", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.agents = [{ ...codexReady, installation: { state: "not_installed", freshness: "fresh" } }];
		renderView();

		expect(screen.getByRole("heading", { name: "No agent is ready" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Create artifact" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Open agent settings" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "agents" });
	});

	describe("storage", () => {
		const scratch = { sessionId: "standalone-1", displayName: "Scratch run", terminated: true, updatedAt: "2026-01-01T00:00:00Z", sizeBytes: 4 * 1024, versionsBytes: 0, files: 1, overWarn: false, eligible: true, protected: false };

		const storage = {
			olderThanDays: 30, warnBytes: 1024 ** 3, totalBytes: 3 * 1024 ** 2, versionsBytes: 0, eligibleBytes: 2 * 1024 ** 2,
			sessions: [
				{ sessionId: "tallpine-9", projectId: "ao", displayName: "Old run", terminated: true, updatedAt: "2026-01-01T00:00:00Z", sizeBytes: 2 * 1024 ** 2, versionsBytes: 0, files: 2, overWarn: false, eligible: true, protected: false },
				{ sessionId: "tallpine-1", projectId: "ao", displayName: "Open run", terminated: false, updatedAt: "2026-10-01T00:00:00Z", sizeBytes: 1024 ** 2, versionsBytes: 0, files: 1, overWarn: false, eligible: false, protected: false },
			],
		};

		it("shows the total, a per-project size and one cleanup button for the finished bytes", async () => {
			mocks.get.mockResolvedValue({ data: storage });
			renderView();
			expect(await screen.findByText("3.0 MB on disk")).toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Clean up finished (2.0 MB)" })).toBeInTheDocument();
			expect(within(screen.getByRole("region", { name: "tallpine" })).getByText("3.0 MB")).toBeInTheDocument();
		});

		it("opens the cleanup with finished sessions preselected and sends nothing until confirmed", async () => {
			const user = userEvent.setup();
			mocks.get.mockResolvedValue({ data: storage });
			renderView();
			await user.click(await screen.findByRole("button", { name: "Clean up finished (2.0 MB)" }));
			expect(await screen.findByRole("checkbox", { name: "Old run" })).toBeChecked();
			expect(screen.getByRole("checkbox", { name: "Open run" })).not.toBeChecked();
			expect(mocks.post).not.toHaveBeenCalled();
		});

		it("hides the cleanup button when nothing is finished", async () => {
			mocks.get.mockResolvedValue({ data: { ...storage, eligibleBytes: 0 } });
			renderView();
			expect(await screen.findByText("3.0 MB on disk")).toBeInTheDocument();
			expect(screen.queryByRole("button", { name: /Clean up finished/ })).not.toBeInTheDocument();
		});

		it("sizes the scratchpad group from sessions without a project and cleans up only those", async () => {
			const user = userEvent.setup();
			mocks.get.mockResolvedValue({ data: { ...storage, sessions: [...storage.sessions, scratch] } });
			renderView();
			const pad = await screen.findByRole("region", { name: "Scratchpad" });
			expect(await within(pad).findByText("4.0 KB")).toBeInTheDocument();
			await user.click(within(pad).getByRole("button", { name: "Clean up" }));
			expect(await screen.findByRole("checkbox", { name: "Scratch run" })).toBeChecked();
			expect(screen.queryByRole("checkbox", { name: "Old run" })).not.toBeInTheDocument();
		});

		it("offers a project cleanup only when something in it is finished", async () => {
			mocks.get.mockResolvedValue({ data: { ...storage, sessions: [storage.sessions[1]] } });
			renderView();
			const project = await screen.findByRole("region", { name: "tallpine" });
			expect(await within(project).findByText("1.0 MB")).toBeInTheDocument();
			expect(within(project).queryByRole("button", { name: "Clean up" })).not.toBeInTheDocument();
		});
	});
});
