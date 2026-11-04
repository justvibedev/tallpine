import {
	h,
	Wrap,
	task,
	startTask,
	waitForTaskReady,
} from "./TaskComposer.test.setup";

import {
	act,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient } from "@tanstack/react-query";
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { TaskComposer } from "./TaskComposer";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { agentReadinessQueryKey } from "../hooks/useAgentReadinessQuery";
import { clearCachedAgentModels } from "../lib/agent-model-cache";
import { useUiStore } from "../stores/ui-store";

beforeEach(() => {
	useUiStore.setState({ remoteHosts: true });
	h.get.mockImplementation(async (path: string) => {
		if (path.includes("/models")) {
			return {
				data: {
					agent: "codex",
					selectionMode: "text",
					models: [],
					allowCustom: true,
					refreshRecommended: false,
				},
			};
		}
		return { data: { status: "ok", project: { config: {} } } };
	});
});

afterEach(() => {
	useUiStore.setState({ remoteHosts: false });
	h.delete.mockReset();
	h.get.mockReset();
	h.post.mockReset();
	h.remoteGet.mockReset();
	h.remotePost.mockReset();
	h.remotePost.mockResolvedValue({ data: {} });
	h.remoteDelete.mockReset();
	h.ensureReadiness.mockReset();
	h.ensureTargetedReadiness.mockReset();
	h.agentCatalog = undefined;
	vi.unstubAllGlobals();
	h.agentValues.length = 0;
	window.localStorage.removeItem("ao.taskComposer.preferences.v1");
	clearCachedAgentModels("codex");
});

describe("TaskComposer", () => {
	it("does not launch a remote project task without a ready agent", async () => {
		h.agentCatalog = { agents: [] };
		h.remoteGet.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: [] } }
			: { data: { status: "ok", project: { id: "project-a", config: {} } } });
		h.remotePost.mockImplementation(async (path: string) => path === "/api/v1/agents/readiness/ensure"
			? { data: { agents: [] } }
			: { data: {} });
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText("No agent is ready on this computer. Configure one there first.");
		expect(startTask()).toBeDisabled();
		expect(h.remotePost.mock.calls.some(([path]) => path === "/api/v1/orchestrators/delegate")).toBe(false);
	});

	it("does not start a remote task with the project's uninstalled default agent", async () => {
		h.agentCatalog = { agents: [
			agentReadiness("claude-code", "Claude Code", { installation: "not_installed" }),
			agentReadiness("opencode", "OpenCode", { authentication: "unknown" }),
		] };
		h.remoteGet.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: ["opencode"] } }
			: { data: { status: "ok", project: { id: "project-a", config: { worker: { agent: "claude-code" } } } } });
		h.remotePost.mockImplementation(async (path: string) => path === "/api/v1/agents/readiness/ensure"
			? { data: { agents: [
				agentReadiness("claude-code", "Claude Code", { installation: "not_installed" }),
				agentReadiness("opencode", "OpenCode", { authentication: "unknown" }),
			] } }
			: { data: {} });
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
		await waitFor(() => expect(screen.getByTestId("agent-field")).toBeEnabled());
		expect(startTask()).toBeDisabled();
		expect(screen.queryByText("No agent is ready on this computer. Configure one there first.")).not.toBeInTheDocument();
	});

	it("creates a remote project task through its host, not the local daemon", async () => {
		h.agentCatalog = { agents: [agentReadiness("opencode", "OpenCode", { authentication: "unknown" })] };
		const onCreated = vi.fn();
		h.remoteGet.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: { agent: "opencode", selectionMode: "text", models: [], allowCustom: true } };
			if (path === "/api/v1/settings") return { data: { defaultSessionMode: "chat", chatHarnesses: ["opencode"] } };
			return { data: { status: "ok", project: { id: "project-a", config: { worker: { agent: "opencode" } } } } };
		});
		h.remotePost.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: { agents: [agentReadiness("opencode", "OpenCode", { authentication: "unknown" })] } };
			if (path === "/api/v1/projects/{id}/tasks/prepare") return { data: { taskPreparation: "" } };
			if (path === "/api/v1/orchestrators/delegate") return { data: { workerId: "remote-task" } };
			return { data: {} };
		});
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={onCreated} /></Wrap>);
		await waitFor(() => expect(startTask()).toBeEnabled());
		expect(screen.queryByText("No agent is ready on this computer. Configure one there first.")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Fix the issue" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("remote-task"));
		expect(h.remotePost).toHaveBeenCalledWith("/api/v1/orchestrators/delegate", expect.objectContaining({
			body: expect.objectContaining({ projectId: "project-a", brief: "Fix the issue" }),
		}));
		expect(h.post.mock.calls.some(([path]) => path === "/api/v1/orchestrators/delegate")).toBe(false);
	});

	it("reuses a task request id after an uncertain response and changes it with the draft", async () => {
		h.agentCatalog = { agents: [agentReadiness("opencode", "OpenCode")] };
		h.remoteGet.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: { agent: "opencode", selectionMode: "text", models: [], allowCustom: true } };
			if (path === "/api/v1/settings") return { data: { defaultSessionMode: "chat", chatHarnesses: ["opencode"] } };
			return {
				data: {
					status: "ok",
					project: { id: "project-a", config: { worker: { agent: "opencode" } } },
				},
			};
		});
		h.remotePost.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: { agents: [agentReadiness("opencode", "OpenCode")] } };
			if (path === "/api/v1/projects/{id}/tasks/prepare") return { data: { taskPreparation: "" } };
			if (path === "/api/v1/orchestrators/delegate") return { error: { code: "SPAWN_TIMEOUT", message: "response lost" } };
			return { data: {} };
		});
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.change(task(), { target: { value: "Fix it" } });
		fireEvent.click(startTask());
		await screen.findByText("response lost");
		fireEvent.click(startTask());
		await waitFor(() => expect(h.remotePost.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate")).toHaveLength(2));
		const requests = h.remotePost.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate");
		const firstID = requests[0][1].body.clientRequestId;
		expect(firstID).toBeTruthy();
		expect(requests[1][1].body.clientRequestId).toBe(firstID);
		await screen.findByText("response lost");
		fireEvent.change(task(), { target: { value: "Fix something else" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.remotePost.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate")).toHaveLength(3));
		const changed = h.remotePost.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate")[2][1].body.clientRequestId;
		expect(changed).not.toBe(firstID);
	});

	it("preselects the highest-ranked ready agent for a standalone task", async () => {
		h.agentCatalog = {
			agents: [
				agentReadiness("claude-code", "Claude Code"),
				agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" }),
				agentReadiness("cursor", "Cursor", { authentication: "unauthorized" }),
			],
		};

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		await waitFor(() => expect(screen.queryByRole("status", { name: "Loading models…" })).not.toBeInTheDocument());
	});

	it("preserves an explicitly selected standalone agent when readiness rankings refresh", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		h.agentCatalog = {
			agents: [
				agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" }),
				agentReadiness("claude-code", "Claude Code"),
			],
		};

		const { rerender } = render(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		fireEvent.click(screen.getByLabelText("Agent"));
		expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "claude-code");

		h.agentCatalog = {
			agents: [
				agentReadiness("claude-code", "Claude Code"),
				agentReadiness("codex", "Codex", { usageCount: 10, lastUsedAt: "2026-09-21T12:00:00Z" }),
			],
		};
		rerender(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() =>
			expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "claude-code"),
		);
	});

	it("restores a standalone agent's draft model after switching away and back", async () => {
		h.agentCatalog = {
			agents: [
				agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" }),
				agentReadiness("claude-code", "Claude Code"),
			],
		};
		h.get.mockImplementation(async (path: string, request?: { params?: { path?: { agent?: string } } }) => {
			if (path.includes("/models")) {
				return request?.params?.path?.agent === "claude-code"
					? {
							data: {
								agent: "claude-code",
								selectionMode: "text",
								models: [{ id: "fable-5.1", label: "Fable 5.1", isDefault: true }],
								allowCustom: true,
							},
						}
					: {
							data: {
								agent: "codex",
								selectionMode: "text",
								models: [
									{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
									{ id: "gpt-5.5", label: "GPT-5.5" },
								],
								allowCustom: true,
							},
						};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		const model = await screen.findByRole("button", { name: "Model" });
		await waitFor(() => expect(model).toHaveTextContent("GPT-6-Astra"));
		await userEvent.click(model);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5.5" }));
		expect(model).toHaveTextContent("GPT-5.5");

		fireEvent.click(screen.getByLabelText("Agent"));
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Fable 5.1"));
		fireEvent.click(screen.getByLabelText("Agent"));

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.5"));
	});

	it("restores the last successfully spawned standalone harness and model in a new composer", async () => {
		h.agentCatalog = {
			agents: [
				agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" }),
				agentReadiness("claude-code", "Claude Code"),
			],
		};
		h.get.mockImplementation(async (path: string, request?: { params?: { path?: { agent?: string } } }) => {
			if (path.includes("/models")) {
				return request?.params?.path?.agent === "claude-code"
					? {
							data: {
								agent: "claude-code",
								selectionMode: "text",
								models: [{ id: "fable-5.1", label: "Fable 5.1", isDefault: true }],
								allowCustom: true,
							},
						}
					: {
							data: {
								agent: "codex",
								selectionMode: "text",
								models: [
									{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
									{ id: "gpt-5.5", label: "GPT-5.5" },
								],
								allowCustom: true,
							},
						};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-remembered" } } });

		const first = render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);
		fireEvent.click(await screen.findByLabelText("Agent"));
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("Fable 5.1");
		fireEvent.change(task(), { target: { value: "Remember this setup" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		first.unmount();

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "claude-code"));
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("Fable 5.1");
	});

	it("persists successful model choices per project without leaking them to another project", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [
							{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
							{ id: "gpt-5.5", label: "GPT-5.5" },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "project-remembered" } });

		const first = render(<Wrap><TaskComposer projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(model);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5.5" }));
		fireEvent.change(task(), { target: { value: "Remember this project setup" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		first.unmount();

		const remembered = render(<Wrap><TaskComposer projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.5"));
		remembered.unmount();

		render(<Wrap><TaskComposer projectId="project-b" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-6-Astra"));
	});

	it("does not persist a standalone preference when task creation fails", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [
							{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
							{ id: "gpt-5.5", label: "GPT-5.5" },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ error: { code: "SPAWN_FAILED", message: "Could not start task" } });

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(model);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5.5" }));
		fireEvent.change(task(), { target: { value: "This spawn will fail" } });
		fireEvent.click(screen.getByText("Start task"));

		expect(await screen.findByText("Could not start task")).toBeInTheDocument();
		expect(window.localStorage.getItem("ao.taskComposer.preferences.v1")).toBeNull();
	});

	it("falls back when remembered standalone harness and model choices are no longer valid", async () => {
		window.localStorage.setItem("ao.taskComposer.preferences.v1", JSON.stringify({
			__standalone__: {
				lastAgent: "removed-agent",
				agents: {
					codex: { model: "retired-model", mode: "" },
					"removed-agent": { model: "removed-model", mode: "" },
				},
			},
		}));
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true }],
						allowCustom: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT-6-Astra");
	});

	it("keeps a standalone task unselected when no agent is ready", () => {
		h.agentCatalog = {
			agents: [
				agentReadiness("claude-code", "Claude Code", { installation: "not_installed" }),
				agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
			],
		};

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "");
		expect(screen.getByLabelText("Model")).toHaveTextContent("Select agent");
		expect(screen.getByLabelText("Model")).toHaveAttribute("aria-disabled", "true");
	});

	it("starts a standalone worker without loading or sending a project", async () => {
		const onCreated = vi.fn();
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={onCreated} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		fireEvent.change(task(), { target: { value: "Research release options" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("standalone-1"));
		expect(h.post).toHaveBeenCalledWith(
			"/api/v1/sessions",
			expect.objectContaining({
				body: expect.objectContaining({
					kind: "worker",
					harness: "codex",
					prompt: "Research release options",
				}),
			}),
		);
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("projectId");
		expect(h.get.mock.calls.some(([path]) => path === "/api/v1/projects/{id}")).toBe(false);
	});

	it("sends the visible default model when starting a standalone worker", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-6-astra", label: "GPT-6 Astra", isDefault: true },
							{ id: "gpt-5.6-luna", label: "GPT-5.6 Luna" },
						],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-6 Astra"));
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({ body: expect.objectContaining({ model: "gpt-6-astra" }) }),
			),
		);
	});

	it("sends the visible default mode when starting a standalone mode-list worker", async () => {
		h.agentCatalog = { agents: [agentReadiness("amp", "Amp")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "amp",
						selectionMode: "mode",
						models: [
							{ id: "high", label: "High", isDefault: true },
							{ id: "low", label: "Low" },
						],
						allowCustom: false,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("High"));
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({ body: expect.objectContaining({ model: "high" }) }),
			),
		);
	});

	it("sends the visible default effort when starting a standalone worker", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{
							id: "gpt-5.6-sol",
							label: "GPT-5.6 Sol",
							isDefault: true,
							efforts: ["low", "high"],
							defaultEffort: "high",
						}],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		expect(await screen.findByRole("button", { name: "Effort" })).toHaveTextContent("High");
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({
					body: expect.objectContaining({ model: "gpt-5.6-sol", effort: "high" }),
				}),
			),
		);
	});

	it("sends the selected effort when starting a standalone worker", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5", label: "GPT-5", isDefault: true, efforts: ["high"] }],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		fireEvent.click(screen.getByLabelText("Agent"));
		const effort = await screen.findByRole("button", { name: "Effort" });
		await userEvent.click(effort);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({ body: expect.objectContaining({ effort: "high" }) }),
			),
		);
	});

	it("selects and sends a middle effort when the model reports levels but no default", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5", label: "GPT-5", isDefault: true, efforts: ["low", "medium", "high", "xhigh"] }],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		fireEvent.click(screen.getByLabelText("Agent"));
		const effort = await screen.findByRole("button", { name: "Effort" });
		expect(effort).toHaveTextContent(/^Medium$/);
		await userEvent.click(effort);
		expect(screen.getByRole("menuitemradio", { name: "Medium" })).toHaveAttribute("aria-checked", "true");
		await userEvent.keyboard("{Escape}");
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({ body: expect.objectContaining({ effort: "medium" }) }),
			),
		);
	});

	it("does not run provider readiness before Start", async () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Task")).toBeInTheDocument());
		expect(h.ensureReadiness).not.toHaveBeenCalled();
	});

	it("submits a gateway-backed Claude project when refreshed global readiness is unauthorized", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "claude-code", selectionMode: "text", models: [], allowCustom: true } };
			}
			return {
				data: {
					status: "ok",
					project: {
						agent: "claude-code",
						config: { env: { ANTHROPIC_BASE_URL: "https://gateway.example" } },
					},
				},
			};
		});
		const unauthorized = agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" });
		h.ensureTargetedReadiness.mockResolvedValueOnce({ agents: [unauthorized] });
		h.post.mockResolvedValueOnce({ data: { workerId: "worker-1" } });
		const onCreated = vi.fn();

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
		fireEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("worker-1"));
		expect(h.ensureTargetedReadiness).toHaveBeenCalledWith(["claude-code"], "launch", undefined);
	});

	it("keeps submission enabled for a gateway-backed Claude project when cached global readiness is unauthorized", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "claude-code", selectionMode: "text", models: [], allowCustom: true } };
			}
			return {
				data: {
					status: "ok",
					project: {
						agent: "claude-code",
						config: { env: { ANTHROPIC_BASE_URL: "https://gateway.example" } },
					},
				},
			};
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		h.agentCatalog = {
			agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })],
		};
		h.ensureTargetedReadiness.mockResolvedValueOnce({
			agents: [agentReadiness("claude-code", "Claude Code", { authentication: "authorized" })],
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "worker-1" } });

		render(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
		const submit = screen.getByRole("button", { name: "Start task" });
		expect(submit).toBeEnabled();
		fireEvent.click(submit);

		await waitFor(() => expect(h.ensureTargetedReadiness).toHaveBeenCalledWith(["claude-code"], "launch", undefined));
		await waitFor(() => expect(h.post).toHaveBeenCalled());
	});

	it("waits for project context before allowing a local task to start", async () => {
		let resolveProject!: (value: unknown) => void;
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true } };
			}
			if (path === "/api/v1/projects/{id}") {
				return new Promise((resolve) => {
					resolveProject = resolve;
				});
			}
			return { data: undefined };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(startTask()).toBeDisabled();
		expect(screen.queryByTestId("execution-context")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "should wait" } });
		fireEvent.keyDown(task(), { key: "Enter", shiftKey: false, altKey: false });
		expect(h.post).not.toHaveBeenCalled();

		await act(async () =>
			resolveProject({ data: { status: "ok", project: { name: "my-app", repo: "acme/my-app", defaultBranch: "main", path: "/repo", config: {} } } }),
		);
		await waitForTaskReady();
	});

	it.each([
		["binary launch failure", "AGENT_BINARY_NOT_FOUND"],
		["project credential rejection", "AGENT_AUTH_REQUIRED"],
	] as const)("waits for and caches targeted readiness after a %s", async (_name, errorCode) => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true } };
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		h.post.mockResolvedValueOnce({
			error: { code: errorCode, message: "Codex is not ready" },
		});
		const stale = agentReadiness("codex", "Codex", { freshness: "stale" });
		const completed = agentReadiness("codex", "Codex", { installation: "not_installed" });
		let finishReadiness!: (value: { agents: ReturnType<typeof agentReadiness>[] }) => void;
		h.ensureTargetedReadiness
			.mockResolvedValueOnce({ agents: [stale] })
			.mockReturnValueOnce(
			new Promise((resolve) => {
				finishReadiness = resolve;
			}),
			);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentReadinessQueryKey, { agents: [stale] });

		render(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "codex"));
		fireEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(h.ensureTargetedReadiness).toHaveBeenCalledTimes(2));
		expect(h.ensureTargetedReadiness).toHaveBeenLastCalledWith(["codex"], "launch", undefined);
		expect(screen.queryByText("Codex is not ready")).not.toBeInTheDocument();

		await act(async () => finishReadiness({ agents: [completed] }));
		expect(await screen.findByText("Codex is not ready")).toBeInTheDocument();
		expect(queryClient.getQueryData(agentReadinessQueryKey)).toEqual({ agents: [completed] });
	});

	it("starts a promptless worker when the task is empty", async () => {
		const onCreated = vi.fn();
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-empty" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);

		expect(task().getAttribute("placeholder")).toBeTruthy();
		expect(task()).toHaveClass("min-h-[calc(3lh+1.125rem)]");
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({ body: expect.objectContaining({ projectId: "proj-1", brief: "" }) }),
			),
		);
		expect(onCreated).toHaveBeenCalledWith("sess-empty");
	});

	it("keeps prompt guidance in the field instead of adding a separate footer row", () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(task().getAttribute("placeholder")).toBeTruthy();
		expect(screen.queryByText("Start now — details can come later.")).not.toBeInTheDocument();
		expect(screen.queryByText("Shift+Enter for a new line")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Investigate the failure" } });
		expect(task()).toHaveValue("Investigate the failure");
	});

	it("does not rerender the agent control for every prompt keystroke", async () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toBeInTheDocument());
		h.agentValues.length = 0;

		fireEvent.change(task(), { target: { value: "a" } });
		fireEvent.change(task(), { target: { value: "ab" } });
		fireEvent.change(task(), { target: { value: "abc" } });

		expect(h.agentValues).toHaveLength(1);
	});

	it("uses 2:2:1 toolbar tracks when the selected model advertises effort", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: {
					agent: "codex",
					selectionMode: "catalog",
					models: [{ id: "gpt-test", label: "GPT Test", isDefault: true, efforts: ["low", "high"] }],
					allowCustom: true,
				} };
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await screen.findByRole("button", { name: "Effort" });
		const runControls = screen.getByRole("group", { name: "Runs with" });
		expect(runControls).toHaveClass("composer-run-controls", "composer-run-controls-with-effort");
		expect(runControls.closest(".composer-toolbar")).not.toBeNull();
		expect(runControls.querySelectorAll(".composer-toolbar-slot")).toHaveLength(3);
		expect(screen.getByTestId("agent-field").closest(".composer-toolbar-slot")).not.toBeNull();
		expect(screen.getByLabelText("Model").closest(".composer-toolbar-slot")).not.toBeNull();
		expect(screen.getByLabelText("Effort").closest(".composer-toolbar-effort-slot")).not.toBeNull();
	});

	it("keeps the file attach control in the bottom action row", () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(screen.getByRole("button", { name: "Add file" }).closest(".composer-toolbar")).not.toBeNull();
	});

	it("emits busy state around an in-flight create and reports the new session", async () => {
		const onSubmittingChange = vi.fn();
		const onCreated = vi.fn();
		let resolveCreate!: (value: { data: { workerId: string } }) => void;
		h.post.mockReturnValueOnce(new Promise((resolve) => (resolveCreate = resolve)));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} onSubmittingChange={onSubmittingChange} />
			</Wrap>,
		);

		fireEvent.change(task(), { target: { value: "Do the thing" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(onSubmittingChange).toHaveBeenLastCalledWith(true));
		expect(h.post).toHaveBeenCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({
				body: expect.not.objectContaining({ attachments: expect.anything() }),
			}),
		);
		expect(h.post).toHaveBeenCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({
				body: expect.objectContaining({ projectId: "proj-1", brief: "Do the thing" }),
			}),
		);

		await act(async () => resolveCreate({ data: { workerId: "sess-1" } }));
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-1"));
		await waitFor(() => expect(onSubmittingChange).toHaveBeenLastCalledWith(false));
	});

	it("locks agent and model selection while task creation is in flight, then unlocks them after failure", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		let rejectCreate!: (error: Error) => void;
		h.post.mockReturnValueOnce(new Promise((_resolve, reject) => (rejectCreate = reject)));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const agent = await screen.findByTestId("agent-field");
		await waitFor(() => expect(agent).toHaveAttribute("data-value", "codex"));
		const model = await screen.findByRole("button", { name: "Model" });
		const prompt = task();
		expect(agent).toBeEnabled();
		expect(model).toBeEnabled();
		expect(prompt).toBeEnabled();

		fireEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(agent).toBeDisabled();
		expect(model).toBeDisabled();
		expect(prompt).toBeDisabled();

		await act(async () => rejectCreate(new Error("creation failed")));
		await screen.findByText("creation failed");
		expect(agent).toBeEnabled();
		expect(model).toBeEnabled();
		expect(prompt).toBeEnabled();
	});

	it.each([
		{
			name: "mode",
			catalog: {
				agent: "codex",
				selectionMode: "mode",
				models: [{ id: "plan", label: "Plan", isDefault: true }],
				customModelEntry: "none",
				allowCustom: false,
			},
			controls: async () => [await screen.findByRole("button", { name: "Model" })],
		},
		{
			name: "catalog",
			catalog: {
				agent: "codex",
				selectionMode: "catalog",
				models: [{ id: "gpt-5", label: "GPT-5", isDefault: true }],
				customModelEntry: "none",
				allowCustom: false,
			},
			controls: async () => [await screen.findByRole("button", { name: "Model" })],
		},
	])("locks the $name selector while creating and restores it after failure", async ({ catalog, controls }) => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: catalog };
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		let rejectCreate!: (error: Error) => void;
		h.post.mockReturnValueOnce(new Promise((_resolve, reject) => (rejectCreate = reject)));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent(catalog.models[0].label));
		const modelControls = await controls();
		for (const control of modelControls) expect(control).toBeEnabled();

		fireEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		for (const control of modelControls) expect(control).toBeDisabled();

		await act(async () => rejectCreate(new Error("creation failed")));
		await screen.findByText("creation failed");
		for (const control of modelControls) expect(control).toBeEnabled();
	});

	it("attaches a selected file and sends it in the delegate body", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });

		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const file = new File([new Uint8Array([1, 2, 3])], "notes.txt", { type: "text/plain" });
		fireEvent.change(input, { target: { files: [file] } });

		expect(await screen.findByText("notes.txt")).toBeInTheDocument();

		fireEvent.change(task(), { target: { value: "Use the notes" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		const body = h.post.mock.calls[0][1].body as {
			attachments?: Array<{ mimeType: string; data: string }>;
		};
		expect(h.post.mock.calls[0][1].headers).toEqual({ "X-AO-Attachment-Upload": "1" });
		expect(body.attachments).toHaveLength(1);
		expect(body.attachments?.[0].mimeType).toBe("text/plain");
		expect(body.attachments?.[0].data.length).toBeGreaterThan(0);
	});









	it("waits for a selected file read before submitting", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });
		let finishRead!: () => void;
		class SlowFileReader {
			error: Error | null = null;
			result: string | ArrayBuffer | null = null;
			onerror: (() => void) | null = null;
			onload: (() => void) | null = null;

			readAsDataURL(file: File) {
				finishRead = () => {
					this.result = `data:${file.type};base64,AQID`;
					this.onload?.();
				};
			}
		}
		vi.stubGlobal("FileReader", SlowFileReader);

		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		fireEvent.change(input, {
			target: { files: [new File([new Uint8Array([1, 2, 3])], "slow.txt", { type: "text/plain" })] },
		});
		fireEvent.change(task(), { target: { value: "Use the slow file" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		expect(h.post).not.toHaveBeenCalled();

		await act(async () => finishRead());
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({
			attachments: [{ mimeType: "text/plain", data: "AQID" }],
		});
	});

	it("waits for both rapidly selected file batches before delegating", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });
		const pendingReads: Array<() => void> = [];
		class SlowFileReader {
			error: Error | null = null;
			result: string | ArrayBuffer | null = null;
			onerror: (() => void) | null = null;
			onload: (() => void) | null = null;

			readAsDataURL(file: File) {
				pendingReads.push(() => {
					this.result = `data:${file.type};base64,${file.name === "first.txt" ? "AQ==" : "Ag=="}`;
					this.onload?.();
				});
			}
		}
		vi.stubGlobal("FileReader", SlowFileReader);
		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		fireEvent.change(input, {
			target: { files: [new File([new Uint8Array([1])], "first.txt", { type: "text/plain" })] },
		});
		fireEvent.change(input, {
			target: { files: [new File([new Uint8Array([2])], "second.txt", { type: "text/plain" })] },
		});
		fireEvent.change(task(), { target: { value: "Use both files" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));
		expect(h.post).not.toHaveBeenCalled();

		await act(async () => pendingReads.shift()?.());
		await waitFor(() => expect(pendingReads).toHaveLength(1));
		expect(h.post).not.toHaveBeenCalled();
		await act(async () => pendingReads.shift()?.());

		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({
			attachments: [
				{ mimeType: "text/plain", data: "AQ==" },
				{ mimeType: "text/plain", data: "Ag==" },
			],
		});
	});
});
