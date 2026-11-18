import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { UseNavigateResult } from "@tanstack/react-router";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { shellTerminalsQueryKey, type useOpenShellTerminal } from "../hooks/useShellTerminals";
import { useUiStore } from "../stores/ui-store";
import { useShellNativeEvents } from "./useShellNativeEvents";

const open = vi.fn((input: { projectId?: string }) => ({ handleId: "pending-shell:new", ...input }));

// One new-terminal request (the + tab or ⌘T) handled by the shell layout.
function requestTerminal({ isTerminalsRoute, projectId }: { isTerminalsRoute: boolean; projectId?: string }) {
	const queryClient = new QueryClient();
	queryClient.setQueryData(shellTerminalsQueryKey, [
		{ handleId: "proj-shell", projectId: "proj-1", title: "Terminal 1", workingDir: "/p", createdAt: "2026-11-17T00:00:00Z" },
	]);
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	renderHook(
		() =>
			useShellNativeEvents({
				navigate: vi.fn() as unknown as UseNavigateResult<string>,
				requestCreateProjectFromPath: vi.fn(),
				requestNewShellTerminal: vi.fn(),
				newShellTerminalNonce: 1,
				setActiveShellTerminal: vi.fn(),
				openShellTerminal: { open } as unknown as ReturnType<typeof useOpenShellTerminal>,
				handledShellNonceRef: { current: 0 },
				routeParams: projectId ? { projectId } : {},
				scopedProjectId: projectId,
				isTerminalsRoute,
				navigateSession: vi.fn(),
			}),
		{ wrapper },
	);
}

describe("useShellNativeEvents new terminal", () => {
	beforeEach(() => {
		open.mockClear();
		useUiStore.setState({ activeShellTerminalHandleId: "proj-shell" });
	});

	it("adds a tab on /terminals in the selected tab's project", () => {
		requestTerminal({ isTerminalsRoute: true });
		expect(open).toHaveBeenCalledWith({ projectId: "proj-1", sessionId: undefined });
	});

	it("keeps the route's project ahead of the selected tab's", () => {
		requestTerminal({ isTerminalsRoute: false, projectId: "proj-2" });
		expect(open).toHaveBeenCalledWith({ projectId: "proj-2", sessionId: undefined });
	});
});
