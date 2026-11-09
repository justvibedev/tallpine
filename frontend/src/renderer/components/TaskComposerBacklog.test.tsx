import {
	h,
	Wrap,
	task,
	startTask,
	waitForTaskReady,
} from "./TaskComposer.test.setup";

import { fireEvent, render, waitFor } from "@testing-library/react";
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

describe("TaskComposer on a backlog item", () => {
	const setupRemote = (delegateResult: unknown = { data: { workerId: "tallpine-9" } }) => {
		h.agentCatalog = { agents: [agentReadiness("opencode", "OpenCode")] };
		h.remoteGet.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: { agent: "opencode", selectionMode: "text", models: [], allowCustom: true } };
			if (path === "/api/v1/settings") return { data: { defaultSessionMode: "chat", chatHarnesses: ["opencode"] } };
			return { data: { status: "ok", project: { id: "project-a", config: { worker: { agent: "opencode" } } } } };
		});
		h.remotePost.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: { agents: [agentReadiness("opencode", "OpenCode")] } };
			if (path === "/api/v1/projects/{id}/tasks/prepare") return { data: { taskPreparation: "" } };
			if (path === "/api/v1/orchestrators/delegate") return delegateResult;
			return { data: {} };
		});
	};
	const delegateBody = () => h.remotePost.mock.calls.find(([path]) => path === "/api/v1/orchestrators/delegate")?.[1].body;

	it("prefills the brief and sends the item ref with the delegation", async () => {
		setupRemote();
		const onCreated = vi.fn();
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" backlog={{ ref: "B12", title: "Fix the leak" }} onCreated={onCreated} /></Wrap>);
		await waitForTaskReady();
		expect(task()).toHaveValue("B12: Fix the leak");
		fireEvent.click(startTask());
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("tallpine-9"));
		expect(delegateBody()).toMatchObject({ brief: "B12: Fix the leak", backlogRef: "B12" });
		expect(delegateBody()).not.toHaveProperty("backlogForce");
	});

	it("sends backlogForce for a held item the user chose to start anyway", async () => {
		setupRemote();
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" backlog={{ ref: "B7", title: "Held", force: true }} onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.click(startTask());
		await waitFor(() => expect(delegateBody()).toMatchObject({ backlogRef: "B7", backlogForce: true }));
	});

	it("sends backlogAllowBlocked, not backlogForce, for a blocked item the user chose to start anyway", async () => {
		setupRemote();
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" backlog={{ ref: "B8", title: "Chained", allowBlocked: true }} onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.click(startTask());
		await waitFor(() => expect(delegateBody()).toMatchObject({ backlogRef: "B8", backlogAllowBlocked: true }));
		expect(delegateBody()).not.toHaveProperty("backlogForce");
	});

	it("shows the server's warnings after the worker starts", async () => {
		setupRemote({ data: { workerId: "tallpine-9", warnings: ["B12 is blocked by B3"] } });
		useUiStore.getState().clearGlobalToast();
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" backlog={{ ref: "B12", title: "Fix" }} onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.click(startTask());
		await waitFor(() => expect(useUiStore.getState().globalToasts.map((toast) => toast.title)).toContain("B12 is blocked by B3"));
	});

	it("sends no backlog fields for an ordinary task", async () => {
		setupRemote();
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.change(task(), { target: { value: "Fix it" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(delegateBody()).toMatchObject({ brief: "Fix it" }));
		expect(delegateBody()).not.toHaveProperty("backlogRef");
	});
});

