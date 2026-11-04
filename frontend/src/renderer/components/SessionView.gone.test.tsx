// Drives SessionView over the real workspace/session queries, mocking only the
// fetch layer, to pin when an open session counts as gone (ended lane) and when
// it does not (a worker that terminated and still resolves by id).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionView } from "./SessionView";
import { SessionTopbarProvider } from "./SessionTopbarPortal";
import { TooltipProvider } from "./ui/tooltip";

const h = vi.hoisted(() => ({
	navigate: vi.fn(),
	live: [] as Array<Record<string, unknown>>,
	byId: new Map<string, { status: number; session?: Record<string, unknown> }>(),
	byIdCalls: [] as string[],
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => h.navigate, useBlocker: () => undefined }));
vi.mock("../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/api-client")>()),
	hasTrustedApiBaseUrl: () => true,
	apiClient: {
		GET: vi.fn(async (path: string, init?: { params?: { path?: { sessionId?: string }; query?: { kind?: string } } }) => {
			if (path === "/api/v1/projects") return { data: { projects: [{ id: "ao", name: "ao", path: "/p" }] }, response: { status: 200 } };
			if (path === "/api/v1/sessions") {
				const kind = init?.params?.query?.kind;
				return { data: { sessions: h.live.filter((s) => s.kind === kind) }, response: { status: 200 } };
			}
			if (path === "/api/v1/sessions/{sessionId}") {
				const id = init?.params?.path?.sessionId ?? "";
				h.byIdCalls.push(id);
				const hit = h.byId.get(id);
				if (hit?.session) return { data: { session: hit.session }, response: { status: 200 } };
				return { error: { code: "SESSION_NOT_FOUND", message: "Unknown session" }, response: { status: 404 } };
			}
			return { data: {}, response: { status: 200 } };
		}),
		POST: vi.fn(async () => ({ data: {}, response: { status: 200 } })),
	},
}));
vi.mock("../lib/platform", () => ({ hidesShellTopbar: () => true, isMacPlatform: () => false, isLinuxPlatform: () => false }));
vi.mock("../hooks/useWindowFullScreen", () => ({ useWindowFullScreen: () => false }));
vi.mock("../lib/shell-context", () => ({ useShell: () => ({ daemonStatus: { state: "ready" } }) }));
vi.mock("../hooks/useSessionPlan", () => ({ useSessionPlan: () => undefined }));
vi.mock("../hooks/useUnreadNotificationCount", () => ({ useUnreadNotificationCount: () => 0 }));
vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => null }));
vi.mock("./ShellTopbar", () => ({ ShellTopbar: () => null }));
vi.mock("./chat/SessionChatSurface", () => ({ SessionChatSurface: ({ session }: { session: { id: string } }) => <div data-testid="chat">{session.id}</div> }));
vi.mock("./chat/ReviewerChatSurface", async (importOriginal) => ({ ...(await importOriginal<typeof import("./chat/ReviewerChatSurface")>()), ReviewerChatSurface: () => null }));
vi.mock("./TerminalAgentControls", async (importOriginal) => ({ ...(await importOriginal<typeof import("./TerminalAgentControls")>()), SessionTabMenu: () => null }));
vi.mock("./CenterPane", async (importOriginal) => ({ ...(await importOriginal<typeof import("./CenterPane")>()), CenterPane: () => <div data-testid="center" /> }));
vi.mock("./SessionInspector", async (importOriginal) => ({ ...(await importOriginal<typeof import("./SessionInspector")>()), SessionInspector: () => null }));
vi.mock("./BrowserPanel", async (importOriginal) => ({ ...(await importOriginal<typeof import("./BrowserPanel")>()), BrowserPanelView: () => null }));
vi.mock("./SessionFileExplorer", async (importOriginal) => ({ ...(await importOriginal<typeof import("./SessionFileExplorer")>()), SessionFileExplorer: () => null }));
vi.mock("./SessionFileWorkspace", async (importOriginal) => ({ ...(await importOriginal<typeof import("./SessionFileWorkspace")>()), SessionFileWorkspace: () => null }));
vi.mock("../hooks/useBrowserView", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useBrowserView")>()),
	useBrowserView: () => ({ viewId: "v", navState: { viewId: "v", url: "", title: "", canGoBack: false, canGoForward: false, isLoading: false }, slotRef: vi.fn(), navigate: vi.fn(), goBack: vi.fn(), goForward: vi.fn(), reload: vi.fn(), stop: vi.fn(), tabs: [], activeTabId: "", tabNotice: "", agentBrowserActive: false, agentBrowserActivity: undefined, selectTab: vi.fn(), closeTab: vi.fn(), annotationMode: false, setAnnotationMode: vi.fn(), destroy: vi.fn() }),
}));
vi.mock("../hooks/useShellTerminals", () => ({
	adoptedShellHandle: () => undefined,
	useShellTerminals: () => ({ data: [], isLoading: false }),
	useOpenShellTerminal: () => ({ open: vi.fn(), isPending: false }),
	useCloseShellTerminal: () => ({ mutate: vi.fn() }),
	useRenameShellTerminal: () => ({ mutate: vi.fn() }),
}));

const dto = (id: string, kind: string, extra: Record<string, unknown> = {}) => ({
	id, kind, projectId: "ao", title: id, provider: "claude-code", status: "working", activity: "active", isTerminated: false,
	mode: "chat", provisionState: "ready", createdAt: "2026-10-30T09:00:00Z", updatedAt: "2026-10-30T09:00:00Z", prs: [], ...extra,
});
const main = dto("tallpine-233", "orchestrator");
const lane = dto("tallpine-291", "orchestrator", { lane: "B1" });

function mount(sessionId: string, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	const ui = () => (
		<QueryClientProvider client={client}>
			<TooltipProvider><SessionTopbarProvider><SessionView projectId="ao" sessionId={sessionId} /></SessionTopbarProvider></TooltipProvider>
		</QueryClientProvider>
	);
	return { client, ...render(ui()) };
}

describe("SessionView: when an open session is gone", () => {
	beforeEach(() => {
		h.navigate.mockReset();
		h.byIdCalls.length = 0;
		h.byId.clear();
		h.live = [main, lane];
	});

	it("(a) an ended lane orchestrator that leaves the list and 404s sends the view to the default orchestrator with no error shown", async () => {
		const { client } = mount("tallpine-291");
		await screen.findByTestId("chat");
		h.live = [main];
		await client.invalidateQueries({ queryKey: ["workspaces"] });
		await waitFor(() => expect(h.navigate).toHaveBeenCalledTimes(1), { timeout: 4000 });
		expect(h.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "ao", sessionId: "tallpine-233" } });
		expect(h.byIdCalls).toContain("tallpine-291");
		expect(screen.queryByText(/not found/i)).toBeNull();
		expect(screen.queryByRole("alert")).toBeNull();
	});

	it("(b) a session that leaves the live list but still resolves by id (terminated) is not left", async () => {
		const worker = dto("tallpine-w1", "worker");
		h.live = [main, worker];
		const { client } = mount("tallpine-w1");
		await screen.findByTestId("chat");
		h.live = [main];
		h.byId.set("tallpine-w1", { status: 200, session: { ...worker, isTerminated: true, status: "terminated" } });
		await client.invalidateQueries({ queryKey: ["workspaces"] });
		await waitFor(() => expect(h.byIdCalls).toContain("tallpine-w1"));
		await new Promise((r) => setTimeout(r, 1500));
		expect(h.navigate).not.toHaveBeenCalled();
		expect(screen.getByTestId("chat").textContent).toBe("tallpine-w1");
	});

	it("(c) another session ending leaves an open default orchestrator untouched", async () => {
		const { client } = mount("tallpine-233");
		await screen.findByTestId("chat");
		h.live = [main];
		await client.invalidateQueries({ queryKey: ["workspaces"] });
		await new Promise((r) => setTimeout(r, 300));
		expect(h.navigate).not.toHaveBeenCalled();
		expect(h.byIdCalls).not.toContain("tallpine-291");
		expect(screen.getByTestId("chat").textContent).toBe("tallpine-233");
	});
});
