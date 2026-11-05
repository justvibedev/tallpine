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
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
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
import { cacheAgentModels, clearCachedAgentModels } from "../lib/agent-model-cache";
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
	function mockDisabledModels(disabledModels: string[]) {
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "tui", chatHarnesses: [], harnessDefaults: { codex: { disabledModels } } } };
			}
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-default", label: "GPT Default", isDefault: true },
							{ id: "gpt-other", label: "GPT Other" },
						],
						allowCustom: false,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: { worker: { agent: "codex" } } } } };
		});
	}

	it("removes a selected file before submitting", async () => {
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
		fireEvent.click(screen.getByRole("button", { name: "Remove notes.txt" }));
		await waitFor(() => expect(screen.queryByText("notes.txt")).not.toBeInTheDocument());

		fireEvent.change(task(), { target: { value: "No attachment now" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].headers).toBeUndefined();
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("attachments");
	});

	it("clears busy state when a create rejects", async () => {
		const onSubmittingChange = vi.fn();
		h.post.mockRejectedValueOnce(new Error("nope"));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} onSubmittingChange={onSubmittingChange} />
			</Wrap>,
		);

		fireEvent.change(task(), { target: { value: "B" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(screen.getByText("nope")).toBeInTheDocument());
		await waitFor(() => expect(onSubmittingChange).toHaveBeenLastCalledWith(false));
	});

	it("silently routes agents without Chat support to Terminal UI", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "chat", chatHarnesses: ["codex"] } };
			}
			if (path.includes("/models")) {
				return {
					data: {
						agent: "grok",
						selectionMode: "catalog",
						models: [{ id: "grok-4.6", label: "grok-4.6", isDefault: true }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "grok", config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-grok" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("grok-4.6");
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Do the thing" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({ body: expect.objectContaining({ agent: "grok", mode: "tui" }) }),
		));
	});

	it("preserves Codex effort when retrying in Terminal UI", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "chat", chatHarnesses: ["codex"] } };
			}
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{
							id: "gpt-test",
							label: "GPT Test",
							isDefault: true,
							efforts: ["low", "high"],
						}],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		h.post
			.mockResolvedValueOnce({ error: { code: "CHAT_DRIVER_UNAVAILABLE" } })
			.mockResolvedValueOnce({ data: { workerId: "sess-tui" } });
		const onCreated = vi.fn();

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);
		await userEvent.click(await screen.findByRole("button", { name: "Effort" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "High" }));
		fireEvent.change(task(), { target: { value: "Do the thing" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		const fallback = await screen.findByRole("button", { name: "Create as Terminal UI" });
		fireEvent.click(fallback);
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-tui"));
		expect(h.post).toHaveBeenLastCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({ body: expect.objectContaining({ effort: "high", mode: "tui" }) }),
		);
	});

	it("offers an explicit approval-less retry from structured capability details", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "cursor", selectionMode: "text", models: [], allowCustom: true } };
			}
			return { data: { status: "ok", project: { agent: "cursor", config: {} } } };
		});
		h.post
			.mockResolvedValueOnce({
				error: {
					code: "SESSION_MODE_UNSUPPORTED",
					message: "This provider cannot satisfy the selected approval policy",
					details: {
						missingCapabilities: ["approvals"],
						allowedApprovalModes: ["bypass-permissions"],
					},
				},
			})
			.mockResolvedValueOnce({ data: { workerId: "sess-pi" } });
		const onCreated = vi.fn();

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "cursor"));
		fireEvent.change(task(), { target: { value: "Use approval-less Chat" } });
		fireEvent.click(screen.getByText("Start task"));

		const fallback = await screen.findByRole("button", { name: "Start without approvals" });
		fireEvent.click(fallback);
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-pi"));
		expect(h.post).toHaveBeenLastCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({
				body: expect.objectContaining({ approvalMode: "bypass-permissions" }),
			}),
		);
		expect(h.post.mock.calls[1][1].body).not.toHaveProperty("mode");
	});

	it("reports a standalone create failure", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.post.mockResolvedValue({ error: { code: "SESSION_CREATE_FAILED", message: "no session" } });
		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		fireEvent.change(task(), { target: { value: "Say hello" } });
		fireEvent.click(screen.getByText("Start task"));
		expect(await screen.findByRole("alert")).toHaveTextContent("no session");
	});

	it("reports dirty then clears it on unmount", () => {
		const onDirtyChange = vi.fn();
		const { unmount } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} onDirtyChange={onDirtyChange} />
			</Wrap>,
		);
		fireEvent.change(task(), { target: { value: "T" } });
		expect(onDirtyChange).toHaveBeenLastCalledWith(true);
		unmount();
		expect(onDirtyChange).toHaveBeenLastCalledWith(false);
	});

	it("preselects the project worker agent and spawns with it", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true } };
			}
			return {
				data: { status: "ok", project: { agent: "claude-code", config: { worker: { agent: "codex" } } } },
			};
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-3" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "codex"));

		fireEvent.change(task(), { target: { value: "Ship it" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({ body: expect.objectContaining({ agent: "codex" }) }),
			),
		);
	});

	it("renders a known default agent without an empty intermediate selection", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol", isDefault: true }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(["project", "proj-1"], { agent: "codex", config: {} });

		render(
			<QueryClientProvider client={queryClient}>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</QueryClientProvider>,
		);

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.6 Sol"));
		expect(h.agentValues).not.toContain("");
	});

	it("falls back to the global default agent when the project sets no worker agent", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "claude-code", selectionMode: "text", models: [], allowCustom: true } };
			}
			return { data: { status: "ok", project: { agent: "claude-code", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
	});

	it("exposes effort for any harness whose selected model advertises it", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "claude-code",
						selectionMode: "catalog",
						models: [{ id: "sonnet", label: "Sonnet", isDefault: true, efforts: ["low", "high"] }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "claude-code", config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "claude-worker" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("Sonnet");
		expect(await screen.findByRole("button", { name: "Effort" })).toBeInTheDocument();
	});

	it("omits the repeated Claude prefix from Claude Code model labels", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "claude-code",
						selectionMode: "catalog",
						models: [
							{ id: "claude-sonnet-4-5", label: "Claude Sonnet 4.5", isDefault: true },
							{ id: "claude-opus-4-7", label: "Claude Opus 4.7" },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "claude-code", config: {} } } };
		});

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("Sonnet 4.5");
		expect(picker).not.toHaveTextContent("Claude");

		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "Sonnet 4.5" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Opus 4.7" })).toBeInTheDocument();
		expect(screen.queryByText(/^Claude /)).not.toBeInTheDocument();
	});

	it("preselects the agent's default model when the project configures none", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [
							{ id: "gpt-5", label: "GPT-5" },
							{ id: "gpt-5-codex", label: "GPT-5 Codex", isDefault: true },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT-5 Codex");
	});

	it.each([
		{ name: "the newest Opus when nothing was run", agent: "claude-code", lastUsed: {}, expected: "Opus 5.5" },
		{ name: "the most recently run model, ahead of Opus", agent: "claude-code", lastUsed: { "claude-fable-5-1": "2026-10-01T12:00:00Z", "claude-sonnet-5-5": "2026-10-05T12:00:00Z" }, expected: "Sonnet 5.5" },
		{ name: "the project model, ahead of the last run", agent: "claude-code", lastUsed: { "claude-sonnet-5-5": "2026-10-05T12:00:00Z" }, projectModel: "claude-opus-4-8", expected: "Opus 4.8" },
		{ name: "no model for another agent", agent: "codex", lastUsed: {}, expected: "Select model" },
	])("opens $agent on $name", async ({ agent, lastUsed, projectModel, expected }) => {
		const models = ["fable-5-1", "opus-4-8", "opus-5-5", "sonnet-5-5"].map((slug) => ({
			id: `claude-${slug}`,
			label: `Claude ${slug.replace(/^(\w)/, (c) => c.toUpperCase()).replace(/-(\d)-(\d)/, " $1.$2")}`,
			lastUsedAt: (lastUsed as Record<string, string>)[`claude-${slug}`],
		}));
		h.get.mockImplementation(async (path: string) =>
			path.includes("/models")
				? { data: { agent, selectionMode: "text", models, allowCustom: false } }
				: { data: { status: "ok", project: { agent, config: { worker: { agent, agentConfig: projectModel ? { model: projectModel } : {} } } } } },
		);

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent(expected);
	});

	it("shows the saved frontend models immediately and updates the picker from the DB without a loader", async () => {
		const cached = {
			agentId: "codex", selectionMode: "catalog" as const, customModelEntry: "none" as const,
			models: [{ id: "cached-model", label: "GPT Cached", isDefault: true }],
			allowCustom: false, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z",
		};
		cacheAgentModels(cached, "codex", "");
		const updated = { ...cached, models: [{ id: "fresh-model", label: "GPT Fresh", isDefault: true }] };
		let finishDB!: (value: { data: typeof cached }) => void;
		const dbResponse = new Promise<{ data: typeof cached }>((resolve) => { finishDB = resolve; });
		h.get.mockImplementation(async (path: string, options?: { params?: { query?: { catalogOnly?: boolean } } }) => {
			if (path.includes("/models")) return options?.params?.query?.catalogOnly ? dbResponse : { data: updated };
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(["project", "proj-1"], { agent: "codex", config: {} });
		const view = render(<Wrap queryClient={queryClient}><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		try {
			expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT Cached");
			expect(screen.queryByRole("status", { name: "Loading models…" })).not.toBeInTheDocument();
			await act(async () => finishDB({ data: updated }));
			await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT Fresh"));
			expect(screen.queryByRole("status", { name: "Loading models…" })).not.toBeInTheDocument();
		} finally {
			finishDB({ data: updated });
			view.unmount();
			queryClient.clear();
		}
	});

	it("keeps the picker visible without a loader while a new agent's DB catalog resolves", async () => {
		let resolveClaudeCatalog!: (value: {
			data: {
				agent: string;
				selectionMode: "text";
				models: Array<{ id: string; label: string; isDefault: boolean }>;
				allowCustom: boolean;
			};
		}) => void;
		h.get.mockImplementation(async (path: string, request?: { params?: { path?: { agent?: string } } }) => {
			if (path.includes("/models")) {
				if (request?.params?.path?.agent === "claude-code") {
					return new Promise((resolve) => {
						resolveClaudeCatalog = resolve;
					});
				}
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol", isDefault: true }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.6 Sol");
		fireEvent.click(screen.getByTestId("agent-field"));

		expect(screen.getByLabelText("Model")).not.toHaveTextContent("GPT-5.6 Sol");
		expect(screen.queryByRole("status", { name: "Loading models…" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Select model");

		await act(async () => {
			resolveClaudeCatalog({
				data: {
					agent: "claude-code",
					selectionMode: "text",
					models: [{ id: "opus[1m]", label: "opus[1m]", isDefault: true }],
					allowCustom: true,
				},
			});
		});
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("opus[1m]");
	});

	it("does not invent a catalog default when none is reported", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{ id: "gpt-5", label: "GPT-5" }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("Select model");

		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "GPT-5" })).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-unreported-model" } });
		fireEvent.change(task(), { target: { value: "Use the provider's model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalled());
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("model");
	});

	it("shows Memcode's native model list and sends an explicit choice", async () => {
		h.agentCatalog = { agents: [agentReadiness("memcode", "memcode", { authentication: "configured" })] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: { agentId: "memcode", selectionMode: "catalog", models: [{ id: "sonnet", label: "Sonnet 5" }, { id: "opus", label: "Opus 5.5" }], customModelEntry: "none", allowCustom: false } };
			return { data: { status: "ok", project: { agent: "memcode", config: { worker: { agent: "memcode" } } } } };
		});

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

		const picker = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "Sonnet 5" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Opus 5.5" }));
		expect(picker).toHaveTextContent("Opus 5.5");
		h.post.mockResolvedValueOnce({ data: { workerId: "memcode-task" } });
		fireEvent.change(task(), { target: { value: "Use the native model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalled());
		expect(h.post.mock.calls[0][1].body).toHaveProperty("model", "opus");
	});

	it("inherits the project worker model without saving it as a task override", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-5", label: "GPT-5" },
							{ id: "gpt-5-codex", label: "GPT-5 Codex", isDefault: true },
						],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						agent: "codex",
						config: { worker: { agent: "codex", agentConfig: { model: "gpt-5" } } },
					},
				},
			};
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-default-model" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT-5");
		fireEvent.change(task(), { target: { value: "Use project default model" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).toEqual(expect.objectContaining({ agent: "codex" }));
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("model");
		expect(JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex.model).toBe("");
	});

	it("forwards model refresh metadata to the task picker", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{ id: "gpt-5", label: "GPT-5", isDefault: true }],
						allowCustom: true,
						refreshState: "error",
						refreshError: "Provider temporarily unavailable",
						retryAt: "2026-09-23T08:05:00Z",
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await userEvent.click(await screen.findByRole("button", { name: "Model" }));
		expect(await screen.findByTitle("Provider temporarily unavailable")).toBeInTheDocument();
	});

	it("does not render free text when models must be configured in the agent", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agentId: "opencode",
						selectionMode: "catalog",
						models: [],
						customModelEntry: "configured",
						allowCustom: false,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "opencode", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const picker = await screen.findByRole("button", { name: "Model" });
		expect(screen.queryByRole("textbox", { name: "Model" })).not.toBeInTheDocument();
		await userEvent.click(picker);
		expect(screen.getByText("Configure the model in opencode, then refresh.")).toBeInTheDocument();
	});

	it("uses the project worker model as the new task model default", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-5", label: "GPT-5" },
							{ id: "gpt-5.1", label: "GPT-5.1" },
						],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						config: { worker: { agent: "codex", agentConfig: { model: "gpt-5" } } },
					},
				},
			};
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-2" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const model = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(model);
		expect(screen.queryByRole("menuitem", { name: "Use agent model" })).not.toBeInTheDocument();
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "GPT-5.1" }));
		fireEvent.change(task(), { target: { value: "Use the selected model" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({
					body: expect.objectContaining({ model: "gpt-5.1" }),
				}),
			),
		);
	});

	it("opens on an offered model when Settings hides the catalog default", async () => {
		mockDisabledModels(["gpt-default"]);
		h.post.mockResolvedValue({ data: { workerId: "sess-offered" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await waitFor(() => expect(model).toHaveTextContent("GPT Other"));
		await userEvent.click(model);
		expect(screen.queryByRole("menuitem", { name: "GPT Default" })).not.toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		fireEvent.change(task(), { target: { value: "Use an offered model" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({ model: "gpt-other" });
	});

	it("keeps the composer usable when the catalog response has no model list", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { status: "ok" } }
			: { data: { status: "ok", project: { config: { worker: { agent: "codex" } } } } });
		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.get).toHaveBeenCalledWith("/api/v1/agents/{agent}/models", expect.anything()));
		expect(task()).toBeVisible();
		fireEvent.change(task(), { target: { value: "Use the native model" } });
		expect(task()).toHaveValue("Use the native model");
	});

	it.each([false, true])("keeps FX's native provider when no default is reported (unrelated model hidden: %s)", async (hideUnrelated) => {
		h.agentCatalog = { agents: [agentReadiness("fx", "FX")] };
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "tui", chatHarnesses: [], harnessDefaults: { fx: {
					disabledModels: hideUnrelated ? ["fx:gateway:beta"] : [],
				} } } };
			}
			if (path.includes("/models")) {
				return { data: { agent: "fx", selectionMode: "catalog", allowCustom: true, refreshRecommended: false, models: [
					{ id: "fx:codex:alpha", label: "Codex Alpha", provider: "codex" },
					{ id: "fx:gateway:beta", label: "Gateway Beta", provider: "gateway" },
					{ id: "fx:gateway:zeta", label: "Gateway Zeta", provider: "gateway" },
				] } };
			}
			return { data: { status: "ok", project: { config: { worker: { agent: "fx" } } } } };
		});
		h.post.mockResolvedValue({ data: { workerId: "sess-fx-native" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "fx"));
		const picker = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "Gateway Zeta" })).toBeInTheDocument();
		if (hideUnrelated) {
			expect(screen.queryByRole("menuitem", { name: "Gateway Beta" })).not.toBeInTheDocument();
		} else {
			expect(screen.getByRole("menuitem", { name: "Gateway Beta" })).toBeInTheDocument();
		}
		await userEvent.keyboard("{Escape}");
		expect(picker).toHaveTextContent("Select model");
		fireEvent.change(task(), { target: { value: "Use FX native Gateway selection" } });
		await waitForTaskReady();
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({ agent: "fx" });
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("model");
	});

	it("keeps the catalog default when Settings switches off no model", async () => {
		mockDisabledModels([]);
		h.post.mockResolvedValue({ data: { workerId: "sess-all" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await waitFor(() => expect(model).toHaveTextContent("GPT Default"));
		fireEvent.change(task(), { target: { value: "Use the default model" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({ model: "gpt-default" });
	});

	it("inherits worker effort visually but sends only explicit task overrides", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{
							id: "gpt-test",
							label: "GPT Test",
							isDefault: true,
							efforts: ["default", "low", "high"],
							defaultEffort: "low",
						}],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return {
				data: { status: "ok", project: { config: { worker: { agent: "codex", agentConfig: {
					model: "gpt-test", effort: "high",
				} } } } },
			};
		});
		h.post.mockResolvedValue({ data: { workerId: "sess-tuned" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("GPT Test");
		const effortPicker = await screen.findByRole("button", { name: "Effort" });
		expect(effortPicker).toHaveTextContent("High");

		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("effort");

		await userEvent.click(effortPicker);
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "Low" }));
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(2));
		expect(h.post.mock.calls[1][1].body).toEqual(expect.objectContaining({ effort: "low" }));

		await userEvent.click(effortPicker);
		expect(screen.queryByRole("menuitem", { name: "Default" })).not.toBeInTheDocument();
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "High" }));
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(3));
		expect(h.post.mock.calls[2][1].body).not.toHaveProperty("effort");
		expect(JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex).not.toHaveProperty("effort");
	});

	it("returns to the reported agent effort without pinning it", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true, efforts: ["low", "high"], defaultEffort: "low" },
			], allowCustom: true } }
			: { data: { status: "ok", project: { config: { worker: { agent: "codex" } } } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-tuned" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Effort" });
		await waitFor(() => expect(picker).toHaveTextContent("Low"));
		await userEvent.click(picker);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		await userEvent.click(picker);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "Low" }));
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("effort");
		expect(JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex).not.toHaveProperty("effort");
	});

	it("offers only concrete effort choices when the agent reports no preferred level", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true, efforts: ["low", "high"] },
			], allowCustom: true } }
			: { data: { status: "ok", project: { config: { worker: { agent: "codex" } } } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-tuned" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Effort" });
		await userEvent.click(picker);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		await userEvent.click(picker);
		expect(screen.queryByRole("menuitem", { name: "Use agent effort" })).not.toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).toEqual(expect.objectContaining({ effort: "high" }));
	});

	it("shows a stored implicit model as the catalog choice without pinning it", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [{ id: "gpt-test", label: "GPT Test", isDefault: true }], allowCustom: true } }
			: { data: { status: "ok", project: { config: { worker: { agent: "codex", agentConfig: { model: "default" } } } } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT Test");
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("model");
		expect(JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex.model).toBe("");
	});

	it.each([
		{ selectionMode: "catalog", preferred: "GPT New", alternative: "GPT Old", preferenceField: "model", expectedModel: "preferred" },
		{ selectionMode: "mode", preferred: "Plan", alternative: "Fast", preferenceField: "mode", expectedModel: undefined },
	] as const)("returns $selectionMode to the provider's choice after a custom selection", async ({ selectionMode, preferred, alternative, preferenceField, expectedModel }) => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode, models: [
				{ id: "preferred", label: preferred, isDefault: true },
				{ id: "alternative", label: alternative },
			], allowCustom: selectionMode === "catalog" } }
			: { data: { status: "ok", project: { agent: "codex", config: {} } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent(preferred));
		const picker = screen.getByRole("button", { name: "Model" });
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: alternative }));
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body.model).toBe("alternative");

		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: preferred }));
		expect(picker).toHaveTextContent(preferred);
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(2));
		expect(h.post.mock.calls[1][1].body.model).toBe(expectedModel);
		const saved = JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex;
		expect(saved[preferenceField]).toBe(expectedModel ?? "");
	});

	it("overrides an explicitly configured project model when choosing the provider's model", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-new", label: "GPT New", isDefault: true },
				{ id: "gpt-old", label: "GPT Old" },
			], allowCustom: true } }
			: { data: { status: "ok", project: { agent: "codex", config: { worker: { agent: "codex", agentConfig: { model: "gpt-old" } } } } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("GPT Old");
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT New" }));
		expect(picker).toHaveTextContent("GPT New");
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body.model).toBe("gpt-new");
	});

	it("clears a mode override when the agent reports no preferred mode", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "mode", models: [{ id: "fast", label: "Fast" }], allowCustom: false } }
			: { data: { status: "ok", project: { agent: "codex", config: {} } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Mode not reported"));
		const picker = screen.getByRole("button", { name: "Model" });
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Fast" }));
		expect(picker).toHaveTextContent("Fast");
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Use agent mode" }));
		expect(picker).toHaveTextContent("Mode not reported");
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("model");
	});

	describe("model login errors", () => {
		const expiredDetail = "claude-code model discovery: claude-code: model discovery: Anthropic rejected the credential: OAuth access token has expired. Re-authenticate to continue.";
		function claudeCatalog(extra: Record<string, unknown>) {
			return {
				agent: "claude-code",
				selectionMode: "catalog",
				models: [
					{ id: "sonnet", label: "Sonnet 5.5" },
					{ id: "opus", label: "Opus 5.5" },
				],
				allowCustom: true,
				stale: true,
				...extra,
			};
		}
		function serveClaude(catalog: () => Record<string, unknown>) {
			h.get.mockImplementation(async (path: string) => path.includes("/models")
				? { data: catalog() }
				: { data: { status: "ok", project: { agent: "claude-code", config: {} } } });
		}

		afterEach(() => {
			useUiStore.setState({ settingsModal: null });
		});

		it("replaces the raw provider error with a login shortcut and an inline retry", async () => {
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })] };
			serveClaude(() => claudeCatalog({ warning: expiredDetail, warningCode: "auth_required" }));
			h.post.mockImplementation(async (path: string) => path === "/api/v1/agents/{agent}/models/refresh"
				? { data: claudeCatalog({ stale: false }) }
				: { data: {} });

			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

			const notice = await screen.findByTestId("model-catalog-notice");
			expect(notice).toHaveTextContent("Claude Code isn't logged in on this computer, so its models couldn't load.");
			expect(screen.queryByText(expiredDetail)).not.toBeInTheDocument();
			await userEvent.click(screen.getByRole("button", { name: "Details" }));
			expect(screen.getByText(expiredDetail)).toBeInTheDocument();

			await userEvent.click(screen.getByRole("button", { name: "Log in" }));
			expect(useUiStore.getState().settingsModal).toEqual(expect.objectContaining({
				scope: "global", section: "harness", focusAgentId: "claude-code", startLogin: true,
			}));

			await userEvent.click(screen.getByRole("button", { name: "Retry" }));
			await waitFor(() => expect(h.post).toHaveBeenCalledWith("/api/v1/agents/{agent}/models/refresh", expect.objectContaining({
				params: expect.objectContaining({ path: { agent: "claude-code" } }),
			})));
			await waitFor(() => expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument());
		});

		it("shows no notice for an expired login that Claude Code renews on its own", async () => {
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "configured" })] };
			serveClaude(() => claudeCatalog({ warning: "the saved Claude Code login token expired", warningCode: "auth_expired" }));
			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			await waitForTaskReady();
			expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument();
			expect(screen.queryByText("the saved Claude Code login token expired")).not.toBeInTheDocument();
		});

		it("recognizes a login error from a daemon that predates warning codes", async () => {
			serveClaude(() => claudeCatalog({ warning: expiredDetail }));
			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			expect(await screen.findByTestId("model-catalog-notice")).toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Log in" })).toBeInTheDocument();
		});

		it("keeps other model warnings as plain text", async () => {
			serveClaude(() => claudeCatalog({ warning: "provider temporarily unavailable" }));
			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			expect(await screen.findByText("provider temporarily unavailable")).toBeInTheDocument();
			expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument();
		});

		it("re-reads the catalog by itself once the agent reads as logged in", async () => {
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })] };
			let loggedIn = false;
			serveClaude(() => loggedIn ? claudeCatalog({ stale: false }) : claudeCatalog({ warning: expiredDetail, warningCode: "auth_required" }));
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const view = render(<Wrap queryClient={queryClient}><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			await screen.findByTestId("model-catalog-notice");

			loggedIn = true;
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "authorized" })] };
			view.rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			await waitFor(() => expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument());
		});

		it("re-reads the catalog when the window regains focus while a login error shows", async () => {
			let loggedIn = false;
			serveClaude(() => loggedIn ? claudeCatalog({ stale: false }) : claudeCatalog({ warning: expiredDetail, warningCode: "auth_required" }));
			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			await screen.findByTestId("model-catalog-notice");
			loggedIn = true;
			act(() => { window.dispatchEvent(new Event("focus")); });
			await waitFor(() => expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument());
		});
	});
});
