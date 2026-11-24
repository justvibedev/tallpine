import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, renderHook, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { useProjectBrowsesInChrome } from "../hooks/useProjectBrowsesInChrome";
import { projectQueryKey } from "../lib/project-settings-api";
import { tallpineBridge } from "../lib/bridge";
import type { WorkspaceSession } from "../types/workspace";
import type { BrowserViewModel } from "../hooks/useBrowserView";
import { BrowserPanel } from "./BrowserPanel";
import type { BrowserAnnotationQueueModel } from "./browser/browser-types";
import { BrowserView } from "./SessionInspector/BrowserView";

function client(configs: Record<string, Record<string, unknown>>) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
	for (const [id, config] of Object.entries(configs)) queryClient.setQueryData(projectQueryKey(id), { config });
	return queryClient;
}

const wrap = (queryClient: QueryClient) => ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;

describe("useProjectBrowsesInChrome", () => {
	it("is on only for a local session whose project sets browserBackend=chrome", () => {
		const queryClient = client({ chromeProj: { browserBackend: "chrome" }, electronProj: { browserBackend: "electron" }, unsetProj: {} });
		const browses = (session: Pick<WorkspaceSession, "hostId" | "workspaceId">) =>
			renderHook(() => useProjectBrowsesInChrome(session), { wrapper: wrap(queryClient) }).result.current;
		expect(browses({ workspaceId: "chromeProj" })).toBe(true);
		expect(browses({ workspaceId: "electronProj" })).toBe(false);
		expect(browses({ workspaceId: "unsetProj" })).toBe(false);
		// A remote host keeps its own agent browser (a14).
		expect(browses({ workspaceId: "chromeProj", hostId: "host-2" })).toBe(false);
	});
});

describe("BrowserPanel in Chrome mode", () => {
	it("shows the Chrome mode card instead of the in-app browser", async () => {
		vi.spyOn(tallpineBridge.browser, "chromeState").mockResolvedValue({ running: false, activeTab: null });
		const session = { id: "s1", workspaceId: "chromeProj" } as WorkspaceSession;
		render(<BrowserPanel session={session} active poppedOut={false} onTogglePopOut={() => undefined} />, { wrapper: wrap(client({ chromeProj: { browserBackend: "chrome" } })) });
		expect(await screen.findByTestId("browser-chrome-mode")).toBeInTheDocument();
		expect(screen.queryByTestId("browser-panel")).not.toBeInTheDocument();
	});

	// The session view mounts BrowserPanelView through the inspector's Browser
	// tab, not BrowserPanel: the card and Sign in to sites must show there too.
	it("shows Sign in to sites in the inspector's Browser tab", async () => {
		vi.spyOn(tallpineBridge.browser, "chromeState").mockResolvedValue({ running: true, activeTab: null });
		const session = { id: "s1", workspaceId: "chromeProj" } as WorkspaceSession;
		render(
			<BrowserView
				session={session}
				isActive
				browserPoppedOut={false}
				browserView={{} as BrowserViewModel}
				browserAnnotationQueue={{} as BrowserAnnotationQueueModel}
			/>,
			{ wrapper: wrap(client({ chromeProj: { browserBackend: "chrome" } })) },
		);
		expect(await screen.findByTestId("browser-chrome-mode")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Sign in to sites" })).toBeInTheDocument();
		expect(screen.queryByTestId("browser-panel")).not.toBeInTheDocument();
	});
});
