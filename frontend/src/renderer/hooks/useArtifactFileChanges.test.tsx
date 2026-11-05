import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ subscribeLocal: vi.fn(), unsubscribeLocal: vi.fn() }));

vi.mock("../lib/artifact-file-events", () => ({ subscribeArtifactFileChanges: mocks.subscribeLocal }));

import { useArtifactFileChanges } from "./useArtifactFileChanges";

function render(sessionId: string, enabled: boolean) {
	const client = new QueryClient();
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { ...renderHook(({ on }) => useArtifactFileChanges(sessionId, on), { initialProps: { on: enabled }, wrapper }), client };
}

beforeEach(() => {
	mocks.subscribeLocal.mockReset();
	mocks.unsubscribeLocal.mockReset();
	mocks.subscribeLocal.mockReturnValue(mocks.unsubscribeLocal);
});

describe("useArtifactFileChanges", () => {
	it("follows a session's artifact stream while enabled and releases it after", () => {
		const view = render("s-1", true);
		expect(mocks.subscribeLocal).toHaveBeenCalledWith("s-1", view.client);
		view.rerender({ on: false });
		expect(mocks.unsubscribeLocal).toHaveBeenCalledTimes(1);
	});

	it("does not subscribe while disabled", () => {
		render("s-1", false);
		expect(mocks.subscribeLocal).not.toHaveBeenCalled();
	});
});
