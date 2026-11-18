import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { conversationQueryKey, refreshConversation } from "./conversation-state";

function clientWith(controller: string) {
	const client = new QueryClient();
	const spy = vi.spyOn(client, "invalidateQueries").mockResolvedValue();
	client.setQueryData(conversationQueryKey("s1"), { pages: [{ controller: { state: controller } }], pageParams: [undefined] }, { updatedAt: 2_000 });
	return { client, spy };
}

describe("refreshConversation", () => {
	it("skips a live snapshot fetched after the change was requested", async () => {
		const { client, spy } = clientWith("ready");
		await refreshConversation(client, "s1", undefined, 1_000);
		expect(spy).not.toHaveBeenCalled();
	});

	it("refreshes a snapshot taken mid-wake, an older one, or when no request time is given", async () => {
		const hibernated = clientWith("hibernated");
		await refreshConversation(hibernated.client, "s1", undefined, 1_000);
		expect(hibernated.spy).toHaveBeenCalledTimes(1);
		const live = clientWith("ready");
		await refreshConversation(live.client, "s1", undefined, 3_000);
		await refreshConversation(live.client, "s1");
		expect(live.spy).toHaveBeenCalledTimes(2);
	});
});
