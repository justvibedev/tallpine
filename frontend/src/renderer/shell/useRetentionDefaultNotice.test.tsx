import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { useUiStore } from "../stores/ui-store";
import { useRetentionDefaultNotice } from "./useRetentionDefaultNotice";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: get },
	apiErrorMessage: (e: unknown) => String(e),
}));

const policy = (source: string, days = 7) => ({
	schemaVersion: 2,
	rules: [
		{
			name: "sessionRetention",
			titleKey: "x",
			fields: [
				{ name: "enabled", kind: "bool", value: true, default: true, source, locked: false, labelKey: "a", descriptionKey: "b", projectOverride: "any" },
				{ name: "days", kind: "int", value: days, default: 7, source, locked: false, labelKey: "a", descriptionKey: "b", projectOverride: "any" },
			],
		},
	],
});

function mount() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return renderHook(() => useRetentionDefaultNotice(vi.fn() as never), { wrapper });
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	window.localStorage.clear();
	get.mockReset();
	useUiStore.setState({ globalToasts: [], globalToast: null });
});

describe("useRetentionDefaultNotice", () => {
	it("toasts once, with the resolved days, for an install on the default", async () => {
		get.mockResolvedValue({ data: policy("default", 30) });
		const first = mount();
		await waitFor(() => expect(useUiStore.getState().globalToast?.title).toMatch(/after 30 days/));
		first.unmount();
		useUiStore.setState({ globalToasts: [], globalToast: null });
		mount();
		await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
		expect(useUiStore.getState().globalToast).toBeNull();
	});

	it("says nothing when the value was set explicitly", async () => {
		get.mockResolvedValue({ data: policy("user") });
		mount();
		await waitFor(() => expect(get).toHaveBeenCalled());
		expect(useUiStore.getState().globalToast).toBeNull();
	});
});
