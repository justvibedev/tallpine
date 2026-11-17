import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";
import { projectSkillsQueryKey } from "../../lib/skills-offer";
import { InstructionsAndSkillsLsp } from "./InstructionsAndSkillsLsp";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));
vi.mock("../../lib/api-client", () => ({ apiClient: { GET: getMock, POST: postMock }, apiErrorMessage: (e: { message?: string } | undefined) => e?.message ?? "request failed" }));

type Lsp = components["schemas"]["ControllersProjectLSP"];
type Rec = Lsp["recommendations"][number];
const rec = (over: Partial<Rec> = {}): Rec => ({ language: "go", files: 40, pluginId: "gopls-lsp@claude-plugins-official", pluginInstalled: false, pluginInstall: "claude plugin install gopls-lsp@claude-plugins-official", binary: "gopls", binaryInstalled: false, binaryProblem: "", binaryInstall: "go install golang.org/x/tools/gopls@latest", binaryInstallable: true, toolchainInstall: "", toolchainInstallable: false, roles: [], ...over });
const job = (state: "running" | "succeeded" | "failed", over: Record<string, unknown> = {}) => ({ language: "go", target: "plugin", state, command: "claude plugin install gopls-lsp@claude-plugins-official --scope user", output: "", startedAt: "2026-11-17T10:00:00Z", ...over });

function ui(recs: Rec[], projectId: string | undefined = "p", values: Record<string, unknown> = {}) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	render(<QueryClientProvider client={client}><InstructionsAndSkillsLsp values={values as never} lsp={{ recommendations: recs, mismatches: [] }} onChange={vi.fn()} projectId={projectId} /></QueryClientProvider>);
	return { invalidate };
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: {} });
	postMock.mockReset();
});

describe("LSP install buttons", () => {
	it("starts the install by ids only, shows progress, then re-reads the skills status", async () => {
		postMock.mockResolvedValue({ data: { job: job("running") } });
		const { invalidate } = ui([rec()]);
		await userEvent.click(screen.getByRole("button", { name: "Install: Install the plugin" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/lsp/install", { params: { path: { id: "p" } }, body: { language: "go", target: "plugin" } });
		expect(await screen.findByText(/Installing: claude plugin install/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Install: Install gopls" })).toBeDisabled();
		getMock.mockResolvedValue({ data: { job: job("succeeded", { output: "done" }) } });
		expect(await screen.findByText("Installed.", undefined, { timeout: 3000 })).toBeInTheDocument();
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: projectSkillsQueryKey("p") }));
		expect(screen.getByText("claude plugin install gopls-lsp@claude-plugins-official")).toBeInTheDocument();
	});

	it("shows why an install failed with the output tail", async () => {
		getMock.mockResolvedValue({ data: { job: job("failed", { target: "binary", error: "gopls needs one of go, brew, and none is installed", output: "$ go install\nboom" }) } });
		ui([rec()]);
		expect(await screen.findByRole("alert")).toHaveTextContent("Install failed: gopls needs one of go, brew");
		expect(screen.getByLabelText("Install output")).toHaveTextContent("boom");
	});

	it("shows a refused start (another install running)", async () => {
		postMock.mockResolvedValue({ error: { message: "an install is already running for this project" } });
		ui([rec()]);
		await userEvent.click(screen.getByRole("button", { name: "Install: Install gopls" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("an install is already running");
	});

	it("offers a reinstall for a TypeScript install without tsserver, not for a missing go", () => {
		const on = (id: string) => ({ claudePlugins: { worker: { [id]: true } } });
		const ts = rec({ language: "typescript", pluginId: "typescript-lsp@x", pluginInstalled: true, binary: "typescript-language-server", binaryInstalled: true, binaryProblem: "no-tsserver" });
		ui([ts], "p", on(ts.pluginId));
		expect(screen.getByTestId("lsp-enabled-missing-typescript-lsp@x")).toHaveTextContent("but typescript-language-server cannot work here");
		expect(screen.getByRole("button", { name: /^Install: Install typescript-language-server/ })).toBeInTheDocument();
		cleanup();
		const go = rec({ pluginInstalled: true, binaryInstalled: true, binaryProblem: "no-go" });
		ui([go], "p", on(go.pluginId));
		expect(screen.getByTestId("lsp-binary-problem-go")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Install:/ })).toBeNull();
	});

	it("shows the Go install command with Copy and Install for a no-go row, and starts the go target", async () => {
		postMock.mockResolvedValue({ data: { job: job("running", { target: "go" }) } });
		ui([rec({ pluginInstalled: true, binaryInstalled: true, binaryProblem: "no-go", toolchainInstall: "brew install go", toolchainInstallable: true })]);
		expect(screen.getByRole("button", { name: "Copy: Install Go" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Install: Install Go" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/lsp/install", { params: { path: { id: "p" } }, body: { language: "go", target: "go" } });
	});

	it("hides Enable for workers once worker and reviewer are already on", () => {
		const r = rec({ pluginInstalled: true, binaryInstalled: true });
		ui([r], "p", { claudePlugins: { worker: { [r.pluginId]: true }, reviewer: { [r.pluginId]: true }, orchestrator: { [r.pluginId]: true } } });
		expect(screen.queryByRole("button", { name: "Enable for workers and reviewers" })).toBeNull();
		cleanup();
		ui([r]);
		expect(screen.getByRole("button", { name: "Enable for workers and reviewers" })).toBeInTheDocument();
	});

	it("offers only the copy line for a server Tallpine cannot install, and none without a project", () => {
		ui([rec({ language: "swift", pluginId: "swift-lsp@x", binary: "sourcekit-lsp", binaryInstallable: false, pluginInstalled: true })]);
		expect(screen.queryByRole("button", { name: /^Install:/ })).toBeNull();
		expect(screen.getByRole("button", { name: "Copy: Install sourcekit-lsp" })).toBeInTheDocument();
	});

	it("renders copy lines only, without a query client, when there is no project id", () => {
		render(<InstructionsAndSkillsLsp values={{} as never} lsp={{ recommendations: [rec()], mismatches: [] }} onChange={vi.fn()} />);
		expect(screen.queryByRole("button", { name: /^Install:/ })).toBeNull();
		expect(screen.getAllByRole("button", { name: /^Copy:/ })).toHaveLength(2);
	});

	const id = "gopls-lsp@claude-plugins-official";
	const on = (...roles: string[]) => ({ claudePlugins: Object.fromEntries(roles.map((role) => [role, { [id]: true }])) });
	const allBody = { params: { path: { id: "p" } }, body: { language: "go", target: "all" } };

	it("switching a missing plugin on installs what it lacks; switching off or nothing missing installs nothing", async () => {
		postMock.mockResolvedValue({ data: { job: job("running", { target: "all" }) } });
		ui([rec()]);
		await userEvent.click(screen.getByRole("switch", { name: "gopls-lsp@claude-plugins-official for Worker" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/lsp/install", allBody);
		cleanup();
		postMock.mockClear();
		ui([rec()], "p", on("worker"));
		await userEvent.click(screen.getByRole("switch", { name: "gopls-lsp@claude-plugins-official for Worker" }));
		cleanup();
		ui([rec({ pluginInstalled: true, binaryInstalled: true })]);
		await userEvent.click(screen.getByRole("button", { name: "Enable for workers and reviewers" }));
		cleanup();
		ui([rec({ language: "swift", pluginId: "swift-lsp@x", binary: "sourcekit-lsp", pluginInstalled: true, binaryInstallable: false })]);
		await userEvent.click(screen.getByRole("button", { name: "Enable for workers and reviewers" }));
		expect(postMock).not.toHaveBeenCalled();
	});

	it("Enable for workers and reviewers starts the install too, but not while one runs", async () => {
		postMock.mockResolvedValue({ data: { job: job("running", { target: "all" }) } });
		ui([rec()]);
		await userEvent.click(screen.getByRole("button", { name: "Enable for workers and reviewers" }));
		expect(postMock).toHaveBeenCalledTimes(1);
		expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/lsp/install", allBody);
		await screen.findByText(/Installing:/);
		await userEvent.click(screen.getByRole("switch", { name: "gopls-lsp@claude-plugins-official for Orchestrator" }));
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it("warns about a plugin that is on but not installed, with an Install button", async () => {
		postMock.mockResolvedValue({ data: { job: job("running", { target: "all" }) } });
		ui([rec({ pluginInstalled: true })], "p", on("worker", "reviewer"));
		const row = screen.getByTestId("lsp-enabled-missing-gopls-lsp@claude-plugins-official");
		expect(row).toHaveTextContent("On for Worker, Reviewer, but gopls is not installed");
		await userEvent.click(screen.getByRole("button", { name: "Install: gopls-lsp@claude-plugins-official" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/lsp/install", allBody);
		cleanup();
		ui([rec({ pluginInstalled: true })]);
		expect(screen.queryByTestId("lsp-enabled-missing-gopls-lsp@claude-plugins-official")).toBeNull();
	});
});
