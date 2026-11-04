import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import { vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { useAgentReadinessQuery } from "../../hooks/useAgentReadinessQuery";
import type { TerminalSessionState } from "../../hooks/useTerminalSession";
import { TooltipProvider } from "../ui/tooltip";
import { HarnessSettingsSection } from "./HarnessSettingsSection";

// Cloud sign-in state for the cloud login rows. Signed out by default, which
// leaves every row on its local-only controls.
const cloudMocks = vi.hoisted(() => ({
	cloudEnabled: false,
	org: undefined as { id: string } | undefined,
	connections: [] as Array<{ provider: string; label?: string; validationState: string }>,
}));





// A connected remote host, off by default; the remote diagnostics test turns it on.
const hostMocks = vi.hoisted(() => {
	const remoteGET = vi.fn();
	const remotePOST = vi.fn();
	// One object, like the real per-host client cache: a fresh one each call
	// would change the component's client every render.
	return { connected: [] as string[], remoteGET, remotePOST, remote: { GET: remoteGET, POST: remotePOST } };
});

vi.mock("../../hooks/useHostConnection", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useHostConnection")>()),
	useConnectedHosts: () => hostMocks.connected,
}));

vi.mock("../../lib/host-clients", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../lib/host-clients")>();
	return {
		...actual,
		clientForHost: (hostId: string) => (hostId === "remote-1" ? (hostMocks.remote as never) : actual.clientForHost(hostId)),
		clientForSessionHost: (hostId?: string) => (hostId === "remote-1" ? (hostMocks.remote as never) : actual.clientForSessionHost(hostId)),
	};
});


const { terminalFocusRequested, terminalStateCallback, terminalMuxFactory } = vi.hoisted(() => ({
	terminalFocusRequested: { value: false },
	terminalMuxFactory: { value: undefined as undefined | (() => unknown) },
	terminalStateCallback: { value: undefined as ((state: TerminalSessionState) => void) | undefined },
}));


// Recheck a login without the production 1.5s wait between attempts; the
// attempt count and every assertion stay as in production.
vi.mock("./harness/local-harness/local-harness-types", async (importOriginal) => ({
	...(await importOriginal<typeof import("./harness/local-harness/local-harness-types")>()),
	AUTH_VERIFY_RETRY_MS: 1,
}));

vi.mock("../TerminalPane", () => ({
	TerminalPane: ({ focusRequested, onTerminalStateChange, createMux }: { focusRequested?: boolean; onTerminalStateChange?: (state: TerminalSessionState) => void; createMux?: () => unknown }) => {
		terminalMuxFactory.value = createMux;
		terminalFocusRequested.value = focusRequested === true;
		terminalStateCallback.value = onTerminalStateChange;
		return (
			<div data-testid="inline-terminal-body">
				<button onClick={() => onTerminalStateChange?.("exited")}>Complete login terminal</button>
			</div>
		);
	},
}));


export function catalogWithInstalled(...installed: string[]) {
	return {
		agents: [
			{ id: "claude-code", label: "Claude Code" },
			{ id: "codex", label: "Codex" },
			{ id: "cursor", label: "Cursor" },
			{ id: "goose", label: "Goose" },
			{ id: "codewhale", label: "Codewhale" },
			{ id: "deepseek-harness", label: "DeepSeek" },
		].map((agent) => ({
			...agent,
			installation: { state: installed.includes(agent.id) ? "installed" : "not_installed", freshness: "fresh", reason: "", reasonCode: "", attemptedAt: null, checkedAt: null },
			authentication: { state: "unknown", freshness: "fresh", reason: "", reasonCode: "", attemptedAt: null, checkedAt: null },
			effectiveReadiness: installed.includes(agent.id) ? "ready" : "not_ready",
			usageCount: 0,
		})),
	};
}


export const catalog = catalogWithInstalled("claude-code");
// Login workflows require a confirmed logged-out observation, not "unknown".
catalog.agents[0].authentication.state = "unauthorized";
catalog.agents[0].effectiveReadiness = "not_ready";


export const plans = {
	agents: [
		{
			agentId: "claude-code", available: true, automatic: true, method: "homebrew",
			command: "brew install --cask claude-code", documentationUrl: "https://code.claude.com/docs/en/installation",
			methods: [{ id: "homebrew", label: "Homebrew", available: true, recommended: true, command: "brew install --cask claude-code", reinstallAvailable: true, reinstallCommand: "brew reinstall --cask claude-code" }],
		},
		{
			agentId: "codex", available: true, automatic: true, method: "homebrew",
			command: "brew install --cask codex", documentationUrl: "https://github.com/openai/codex",
			methods: [
				{ id: "homebrew", label: "Homebrew", available: true, recommended: true, command: "brew install --cask codex", reinstallAvailable: true, reinstallCommand: "brew reinstall --cask codex" },
				{ id: "npm", label: "npm", available: true, recommended: false, command: "npm install -g @openai/codex", expectedDestination: "/Users/test/.npm/bin", reinstallAvailable: true, reinstallCommand: "npm install -g @openai/codex --force" },
			],
		},
		{
			agentId: "aider", available: true, automatic: true, method: "pipx",
			command: "pipx install aider-chat", documentationUrl: "https://aider.chat/docs/install.html",
			methods: [{ id: "pipx", label: "pipx", available: true, recommended: true, command: "pipx install aider-chat", reinstallAvailable: true, reinstallCommand: "pipx reinstall aider-chat" }],
		},
		{
			agentId: "cursor", available: true, automatic: true, method: "official-installer",
			command: "bash <downloaded from https://cursor.com/install>", documentationUrl: "https://cursor.com/cli",
			methods: [{ id: "official-installer", label: "Official installer", available: true, recommended: true, command: "bash <downloaded from https://cursor.com/install>", reinstallAvailable: false, reinstallReason: "No headless reinstall" }],
		},
		{
			agentId: "goose", available: true, automatic: true, method: "official-installer",
			command: "pwsh.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File <downloaded from https://raw.githubusercontent.com/aaif-goose/goose/main/download_cli.ps1>",
			documentationUrl: "https://goose-docs.ai/docs/getting-started/installation/",
			methods: [{ id: "official-installer", label: "Official installer", available: true, recommended: true, command: "pwsh.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File <downloaded from https://raw.githubusercontent.com/aaif-goose/goose/main/download_cli.ps1>", reinstallAvailable: false, reinstallReason: "No headless reinstall" }],
		},
		{
			agentId: "codewhale", available: false, automatic: false, method: "manual",
			reason: "Tallpine does not automatically install Codewhale.", documentationUrl: "https://github.com/Hmbown/Codewhale",
			methods: [{ id: "manual", label: "Manual", available: false, recommended: true, reason: "Tallpine does not automatically install Codewhale.", reinstallAvailable: false }],
		},
	],
};


export function ReadinessSelector({ agentId }: { agentId: string }) {
	const readiness = useAgentReadinessQuery();
	return (
		<div data-testid="originating-selector">
			{readiness.data?.agents.find((agent) => agent.id === agentId)?.effectiveReadiness}
		</div>
	);
}

/** The list row for a harness. The first render shows bare labels before the rows mount; wait for the row itself. */

export async function findListRow(agentId: string): Promise<HTMLElement> {
	return waitFor(() => {
		const row = document.querySelector<HTMLElement>(`button[data-agent="${agentId}"]`);
		if (!row) throw new Error(`harness row ${agentId} has not rendered`);
		return row;
	});
}

/** Opens a harness from the list (unless its page is already open) and returns its page. */

export async function findAgentRow(agentId: string): Promise<HTMLElement> {
	const page = document.querySelector<HTMLElement>(`div[data-agent="${agentId}"]`);
	if (page) return page;
	if (document.querySelector("div[data-agent]")) backToList();
	fireEvent.click(await findListRow(agentId));
	return waitFor(() => {
		const detail = document.querySelector<HTMLElement>(`div[data-agent="${agentId}"]`);
		if (!detail) throw new Error(`harness page ${agentId} has not opened`);
		return detail;
	});
}

/** Returns from a harness page to the list. */

export function backToList() {
	fireEvent.click(screen.getByRole("button", { name: "All harnesses" }));
}


export function renderSection(focusAgentId?: string, selectorAgentId?: string, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	const view = render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				{selectorAgentId ? <ReadinessSelector agentId={selectorAgentId} /> : null}
				<HarnessSettingsSection focusAgentId={focusAgentId} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return { ...view, client };
}


export function readyCatalog() {
	const readiness = catalogWithInstalled("claude-code", "codex", "cursor");
	for (const agent of readiness.agents) agent.authentication.state = "authorized";
	return readiness;
}


export function mockInstalledOperations(savedMethod?: string, readiness = readyCatalog(), codexInUse?: { liveSessions: number; sessionIds: string[]; level: "safe" | "warn" | "blocked" | "idle" }) {
	const managedPlans = { agents: plans.agents.map((plan) => ({
		...plan,
		methods: plan.methods.map((method) => ({
			...method,
			updateAvailable: method.id !== "official-installer",
			updateReason: method.id === "official-installer" ? "Vendor update is not supported." : "",
		})),
	})) };
	let operationJobs = savedMethod ? [{ target: "codex", method: savedMethod, status: "succeeded", startedAt: "2026-09-24T12:00:00Z" }] : [];
	vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
		if (path === "/api/v1/agents/installers") return { data: managedPlans } as never;
		if (path === "/api/v1/agents/install-jobs") return { data: { jobs: operationJobs } } as never;
		if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "codex", action: "login", launchMode: "terminal", available: true, logoutCommand: "codex logout" }] } } as never;
		if (path === "/api/v1/agents/{agent}/update-advisory") {
			const agentId = (options as { params: { path: { agent: string } } }).params.path.agent;
			return { data: { agentId, status: agentId === "codex" ? "behind_latest" : "current", currentVersion: "1.2.3", latestVersion: "1.3.0", binaryPath: "~/.local/bin/codex", maintenanceMethod: savedMethod, checkedAt: "2026-10-06T00:00:00Z", ...(agentId === "codex" && codexInUse ? { inUse: codexInUse } : {}) } } as never;
		}
		return { data: undefined } as never;
	});
	vi.mocked(apiClient.POST).mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/refresh" || path === "/api/v1/agents/readiness/ensure") return { data: readiness } as never;
		if (path === "/api/v1/agents/{agent}/probe") {
			const id = (options as { params: { path: { agent: string } } }).params.path.agent;
			const agent = readiness.agents.find((item) => item.id === id);
			return { data: { agent: { id, authStatus: agent?.authentication.state ?? "unknown" }, supported: true, installed: true } } as never;
		}
		if (path === "/api/v1/agents/{agent}/install") {
			const request = options as { params: { path: { agent: string } }; body: { method: string } };
			const job = { target: request.params.path.agent, method: request.body.method, status: "installing", startedAt: "2026-09-27T12:00:00Z" };
			operationJobs = [job];
			return { data: job } as never;
		}
		return { data: undefined } as never;
	});
}


export { cloudMocks, hostMocks, terminalFocusRequested, terminalStateCallback, terminalMuxFactory };
