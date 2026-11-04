import { screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { expect, vi } from "vitest";

const h = vi.hoisted(() => ({
	delete: vi.fn(),
	get: vi.fn(),
	post: vi.fn(),
	remoteGet: vi.fn(),
	remotePost: vi.fn(),
	remoteDelete: vi.fn(),
	connectedHostIds: ["box-a"],
	ensureReadiness: vi.fn(),
	ensureTargetedReadiness: vi.fn(),
	agentValues: [] as string[],
	agentCatalog: undefined as { agents: ReturnType<typeof import("../test/agent-readiness-fixtures").agentReadiness>[] } | undefined,
}));


vi.mock("../hooks/useAgentReadinessQuery", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useAgentReadinessQuery")>();
	return {
		...actual,
		ensureAgentReadiness: h.ensureTargetedReadiness,
		useAgentReadinessQuery: () => ({ data: h.agentCatalog, isFetching: false, isSuccess: h.agentCatalog !== undefined }),
		useEnsureAgentReadiness: h.ensureReadiness,
	};
});


vi.mock("./CreateProjectAgentSheet", () => ({
	RequiredAgentField: ({
		value,
		onChange,
		triggerClassName,
		disabled,
		manageView,
	}: {
		value: string;
		onChange: (value: string) => void;
		triggerClassName?: string;
		disabled?: boolean;
		manageView?: "local" | "cloud";
	}) => {
		h.agentValues.push(value);
		return (
			<button
				type="button"
				aria-label="Agent"
				className={triggerClassName}
				data-testid="agent-field"
				data-value={value}
				data-manage-view={manageView}
				disabled={disabled}
				onClick={() => onChange(value === "codex" ? "claude-code" : "codex")}
			/>
		);
	},
}));


vi.mock("../lib/api-client", () => ({
	apiClient: {
		DELETE: h.delete,
		GET: h.get,
		POST: h.post,
	},
	apiErrorCode: (error: { code?: string }) => error?.code,
	apiErrorMessage: (error: { message?: string }, fallback = "err") => error?.message ?? fallback,
}));


vi.mock("../lib/host-clients", () => ({
	clientForHost: () => ({ GET: h.remoteGet, POST: h.remotePost, DELETE: h.remoteDelete }),
	connectedHosts: () => h.connectedHostIds,
	subscribeConnectedHosts: () => () => undefined,
}));



vi.mock("../hooks/useWorkspaceQuery", () => ({
}));





export function Wrap({ children, queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }) }: {
	children: ReactNode;
	queryClient?: QueryClient;
}) {
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}


export const task = () => screen.getByRole("textbox", { name: "Task" });

export const startTask = () => screen.getByRole("button", { name: "Start task" });


export async function waitForTaskReady() {
	await waitFor(() => expect(startTask()).toBeEnabled());
}


export { h };
