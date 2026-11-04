import { expect, it, vi } from "vitest";
import { scopeLaunchConductor } from "./hostScopeLaunch";

it("sends the scope's host and the caller's lane when the launcher passes both", async () => {
	const launch = vi.fn(async () => ({}) as never);
	await scopeLaunchConductor(launch, "scope-host")("p", false, "chat", "caller-host", "B7");
	expect(launch).toHaveBeenCalledWith("p", false, "chat", "scope-host", "B7");
});
