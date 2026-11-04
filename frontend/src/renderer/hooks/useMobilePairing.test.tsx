import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { mobileStatusQueryKey } from "../lib/mobile-status-key";
import { useMobilePairing } from "./useMobilePairing";

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: api.get, POST: api.post },
	apiErrorMessage: (e: unknown) => String(e),
}));
vi.mock("./useStayAwake", () => ({
	useStayAwake: () => ({ on: false, lidOn: false, supported: false, error: null, set: vi.fn(), setLid: vi.fn() }),
}));

function status(overrides: Record<string, unknown> = {}) {
	return {
		featureEnabled: true,
		enabled: false,
		requireDevicePairing: false,
		browserRemoteAccess: false,
		host: "",
		tailscaleHost: "",
		port: 0,
		password: "",
		endpoints: [],
		securePairing: { enabled: false, available: false, active: false, host: "", port: 0, reason: "" },
		...overrides,
	};
}

function setup(active = true) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { client, ...renderHook(({ on }) => useMobilePairing(on), { wrapper, initialProps: { on: active } }) };
}

describe("useMobilePairing", () => {
	beforeEach(() => {
		api.get.mockReset();
		api.post.mockReset();
	});

	it("polls the status only while the page is active", async () => {
		api.get.mockResolvedValue({ data: status() });
		const idle = setup(false);
		await new Promise((r) => setTimeout(r, 20));
		expect(api.get).not.toHaveBeenCalled();
		expect(idle.result.current.query.data).toBeUndefined();

		const live = setup(true);
		await waitFor(() => expect(live.result.current.query.data?.featureEnabled).toBe(true));
		expect(api.get).toHaveBeenCalledWith("/api/v1/mobile/status");
	});

	it("enable posts to the daemon and refetches the status", async () => {
		api.get.mockResolvedValueOnce({ data: status() }).mockResolvedValue({ data: status({ enabled: true }) });
		api.post.mockResolvedValue({ data: {} });
		const { result } = setup();
		await waitFor(() => expect(result.current.query.data).toBeDefined());
		await act(async () => {
			await result.current.enable.mutateAsync();
		});
		expect(api.post).toHaveBeenCalledWith("/api/v1/mobile/enable");
		await waitFor(() => expect(result.current.query.data?.enabled).toBe(true));
	});

	it("a failed enable clears the optimistic flag and surfaces the error", async () => {
		api.get.mockResolvedValue({ data: status() });
		api.post.mockResolvedValue({ error: "boom" });
		const { result } = setup();
		await waitFor(() => expect(result.current.query.data).toBeDefined());
		act(() => result.current.setOptimisticEnabled(true));
		await act(async () => {
			await result.current.enable.mutateAsync().catch(() => undefined);
		});
		await waitFor(() => expect(result.current.optimisticEnabled).toBeNull());
		expect(result.current.enable.error).toBeTruthy();
	});

	it("device pairing flips optimistically and rolls back when the request fails", async () => {
		// The refetch after the failure never answers, so only the rollback can restore the flag.
		api.get.mockResolvedValueOnce({ data: status({ requireDevicePairing: false }) }).mockImplementation(() => new Promise(() => undefined));
		let reject: (value: unknown) => void = () => undefined;
		api.post.mockImplementation(() => new Promise((resolve) => { reject = resolve; }));
		const { result, client } = setup();
		await waitFor(() => expect(result.current.query.data).toBeDefined());
		act(() => result.current.setRequireDevicePairing.mutate(true));
		await waitFor(() => expect((client.getQueryData(mobileStatusQueryKey) as { requireDevicePairing: boolean }).requireDevicePairing).toBe(true));
		await act(async () => reject({ error: "nope" }));
		await waitFor(() => expect((client.getQueryData(mobileStatusQueryKey) as { requireDevicePairing: boolean }).requireDevicePairing).toBe(false));
	});

	it("turns HTTPS on once for a tailnet host, not again on later polls", async () => {
		api.get.mockResolvedValue({
			data: status({ tailscaleHost: "100.1.2.3", securePairing: { enabled: false, available: true, active: false, host: "", port: 0, reason: "" } }),
		});
		api.post.mockResolvedValue({ data: {} });
		const { result, client } = setup();
		await waitFor(() => expect(api.post).toHaveBeenCalledWith("/api/v1/mobile/secure-pairing", { body: { enabled: true } }));
		await act(async () => {
			await client.invalidateQueries({ queryKey: mobileStatusQueryKey });
		});
		expect(api.post.mock.calls.filter(([path]) => path === "/api/v1/mobile/secure-pairing")).toHaveLength(1);
		expect(result.current.query.data?.tailscaleHost).toBe("100.1.2.3");
	});
});
