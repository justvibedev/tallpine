import { beforeEach, describe, expect, it, vi } from "vitest";

const post = vi.fn();
vi.mock("./host-clients", () => ({ clientForSessionHost: () => ({ POST: post }) }));

import { RESUME_REJOIN_LIMIT, postResumeAgent } from "./resume-agent-request";

const running = { data: undefined, error: { code: "AGENT_RESUME_RUNNING", message: "still resuming" }, response: { status: 409 } };

describe("postResumeAgent", () => {
	beforeEach(() => post.mockReset());

	it("re-joins a resume still running and returns its result", async () => {
		const ok = { data: { resumeMode: "native" }, error: undefined, response: { status: 200 } };
		post.mockResolvedValueOnce(running).mockResolvedValueOnce(running).mockResolvedValueOnce(ok);
		await expect(postResumeAgent("tp-1")).resolves.toBe(ok);
		expect(post).toHaveBeenCalledTimes(3);
	});

	it("stops after the re-join limit with the still-running answer", async () => {
		post.mockResolvedValue(running);
		await expect(postResumeAgent("tp-1")).resolves.toBe(running);
		expect(post).toHaveBeenCalledTimes(RESUME_REJOIN_LIMIT + 1);
	});

	it("returns any other failure at once", async () => {
		const busy = { data: undefined, error: { code: "AGENT_RESUME_IN_PROGRESS", message: "busy" }, response: { status: 409 } };
		post.mockResolvedValueOnce(busy);
		await expect(postResumeAgent("tp-1")).resolves.toBe(busy);
		expect(post).toHaveBeenCalledTimes(1);
	});
});
