import {
	cloudMocks,
	hostMocks,
	terminalFocusRequested,
	terminalStateCallback,
	catalogWithInstalled,
	catalog,
	plans,
	findListRow,
	findAgentRow,
	renderSection,
	readyCatalog,
	mockInstalledOperations,
} from "./HarnessSettingsSection.test.setup";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	act,
	cleanup,
	render,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { apiClient } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import { appI18n } from "../../i18n";
import { agentReadinessQueryKey, type AgentReadiness } from "../../hooks/useAgentReadinessQuery";
import { agentReadiness } from "../../test/agent-readiness-fixtures";
import { TooltipProvider } from "../ui/tooltip";
import { HarnessSettingsSection } from "./HarnessSettingsSection";



describe("HarnessSettingsSection", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		terminalFocusRequested.value = false;
		terminalStateCallback.value = undefined;
		cloudMocks.cloudEnabled = false;
		cloudMocks.org = undefined;
		cloudMocks.connections = [];
		window.tallpine!.clipboard.writeText = vi.fn().mockResolvedValue(undefined);
		vi.spyOn(apiClient, "GET").mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.spyOn(apiClient, "POST").mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: catalog } as never;
			if (path === "/api/v1/agents/refresh") return { data: catalog } as never;
			if (path === "/api/v1/agents/{agent}/install") {
				return { data: { target: "codex", status: "failed", error: "npm failed" } } as never;
			}
			return { data: undefined } as never;
		});
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	function mockClaudeLogin({ probeStatus = "authorized" }: { probeStatus?: string } = {}) {
		const authorized = catalogWithInstalled("claude-code");
		authorized.agents[0].authentication.state = "authorized";
		const calls = { auth: 0, probe: 0 };
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				calls.probe += 1;
				return { data: { agent: { id: "claude-code", label: "Claude Code", authStatus: probeStatus }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") return { data: calls.probe > 0 ? authorized : catalog } as never;
			if (path === "/api/v1/agents/refresh") return { data: catalog } as never;
			if (path === "/api/v1/agents/{agent}/auth") {
				calls.auth += 1;
				return { data: {
					agentId: "claude-code", action: "login", guidance: "",
					terminal: { handleId: "auth-terminal-1", title: "Claude Code login", workingDir: "/tmp", createdAt: "2026-09-15T00:00:00Z" },
				} } as never;
			}
			return { data: undefined } as never;
		});
		vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		return calls;
	}

	it("keeps updates and displayed versions scoped to the selected host", async () => {
		mockInstalledOperations("npm");
		hostMocks.connected = ["remote-1"];
		const localGET = vi.mocked(apiClient.GET).getMockImplementation()!;
		hostMocks.remoteGET.mockImplementation(async (path: string, options: unknown) => {
			if (path === "/api/v1/agents/{agent}/update-advisory") return { data: {
				agentId: "codex", status: (options as { params: { path: { agent: string } } }).params.path.agent === "codex" ? "behind_latest" : "current", currentVersion: "9.4.0", latestVersion: "9.5.0", source: "homebrew",
			} };
			return (localGET as (path: string, options: unknown) => Promise<unknown>)(path, options);
		});
		hostMocks.remotePOST.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/{agent}/install") return { data: { target: "codex", method: "homebrew", status: "installing", startedAt: "2026-10-07T00:00:00Z" } };
			return { data: readyCatalog() };
		});
		try {
			renderSection();
			await within(await findListRow("codex")).findByRole("img", { name: "v1.3.0 available" });
			await userEvent.click(screen.getByRole("button", { name: "Computer" }));
			await userEvent.click(screen.getByRole("menuitem", { name: "remote-1" }));
			await waitFor(() => expect(within(document.querySelector<HTMLElement>('button[data-agent="codex"]')!).getByRole("img", { name: "v9.5.0 available" })).toBeInTheDocument());
			const row = await findAgentRow("codex");
			expect(within(row).getByText("v9.4.0")).toBeInTheDocument();
			expect(within(row).queryByText("v1.2.3")).toBeNull();
			expect(within(row).getByText("v9.5.0 available")).toBeInTheDocument();
			await userEvent.click(within(row).getByRole("button", { name: "Update" }));
			await waitFor(() => expect(hostMocks.remotePOST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything()));
			expect(hostMocks.remotePOST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "homebrew", operation: "update", expectedVersion: "9.5.0" } });
			expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
		} finally {
			hostMocks.connected = [];
			hostMocks.remoteGET.mockReset();
			hostMocks.remotePOST.mockReset();
		}
	});







	it("refreshes local readiness in the background without a Refresh login button", async () => {
		const authorized = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "authorized" })] };
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: authorized } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true }] } } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: [] } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockResolvedValue({ data: authorized } as never);
		renderSection();

		const row = await findAgentRow("claude-code");
		expect(await within(row).findByText("Connected")).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Refresh login" })).toBeNull();
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/refresh");
		expect(within(row).queryByRole("button", { name: "Login" })).toBeNull();
		expect(within(row).queryByRole("button", { name: "Authorized" })).toBeNull();
	});


	it("lets the Settings body scroll on the harness page and filters rows from the search box", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<div className="settings-thin-scrollbar" data-testid="settings-body"><HarnessSettingsSection /></div>
				</TooltipProvider>
			</QueryClientProvider>,
		);
		const body = screen.getByTestId("settings-body");
		await findListRow("codex");
		// The page body is the only scroll container: nothing pins it to the top.
		body.scrollTop = 200;
		body.dispatchEvent(new Event("scroll"));
		expect(body.scrollTop).toBe(200);
		await userEvent.type(screen.getByRole("textbox", { name: "Search harnesses" }), "no-such-harness");
		await waitFor(() => expect(screen.queryByRole("button", { name: /codex/i })).toBeNull());
	});

	it("opens a targeted harness page without scrolling the dialog, focuses Install, and highlights it for two seconds once", async () => {
		const scrollIntoView = vi.fn();
		const setTimeoutSpy = vi.spyOn(window, "setTimeout");
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
		const view = renderSection("codex");
		const row = await findAgentRow("codex");
		const install = await within(row).findByRole("button", { name: "Install" });

		await waitFor(() => expect(document.activeElement).toBe(install));
		// The dialog body does not scroll while a page is open; scrolling it shifted the page out of view.
		expect(scrollIntoView).not.toHaveBeenCalled();
		expect(row).toHaveAttribute("data-focus-highlighted");
		// The page replaces the list, so its search steps aside.
		expect(screen.queryByRole("textbox", { name: "Search harnesses" })).toBeNull();

		const highlightTimeout = setTimeoutSpy.mock.calls.find(([, delay]) => delay === 2_000)?.[0];
		expect(highlightTimeout).toBeTypeOf("function");
		act(() => highlightTimeout?.());
		expect(row).not.toHaveAttribute("data-focus-highlighted");

		view.rerender(
			<QueryClientProvider client={view.client}>
				<TooltipProvider>
					<HarnessSettingsSection focusAgentId="cursor" />
				</TooltipProvider>
			</QueryClientProvider>,
		);
		expect(scrollIntoView).not.toHaveBeenCalled();
	});

	it("focuses Login for an installed harness when authentication is required", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") {
				return { data: { plans: [{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true }] } } as never;
			}
			return { data: undefined } as never;
		});
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		renderSection("claude-code");
		const row = await findAgentRow("claude-code");
		const login = await within(row).findByRole("button", { name: "Login" });

		await waitFor(() => expect(document.activeElement).toBe(login));
	});

	it("falls back to the Account tab when the targeted harness has no primary action", async () => {
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		renderSection("claude-code");
		const row = await findAgentRow("claude-code");

		await waitFor(() => expect(document.activeElement).toBe(within(row).getByRole("tab", { name: "Account" })));
	});

	it("does not scroll, focus, or highlight for an unknown harness id", async () => {
		const scrollIntoView = vi.fn();
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
		renderSection("not-a-harness");
		await screen.findByText("Codex");
		await waitFor(() => expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/install-jobs"));

		expect(scrollIntoView).not.toHaveBeenCalled();
		expect(document.querySelector("[data-focus-highlighted]")).toBeNull();
		expect(document.activeElement).toBe(document.body);
	});

	it("shows installed harnesses and install actions without authentication UI", async () => {
		renderSection();
		// The list carries status as a tooltip; the harness page states it.
		await waitFor(async () => expect(await findListRow("claude-code")).toHaveAccessibleDescription("Installed"), { timeout: 10_000 });
		expect(await findListRow("codex")).toBeInTheDocument();
		const page = await findAgentRow("claude-code");
		expect(within(page).getAllByText("Installed").length).toBeGreaterThan(0);
		expect(screen.queryByText(/sign in/i)).not.toBeInTheDocument();
	});

	it("shows the authentication action for an installed agent and opens documentation", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") {
				return { data: { plans: [{ agentId: "claude-code", action: "login", launchMode: "documentation", available: true, documentationUrl: "https://example.test/login" }] } } as never;
			}
			return { data: undefined } as never;
		});
		const openExternal = vi.spyOn(tallpineBridge.app, "openExternal").mockResolvedValue(undefined);
		renderSection();
		const row = await findAgentRow("claude-code");
		const login = await within(row).findByRole("button", { name: "Login" });
		await userEvent.click(login);
		expect(openExternal).toHaveBeenCalledWith("https://example.test/login");
	});

	it("shows cached readiness while silently refreshing when the page opens", async () => {
		const refreshed = catalogWithInstalled("claude-code", "codex");
		let current = catalog;
		let resolveRefresh!: (value: { data: typeof refreshed }) => void;
		const pendingRefresh = new Promise<{ data: typeof refreshed }>((resolve) => { resolveRefresh = resolve; });
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: current } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/refresh") return await pendingRefresh as never;
			return { data: undefined } as never;
		});

		renderSection();
		const row = await findAgentRow("codex");

		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/refresh"));
		await within(row).findByRole("button", { name: "Install" });
		expect(screen.queryByRole("button", { name: "Refresh harness status" })).not.toBeInTheDocument();
		expect(screen.queryByText("Checking…")).not.toBeInTheDocument();
		await act(async () => {
			current = refreshed;
			resolveRefresh({ data: refreshed });
		});
		await waitFor(() => {
			expect(within(row).getByText("Installed")).toBeInTheDocument();
			expect(within(row).queryByRole("button", { name: "Install" })).toBeNull();
		});
	});

	it("runs a fresh authentication check after terminal completion", async () => {
		const authorized = catalogWithInstalled("claude-code");
		authorized.agents[0].authentication.state = "authorized";
		let probeCalls = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				probeCalls += 1;
				return { data: { agent: { id: "claude-code", label: "Claude Code", authStatus: "authorized" }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") return { data: authorized } as never;
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "claude-code",
				action: "login",
				guidance: "Complete login in the terminal.",
				terminal: {
					handleId: "auth-terminal-1",
					title: "Claude Code login",
					workingDir: "/tmp",
					createdAt: "2026-09-15T00:00:00Z",
				},
			} } as never;
			return { data: undefined } as never;
		});
		const close = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		const user = userEvent.setup();

		renderSection();
		const row = await findAgentRow("claude-code");
		const login = await within(row).findByRole("button", { name: "Login" });
		await user.click(login);
		await within(row).findByTestId("inline-terminal-body");
		expect(terminalStateCallback.value).toBeDefined();
		expect(terminalFocusRequested.value).toBe(false);

		act(() => terminalStateCallback.value?.("attached"));
		await waitFor(() => expect(terminalFocusRequested.value).toBe(true));

		act(() => terminalStateCallback.value?.("exited"));
		await waitFor(() => expect(probeCalls).toBe(1));

		await waitFor(() => expect(close).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", {
			params: { path: { handleId: "auth-terminal-1" } },
		}));
		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument());
	});

	// The task composer's "Log in" shortcut opens this page asking it to start
	// the login, so the user lands in the same flow the row's button runs.
	it("starts the focused harness login once when opened from a login shortcut", async () => {
		const calls = mockClaudeLogin();
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<HarnessSettingsSection focusAgentId="claude-code" startLogin />
				</TooltipProvider>
			</QueryClientProvider>,
		);
		const row = await findAgentRow("claude-code");
		await within(row).findByTestId("inline-terminal-body");
		expect(calls.auth).toBe(1);
	});

	// A login fixed here must clear a stale model-discovery error in an open
	// task composer without the user pressing refresh.
	it("invalidates the harness's cached model catalogs after a login check", async () => {
		const calls = mockClaudeLogin();
		const user = userEvent.setup();
		const { client } = renderSection();
		const local = ["agent-models", "claude-code", ""] as const;
		const otherAgent = ["agent-models", "codex", ""] as const;
		client.setQueryData(local, { agentId: "claude-code", models: [], warning: "OAuth access token has expired." });
		client.setQueryData(otherAgent, { agentId: "codex", models: [] });
		const row = await findAgentRow("claude-code");
		await user.click(await within(row).findByRole("button", { name: "Login" }));
		await within(row).findByTestId("inline-terminal-body");
		expect(client.getQueryState(local)?.isInvalidated).toBe(false);

		act(() => terminalStateCallback.value?.("exited"));
		await waitFor(() => expect(calls.probe).toBeGreaterThan(0));
		await waitFor(() => expect(client.getQueryState(local)?.isInvalidated).toBe(true));
		expect(client.getQueryState(otherAgent)?.isInvalidated).toBe(false);
	});

	// The first check right after a login terminal exits can fail transiently;
	// the panel must not report a completed login as signed out.
	async function loginWithProbeResults(statuses: string[], readinessAfterProbes: number, terminalInput?: string) {
		const authorized = catalogWithInstalled("claude-code");
		authorized.agents[0].authentication.state = "authorized";
		let probeCalls = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				const authStatus = statuses[Math.min(probeCalls, statuses.length - 1)];
				probeCalls += 1;
				return { data: { agent: { id: "claude-code", label: "Claude Code", authStatus }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") return { data: probeCalls >= readinessAfterProbes ? authorized : catalog } as never;
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "claude-code", action: "login", guidance: "Complete login in the terminal.", terminalInput,
				terminal: { handleId: "auth-terminal-1", title: "Claude Code login", workingDir: "/tmp", createdAt: "2026-09-15T00:00:00Z" },
			} } as never;
			return { data: undefined } as never;
		});
		const close = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("claude-code");
		await user.click(await within(row).findByRole("button", { name: "Login" }));
		await within(row).findByTestId("inline-terminal-body");
		return { row, close, probeCalls: () => probeCalls, exit: () => act(() => terminalStateCallback.value?.("exited")) };
	}

	it("shows login guidance only when it asks for an action outside the terminal", async () => {
		const { row, exit } = await loginWithProbeResults(["authorized"], Number.POSITIVE_INFINITY);
		expect(within(row).queryByText("Complete login in the terminal.")).toBeNull();
		exit();
		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument());
		cleanup();

		const withAction = await loginWithProbeResults(["authorized"], Number.POSITIVE_INFINITY, "/login\r");
		expect(within(withAction.row).getByText("Complete login in the terminal.")).toBeInTheDocument();
	});

	it("re-checks a login whose first post-login check fails transiently", async () => {
		const { row, close, probeCalls, exit } = await loginWithProbeResults(["unauthorized", "authorized"], Number.POSITIVE_INFINITY);
		exit();

		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument(), { timeout: 5_000 });
		expect(probeCalls()).toBe(2);
		expect(close).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", { params: { path: { handleId: "auth-terminal-1" } } });
	});

	it("closes a login panel it could not confirm once the harness reads as logged in", async () => {
		// Every post-login probe misses, but the daemon's readiness later reports
		// the harness logged in.
		const { row, probeCalls, exit } = await loginWithProbeResults(["unauthorized"], 4);
		exit();

		await waitFor(() => expect(probeCalls()).toBe(4), { timeout: 8_000 });
		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument(), { timeout: 5_000 });
	}, 15_000);

	it("refreshes authentication when the user closes the login terminal", async () => {
		const authorized = catalogWithInstalled("claude-code");
		authorized.agents[0].authentication.state = "authorized";
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				return { data: { agent: { id: "claude-code", label: "Claude Code", authStatus: "authorized" }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") return { data: authorized } as never;
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "claude-code",
				action: "login",
				guidance: "Complete login in the terminal.",
				terminal: {
					handleId: "auth-terminal-close",
					title: "Claude Code login",
					workingDir: "/tmp",
					createdAt: "2026-09-15T00:00:00Z",
				},
			} } as never;
			return { data: undefined } as never;
		});
		const closeTerminal = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		const user = userEvent.setup();

		renderSection();
		const row = await findAgentRow("claude-code");
		await user.click(await within(row).findByRole("button", { name: "Login" }));
		await user.click(await within(row).findByRole("button", { name: "Close settings" }));

		await waitFor(() => expect(closeTerminal).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", {
			params: { path: { handleId: "auth-terminal-close" } },
		}));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", {
			params: { path: { agent: "claude-code" } },
		}));
		await within(row).findByText("Connected");
		expect(within(row).queryByRole("button", { name: "Authorized" })).toBeNull();
	});

	it("uses Configured for a completed setup action", async () => {
		const authorized = catalogWithInstalled("codex");
		authorized.agents[1].authentication.state = "authorized";
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: authorized } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "codex", action: "setup", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: authorized } as never;
			if (path === "/api/v1/agents/{agent}/probe") return { data: { agent: { id: "codex", label: "Codex", authStatus: "authorized" }, supported: true, installed: true } } as never;
			return { data: undefined } as never;
		});

		renderSection();
		const row = await findAgentRow("codex");

		await within(row).findByText("Configured");
	});

	it("does not expose manual readiness controls", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		renderSection();
		const row = await findAgentRow("claude-code");
		expect(await within(row).findByRole("button", { name: "Login" })).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Installed" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Refresh harness status" })).not.toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Check login" })).not.toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Check configuration" })).not.toBeInTheDocument();
	});

	it.each(["not_checked", "install_check_unsupported"])("shows a neutral unknown state without install controls for %s", async (reasonCode) => {
		const unknown = catalogWithInstalled();
		unknown.agents[1].installation.state = "unknown";
		unknown.agents[1].installation.reasonCode = reasonCode;
		unknown.agents[1].installation.reason = reasonCode === "not_checked"
			? "Installation has not been checked yet."
			: "Installation checks are not supported for this harness.";
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: unknown } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [] } } as never;
			return { data: undefined } as never;
		});

		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByText("Installation status unknown")).toBeInTheDocument();
		expect(within(row).queryByText("Installation checks are not supported for this harness.")).not.toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Install" })).not.toBeInTheDocument();
	});

	it("falls back to installer plans when readiness cannot be loaded", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { error: { message: "readiness unavailable" } } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [] } } as never;
			return { data: undefined } as never;
		});

		renderSection();
		const row = await findAgentRow("codex");
		// The readiness query retries once before surfacing its error.
		expect(await within(row).findByRole("button", { name: "Install" }, { timeout: 5_000 })).toBeInTheDocument();
		expect(within(row).queryByText("Installation status unknown")).not.toBeInTheDocument();
	});

	it("falls back to ensuring readiness when the page-open refresh fails", async () => {
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/refresh") return { error: { message: "refresh failed" } } as never;
			if (path === "/api/v1/agents/readiness/ensure") return { data: catalog } as never;
			return { data: undefined } as never;
		});

		renderSection();
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith(
			"/api/v1/agents/readiness/ensure",
			{ body: { agentIds: [], purpose: "display" } },
		));
	});

	it("re-fetches readiness when both the page-open refresh and ensure fail", async () => {
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/refresh") throw new Error("network down");
			if (path === "/api/v1/agents/readiness/ensure") throw new Error("network down");
			return { data: undefined } as never;
		});
		let readinessFetches = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") {
				readinessFetches += 1;
				return { data: catalog } as never;
			}
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});

		renderSection();
		await screen.findByText("Codex");
		await waitFor(() => expect(readinessFetches).toBeGreaterThanOrEqual(2));
	});

	it("starts the fixed daemon install route and exposes retry after failure", async () => {
		const user = userEvent.setup();
		renderSection();
		const codexRow = await findAgentRow("codex");
		const install = await within(codexRow as HTMLElement).findByRole("button", { name: "Install" });
		// The first install is one click; a method picker appears only after a failure.
		expect(within(codexRow as HTMLElement).queryByRole("button", { name: "Installation method" })).toBeNull();
		await user.click(install);

		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "homebrew", operation: "install" },
		}));
		await waitFor(() => expect(codexRow).toHaveTextContent("npm failed"));
		expect(codexRow).toHaveTextContent("Retry");
		await user.click(within(codexRow as HTMLElement).getByRole("button", { name: "Retry install" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenLastCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "homebrew", operation: "install" },
		}));
	});

	it("lets a failed install retry through another available method", async () => {
		const user = userEvent.setup();
		renderSection();
		const codexRow = await findAgentRow("codex");
		await user.click(await within(codexRow).findByRole("button", { name: "Install" }));
		await waitFor(() => expect(codexRow).toHaveTextContent("npm failed"));
		await user.click(within(codexRow).getByRole("button", { name: "Installation method" }));
		await user.click(await screen.findByRole("menuitem", { name: "npm" }));
		await user.click(within(codexRow).getByRole("button", { name: "Retry install" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenLastCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "npm", operation: "install" },
		}));
	});

	it("automatically uses the installer available on the user's machine", async () => {
		const npmOnlyPlans = {
			agents: plans.agents.map((plan) => plan.agentId === "codex" ? {
				...plan,
				method: "npm",
				methods: plan.methods.map((method) => ({
					...method,
					available: method.id === "npm",
					recommended: method.id === "npm",
				})),
			} : plan),
		};
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: npmOnlyPlans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");

		await within(row).findByRole("button", { name: "Install" });
		expect(within(row).queryByRole("combobox", { name: "Installation method" })).not.toBeInTheDocument();
		await user.click(within(row).getByRole("button", { name: "Install" }));

		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "npm", operation: "install" },
		}));
	});

	it("does not treat a historical successful job as current installation inventory", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "succeeded", method: "npm", updatedAt: "2026-08-01T00:00:00Z" }] } } as never;
			return { data: undefined } as never;
		});
		renderSection();
		const row = await findAgentRow("codex");
		await within(row).findByRole("button", { name: "Install" });
		expect(within(row).getByRole("button", { name: "Install" })).toBeEnabled();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", expect.anything());
	});

	it("probes the installed harness after an observed install completes", async () => {
		let installed = false;
		let installerFetches = 0;
		let jobFetches = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: installed ? catalogWithInstalled("claude-code", "codex") : catalog } as never;
			if (path === "/api/v1/agents/installers") {
				installerFetches += 1;
				return { data: plans } as never;
			}
			if (path === "/api/v1/agents/install-jobs") {
				jobFetches += 1;
				return { data: { jobs: [{
					target: "codex",
					status: jobFetches === 1 ? "installing" : "succeeded",
					method: "npm",
					updatedAt: "2026-08-31T00:00:00Z",
				}] } } as never;
			}
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				installed = true;
				return { data: { agent: { id: "codex", label: "Codex" }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") {
				return { data: installed ? catalogWithInstalled("claude-code", "codex") : catalog } as never;
			}
			return { data: undefined } as never;
		});
		renderSection();
		const row = await findAgentRow("codex");
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } }), { timeout: 3_000 });
		await waitFor(() => expect(row).toHaveTextContent("Installed"));
		await waitFor(() => expect(installerFetches).toBe(2));
	});

	it.each(["authorized", "unauthorized"] as const)("updates a mounted readiness consumer after installation returns %s", async (authentication) => {
		const initial = { agents: [
			agentReadiness("claude-code", "Claude Code"),
			agentReadiness("codex", "Codex", { installation: "not_installed", authentication: "unknown" }),
		] };
		let probed = false;
		const updated = agentReadiness("codex", "Codex", { authentication });
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: initial } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: probed ? { agents: [updated] } : initial } as never;
			if (path === "/api/v1/agents/{agent}/install") return { data: { target: "codex", status: "succeeded", method: "homebrew", updatedAt: "2026-09-19T00:00:00Z" } } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				probed = true;
				return { data: { agent: { id: "codex", authStatus: authentication }, installed: true } } as never;
			}
			return { data: undefined } as never;
		});
		const { client } = renderSection(undefined, "codex");
		const selector = screen.getByTestId("originating-selector");
		await waitFor(() => expect(selector).toHaveTextContent("not_ready"));
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Install" }));

		await waitFor(() => expect(row).toHaveTextContent(authentication === "authorized" ? "Connected" : "Installed"));
		await waitFor(() => expect(selector).toHaveTextContent(authentication === "authorized" ? /^ready$/ : /^not_ready$/));
		expect(client.getQueryData<AgentReadiness>(agentReadinessQueryKey)?.agents).toEqual([initial.agents[0], updated]);
		expect(screen.getByTestId("originating-selector")).toBe(selector);
	});

	it("updates a mounted readiness consumer when the Harness authentication terminal completes", async () => {
		const initial = { agents: [
			agentReadiness("claude-code", "Claude Code"),
			agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
		] };
		const authorized = agentReadiness("codex", "Codex");
		let probed = false;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: initial } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "codex", action: "login", launchMode: "terminal", available: true, logoutCommand: "codex logout" }] } } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: probed ? { agents: [authorized] } : initial } as never;
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "codex", action: "login", terminal: { handleId: "auth-codex", projectId: null, sessionId: null, workingDir: "/tmp", title: "Codex login", createdAt: "2026-09-19T00:00:00Z" },
			} } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				probed = true;
				return { data: { agent: { id: "codex", authStatus: "authorized" }, installed: true } } as never;
			}
			return { data: undefined } as never;
		});
		vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		const { client } = renderSection(undefined, "codex");
		const selector = screen.getByTestId("originating-selector");
		await waitFor(() => expect(selector).toHaveTextContent("not_ready"));
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Login" }));
		await userEvent.click(await screen.findByRole("button", { name: "Complete login terminal" }));

		await waitFor(() => expect(selector).toHaveTextContent(/^ready$/));
		expect(client.getQueryData<AgentReadiness>(agentReadinessQueryKey)?.agents).toEqual([initial.agents[0], authorized]);
		expect(screen.getByTestId("originating-selector")).toBe(selector);
		await waitFor(() => expect(screen.queryByRole("button", { name: "Complete login terminal" })).not.toBeInTheDocument());
	});

	it("admits only one install request per harness while the first POST is pending", async () => {
		let resolveInstall!: (value: unknown) => void;
		let installCalls = 0;
		const pendingInstall = new Promise((resolve) => { resolveInstall = resolve; });
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/install") {
				installCalls += 1;
				return await pendingInstall as never;
			}
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		const button = await within(row).findByRole("button", { name: "Install" });
		await user.dblClick(button);
		expect(installCalls).toBe(1);
		resolveInstall({ data: { target: "codex", status: "installing", method: "homebrew" } });
		await waitFor(() => expect(row).toHaveTextContent("Installing…"));
	});

	it("hydrates interrupted jobs and offers verification", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "interrupted", method: "npm", error: "Tallpine restarted", output: "partial output", expectedDestination: "/Users/test/.npm/bin/codex" }] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/verify") return { data: { target: "codex", status: "verifying" } } as never;
			if (path === "/api/v1/agents/{agent}/install") return { data: { target: "codex", status: "installing", method: "npm" } } as never;
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		await waitFor(() => expect(row).toHaveTextContent("Interrupted"));
		await user.click(within(row).getByRole("button", { name: "Verify again" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/verify", { params: { path: { agent: "codex" } } });
		await waitFor(() => expect(row).toHaveTextContent("Verifying…"));
	});

	it("shows and copies daemon diagnostics", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "failed", method: "npm", error: "exit status 1", output: "permission denied", expectedDestination: "/Users/test/.npm/bin/codex" }] } } as never;
			if (path === "/api/v1/usage/sessions/memory") {
				return { data: {
					sessions: [{ sessionId: "s1", rssBytes: 641_728_512, processCount: 3, cpuPercent: 0, sampledAt: "2026-09-22T00:00:00Z", processes: [] }],
					app: { rssBytes: 2 * 1024 ** 3, processCount: 20, cpuPercent: 12 },
					system: { totalBytes: 32 * 1024 ** 3, availableBytes: 4 * 1024 ** 3, swapTotalBytes: 0, swapUsedBytes: 0, swapBytesPerSec: 0, cpuCount: 8, load1: 1.25, cpuPercent: 30, pressureRaw: 5, pressureSource: "psi" },
				} } as never;
			}
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		await user.click(await within(row).findByRole("button", { name: "Show diagnostics" }));
		expect(row).toHaveTextContent("permission denied");
		expect(row).toHaveTextContent("/Users/test/.npm/bin/codex");
		await user.click(within(row).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalled());
		const copied = vi.mocked(window.tallpine!.clipboard.writeText).mock.calls.at(-1)![0] as string;
		// The machine the install died on is part of the report.
		expect(copied).toContain("permission denied");
		expect(copied).toContain("Memory: Tallpine 2.0 GB · available 4.0 GB of 32.0 GB");
		expect(copied).toContain("CPU: 30% of 8 cores · load 1.25");
		expect(copied).toContain("Live sessions: 1 · 612 MB");
	});

	it("copies the remote host's machine numbers, not this computer's", async () => {
		hostMocks.connected = ["remote-1"];
		try {
			const memory = (availableGB: number, cores: number) => ({ data: {
				sessions: [],
				app: { rssBytes: 1024 ** 3, processCount: 4, cpuPercent: 2 },
				system: { totalBytes: 64 * 1024 ** 3, availableBytes: availableGB * 1024 ** 3, swapTotalBytes: 0, swapUsedBytes: 0, swapBytesPerSec: 0, cpuCount: cores, load1: 0.5, cpuPercent: 10, pressureRaw: 0, pressureSource: "psi" },
			} });
			hostMocks.remoteGET.mockImplementation(async (path: string) => {
				if (path === "/api/v1/agents/readiness") return { data: catalog };
				if (path === "/api/v1/agents/installers") return { data: plans };
				if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "failed", method: "npm", error: "exit status 1", output: "remote failure" }] } };
				if (path === "/api/v1/usage/sessions/memory") return memory(48, 32);
				return { data: undefined };
			});
			// Readiness refreshes and installs answer the same as this computer's.
			hostMocks.remotePOST.mockImplementation((path: never, init: never) => apiClient.POST(path, init));
			vi.mocked(apiClient.GET).mockImplementation(async (path) => {
				if (path === "/api/v1/usage/sessions/memory") return memory(3, 8) as never;
				return { data: undefined } as never;
			});
			const user = userEvent.setup();
			const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			render(
				<QueryClientProvider client={client}>
					<TooltipProvider>
						<HarnessSettingsSection hostId="remote-1" />
					</TooltipProvider>
				</QueryClientProvider>,
			);
			const row = await findAgentRow("codex");
			await user.click(await within(row).findByRole("button", { name: "Show diagnostics" }));
			await user.click(within(row).getByRole("button", { name: "Copy diagnostics" }));
			await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalled());
			const copied = vi.mocked(window.tallpine!.clipboard.writeText).mock.calls.at(-1)![0] as string;
			expect(copied).toContain("remote failure");
			expect(copied).toContain("available 48.0 GB of 64.0 GB");
			expect(copied).toContain("of 32 cores");
			expect(apiClient.GET).not.toHaveBeenCalledWith("/api/v1/usage/sessions/memory", expect.anything());
		} finally {
			hostMocks.connected = [];
			hostMocks.remoteGET.mockReset();
			hostMocks.remotePOST.mockReset();
		}
	});

	it("leaves load out of copied diagnostics on a platform with no load average", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "failed", method: "npm", error: "exit status 1", output: "permission denied", expectedDestination: "/Users/test/.npm/bin/codex" }] } } as never;
			if (path === "/api/v1/usage/sessions/memory") {
				return { data: {
					sessions: [],
					app: { rssBytes: 2 * 1024 ** 3, processCount: 20, cpuPercent: 12 },
					// Windows reports load1 = -1 to mean "no such concept here", not 0.
					system: { totalBytes: 32 * 1024 ** 3, availableBytes: 4 * 1024 ** 3, swapTotalBytes: 0, swapUsedBytes: 0, swapBytesPerSec: 0, cpuCount: 8, load1: -1, cpuPercent: 30, pressureRaw: 5, pressureSource: "available_pct" },
				} } as never;
			}
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		await user.click(await within(row).findByRole("button", { name: "Show diagnostics" }));
		await user.click(within(row).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalled());
		const copied = vi.mocked(window.tallpine!.clipboard.writeText).mock.calls.at(-1)![0] as string;
		expect(copied).toContain("CPU: 30% of 8 cores");
		expect(copied).not.toContain("load");
	});

	it("still copies diagnostics when the host cannot be measured", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "failed", method: "npm", error: "exit status 1", output: "permission denied" }] } } as never;
			if (path === "/api/v1/usage/sessions/memory") return { error: { code: "MEMORY_UNSUPPORTED" } } as never;
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		await user.click(await within(row).findByRole("button", { name: "Show diagnostics" }));
		await user.click(within(row).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalled());
		const copied = vi.mocked(window.tallpine!.clipboard.writeText).mock.calls.at(-1)![0] as string;
		expect(copied).toContain("permission denied");
		expect(copied).not.toContain("Machine");
	});

	it("surfaces install job polling failures", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { error: { error: { message: "Could not poll installation status." } } } as never;
			return { data: undefined } as never;
		});
		renderSection();
		expect(await screen.findByText("Could not poll installation status.")).toBeInTheDocument();
	});
});
