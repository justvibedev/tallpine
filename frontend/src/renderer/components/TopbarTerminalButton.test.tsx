import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { TooltipProvider } from "./ui/tooltip";
import { TopbarTerminalButton } from "./TopbarTerminalButton";

const mocks = vi.hoisted(() => ({
	data: undefined as unknown[] | undefined,
	navigate: vi.fn(),
	open: vi.fn((input: { projectId?: string }) => ({ handleId: "pending-shell:new", ...input })),
	fetchShells: vi.fn(async () => [] as unknown[]),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => mocks.navigate,
}));

vi.mock("../hooks/useShellTerminals", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useShellTerminals")>();
	return {
		...actual,
		shellTerminalsQueryOptions: { ...actual.shellTerminalsQueryOptions, queryFn: mocks.fetchShells },
		useShellTerminals: () => ({ data: mocks.data }),
		useOpenShellTerminal: () => ({ open: mocks.open }),
	};
});

const shell = (handleId: string, projectId?: string, sessionId?: string) => ({
	handleId,
	projectId,
	sessionId,
	title: "Terminal 1",
	workingDir: "/tmp",
	createdAt: "2026-11-17T00:00:00Z",
});

async function clickTerminal(projectId: string | undefined) {
	render(
		<QueryClientProvider client={new QueryClient()}>
			<TooltipProvider>
				<TopbarTerminalButton projectId={projectId} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	await userEvent.setup().click(screen.getByRole("button", { name: "Open terminal" }));
	await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/terminals" }));
}

describe("TopbarTerminalButton", () => {
	beforeEach(() => {
		mocks.data = [];
		mocks.navigate.mockClear();
		mocks.open.mockClear();
		mocks.fetchShells.mockClear();
		useUiStore.setState({ activeShellTerminalHandleId: null });
	});

	it("creates a terminal in the project when it has none", async () => {
		// A session's shell and another project's shell do not count as this project's.
		mocks.data = [shell("other-project", "proj-2"), shell("session-shell", "proj-1", "sess-1")];

		await clickTerminal("proj-1");

		expect(mocks.open).toHaveBeenCalledWith({ projectId: "proj-1" });
		expect(useUiStore.getState().activeShellTerminalHandleId).toBe("pending-shell:new");
	});

	it("opens the project's existing terminals instead of creating another", async () => {
		mocks.data = [shell("other-project", "proj-2"), shell("first", "proj-1"), shell("second", "proj-1")];

		await clickTerminal("proj-1");

		expect(mocks.open).not.toHaveBeenCalled();
		expect(useUiStore.getState().activeShellTerminalHandleId).toBe("first");
	});

	it("keeps the selected tab when it belongs to the project", async () => {
		mocks.data = [shell("first", "proj-1"), shell("second", "proj-1")];
		useUiStore.setState({ activeShellTerminalHandleId: "second" });

		await clickTerminal("proj-1");

		expect(mocks.open).not.toHaveBeenCalled();
		expect(useUiStore.getState().activeShellTerminalHandleId).toBe("second");
	});

	it("asks the daemon before creating when the list has not loaded yet", async () => {
		mocks.data = undefined;
		mocks.fetchShells.mockResolvedValueOnce([shell("existing", "proj-1")]);

		await clickTerminal("proj-1");

		expect(mocks.fetchShells).toHaveBeenCalledTimes(1);
		expect(mocks.open).not.toHaveBeenCalled();
		expect(useUiStore.getState().activeShellTerminalHandleId).toBe("existing");
	});

	it("creates a terminal without a project for the standalone workspace", async () => {
		await clickTerminal(STANDALONE_WORKSPACE_ID);
		expect(mocks.open).toHaveBeenCalledWith({ projectId: undefined });
	});

	it("opens any standalone terminal when no project is in scope", async () => {
		mocks.data = [shell("loose")];
		await clickTerminal(undefined);
		expect(mocks.open).not.toHaveBeenCalled();
		expect(useUiStore.getState().activeShellTerminalHandleId).toBe("loose");
	});
});
