import { describe, expect, it, vi } from "vitest";
import { openBacklogPage } from "./open-backlog-page";

describe("openBacklogPage", () => {
	it("navigates to the Backlog page", () => {
		const navigate = vi.fn();
		openBacklogPage(navigate as never);
		expect(navigate).toHaveBeenCalledWith({ to: "/backlog" });
	});
});
