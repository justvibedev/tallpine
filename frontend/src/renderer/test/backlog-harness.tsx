import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { vi } from "vitest";
import { TooltipProvider } from "../components/ui/tooltip";
import type { BacklogItem } from "../lib/backlog-api";
import type { WorkspaceSummary } from "../types/workspace";

/** Mock of lib/backlog-api's network functions; the keys and types stay real. */
export const backlogApi = {
	listBacklog: vi.fn(),
	createBacklogItem: vi.fn(),
	getBacklogItem: vi.fn(),
	updateBacklogItem: vi.fn(),
	setBacklogState: vi.fn(),
	reorderBacklogItem: vi.fn(),
	addBacklogLink: vi.fn(),
	removeBacklogLink: vi.fn(),
	setBacklogField: vi.fn(),
	clearBacklogField: vi.fn(),
	getBacklogInboxCounts: vi.fn(),
};

export const workspaceData = { current: [] as WorkspaceSummary[] };

export function resetBacklogApi(items: BacklogItem[], full: Partial<Record<string, BacklogItem>> = {}) {
	for (const fn of Object.values(backlogApi)) fn.mockReset();
	backlogApi.listBacklog.mockImplementation(async (projectId: string, state: string) =>
		items.filter((item) => item.projectId === projectId && item.state === state),
	);
	backlogApi.getBacklogItem.mockImplementation(async (projectId: string, ref: string) => {
		const found = items.find((item) => item.projectId === projectId && item.ref === ref);
		return full[ref] ?? found;
	});
	backlogApi.updateBacklogItem.mockImplementation(async (_p: string, ref: string) => items.find((item) => item.ref === ref));
	backlogApi.setBacklogState.mockImplementation(async (_p: string, ref: string) => items.find((item) => item.ref === ref));
	backlogApi.addBacklogLink.mockImplementation(async (_p: string, ref: string) => items.find((item) => item.ref === ref));
	backlogApi.removeBacklogLink.mockImplementation(async (_p: string, ref: string) => items.find((item) => item.ref === ref));
	backlogApi.setBacklogField.mockImplementation(async (_p: string, ref: string) => items.find((item) => item.ref === ref));
	backlogApi.clearBacklogField.mockImplementation(async (_p: string, ref: string) => items.find((item) => item.ref === ref));
	backlogApi.reorderBacklogItem.mockResolvedValue(undefined);
	backlogApi.createBacklogItem.mockImplementation(async (projectId: string, input: { title: string }) => ({
		id: `new-${input.title}`,
		ref: "B99",
		projectId,
		title: input.title,
	}));
	backlogApi.getBacklogInboxCounts.mockResolvedValue({});
}

export function renderBacklog(ui: ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(ui, {
		wrapper: ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={client}>
				<TooltipProvider>{children}</TooltipProvider>
			</QueryClientProvider>
		),
	});
}
