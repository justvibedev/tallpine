import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useManagedLocks } from "./useManagedLocks";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock } }));

const wrapper = ({ children }: { children: ReactNode }) => (
	<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>
);

beforeEach(() => getMock.mockReset());

describe("useManagedLocks", () => {
	it("returns only keys locked by managed config, for the given project", async () => {
		getMock.mockResolvedValue({
			data: { values: [
				{ key: "project.worker", locked: true, lockedBy: "managed" },
				{ key: "app.versionRefresh", locked: true, lockedBy: "TALLPINE_VERSION_REFRESH" },
				{ key: "project.models", locked: false },
			] },
			error: undefined,
		});
		const { result } = renderHook(() => useManagedLocks("p1"), { wrapper });
		await waitFor(() => expect(result.current.has("project.worker")).toBe(true));
		expect([...result.current]).toEqual(["project.worker"]);
		expect(getMock).toHaveBeenCalledWith("/api/v1/settings/values", { params: { query: { project: "p1" } } });
	});

	it("locks nothing when the daemon cannot answer, and does not query when disabled", async () => {
		getMock.mockResolvedValue({ data: undefined, error: { code: "x" } });
		const { result } = renderHook(() => useManagedLocks(undefined), { wrapper });
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(result.current.size).toBe(0);
		getMock.mockClear();
		renderHook(() => useManagedLocks("p1", false), { wrapper });
		expect(getMock).not.toHaveBeenCalled();
	});
});
