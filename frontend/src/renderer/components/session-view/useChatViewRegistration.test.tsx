import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, postMock, sources } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	sources: [] as Array<{ onopen: (() => void) | null }>,
}));

vi.mock("../../lib/host-clients", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../lib/host-clients")>()),
	clientForSessionHost: () => ({ GET: getMock, POST: postMock }),
	baseUrlForHost: () => undefined,
	subscribeConnectedHosts: () => () => {},
}));
vi.mock("../../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../lib/api-client")>()),
	getApiBaseUrl: () => "http://127.0.0.1:3101",
	hasTrustedApiBaseUrl: () => true,
	subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/event-stream-socket", () => ({
	openEventStream: () => {
		const source = { onopen: null, onerror: null, readyState: 0, addEventListener: () => {}, close: () => {} };
		sources.push(source);
		return source;
	},
}));

import { useConversation, useWorkspaceFilePaths } from "../../hooks/conversation";
import { useChatViewRegistration } from "./useChatViewRegistration";

let controllerStates: string[] = [];
const wireSnapshot = (controller: string) => ({
	conversationId: "c1", sessionId: "orch-1", harness: "codex", mode: "chat", controller,
	latestSequence: 0, oldestSequence: 1, hasMoreBefore: false, messages: [], activities: [], turns: [], settings: {},
});

/** The first-open data hooks of an orchestrator chat, wired as SessionView wires them. */
function OrchestratorOpen() {
	const { snapshot } = useConversation("orch-1");
	useWorkspaceFilePaths("orch-1", Boolean(snapshot));
	useChatViewRegistration("orch-1", undefined, true);
	return null;
}

const gets = (suffix: string) => getMock.mock.calls.filter(([path]) => String(path).endsWith(suffix)).length;
const settle = () => new Promise((resolve) => setTimeout(resolve, 400));

describe("opening a fresh orchestrator", () => {
	beforeEach(() => {
		sources.length = 0;
		vi.stubGlobal("EventSource", class {});
		getMock.mockReset().mockImplementation(async (path: string) => {
			await new Promise((resolve) => setTimeout(resolve, 20));
			return path.endsWith("/conversation")
				? { data: wireSnapshot(controllerStates.shift() ?? "ready") }
				: { data: { files: [{ path: "a.go", status: "modified" }], truncated: false } };
		});
		// The wake outlives the first snapshot, so chat-view resolves after it lands.
		postMock.mockReset().mockImplementation(() => new Promise((resolve) => setTimeout(() => resolve({ error: undefined }), 150)));
	});
	afterEach(() => vi.unstubAllGlobals());

	async function open() {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><OrchestratorOpen /></QueryClientProvider>);
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(sources.length).toBeGreaterThan(0));
		sources[0]!.onopen?.(); // the workspace event stream connects after the first load
		await settle();
		return client;
	}

	it("reads the conversation once and workspace/files once for a live orchestrator", async () => {
		controllerStates = ["ready"];
		await open();

		expect(gets("/conversation")).toBe(1);
		expect(gets("/workspace/files")).toBe(1);
	});

	it("refreshes once after the wake when the first snapshot was taken while hibernated", async () => {
		controllerStates = ["hibernated", "ready"];
		const client = await open();

		expect(gets("/conversation")).toBe(2);
		expect(gets("/workspace/files")).toBe(1);
		const pages = client.getQueryData<{ pages: Array<{ controller: { state: string } }> }>(["conversation", "orch-1"]);
		expect(pages?.pages[0]?.controller.state).toBe("ready");
	});
});
