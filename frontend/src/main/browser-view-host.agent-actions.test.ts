import {
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { parseAgentBrowserJSON } from "./agent-browser-runtime/native-actions";
import {
	setupHost,
	setupTabHost,
} from "./browser-view-host-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});


describe("browser:act", () => {
	function setupActHost(
		runAction: (
			sessionId: string,
			action: string,
			args: Record<string, unknown>,
			provider?: unknown,
			signal?: AbortSignal,
		) => Promise<unknown>,
	) {
		const runtime = {
			runAction: vi.fn(runAction),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		return { ...setupHost(runtime), runAction: runtime.runAction as unknown as ReturnType<typeof vi.fn> };
	}

	it("resolves an instruction to a ref via snapshot, then performs the action", async () => {
		const { host, runAction } = setupActHost(async (_sessionId, action, args) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Wrong display text" [ref=e99]', refs: { e3: { role: "button", name: "Submit" } } };
			}
			if (action === "click") return { clicked: args.ref };
			return {};
		});

		const result = await host.execute("sess-1", "act", { instruction: "the submit button" });

		expect(result).toMatchObject({ outcome: "matched", resolvedRef: "e3", retried: false });
		expect(runAction).toHaveBeenCalledWith(
			"sess-1",
			"click",
			{ ref: "e3" },
			expect.objectContaining({ listTargets: expect.any(Function) }),
			undefined,
		);
	});

	it("reports before/after facts and a satisfied navigation postcondition separately from matching", async () => {
		let page: ReturnType<typeof setupActHost>;
		page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Continue" [ref=e1]', refs: { e1: { role: "button", name: "Continue" } } };
			}
			if (action === "click") {
				page.setCurrentURL("http://localhost:5173/next");
				page.webContentsListeners.get("did-navigate")?.({} as never, "http://localhost:5173/next" as never);
				return { clicked: true };
			}
			return {};
		});
		await page.host.execute("sess-1", "tabs");
		// The agent tab's first load waits for the proxy; let it finish before the page moves.
		await new Promise((resolve) => setTimeout(resolve, 0));
		page.setCurrentURL("http://localhost:5173/start");

		const result = (await page.host.execute("sess-1", "act", {
			instruction: "continue",
			postcondition: { kind: "navigation", timeoutMs: 50 },
		})) as Record<string, unknown>;

		expect(result).toMatchObject({
			outcome: "matched",
			inputDispatched: true,
			before: { url: "http://localhost:5173/start", documentGeneration: 0, navigationGeneration: 0 },
			after: { url: "http://localhost:5173/next", documentGeneration: 1, navigationGeneration: 1 },
			postcondition: { kind: "navigation", status: "satisfied" },
		});
	});

	it("does not report a pre-existing URL postcondition as an effect of the action", async () => {
		const page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Refresh" [ref=e1]', refs: { e1: { role: "button", name: "Refresh" } } };
			}
			if (action === "wait") return { waited: "url" };
			return { clicked: true };
		});
		await page.host.execute("sess-1", "tabs");
		// The agent tab's first load waits for the proxy; let it finish before the page moves.
		await new Promise((resolve) => setTimeout(resolve, 0));
		page.setCurrentURL("http://localhost:5173/already-there");

		const result = await page.host.execute("sess-1", "act", {
			instruction: "refresh",
			postcondition: { kind: "url", value: "/already-there", timeoutMs: 50 },
		});

		expect(result).toMatchObject({
			inputDispatched: true,
			postcondition: { kind: "url", status: "already-satisfied", reason: "pre-existing" },
		});
		expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
		expect(page.runAction.mock.calls.filter(([, action]) => action === "wait")).toHaveLength(0);
	});

	it("checks whether expected text existed before dispatch", async () => {
		const callOrder: string[] = [];
		const page = setupActHost(async (_sessionId, action) => {
			callOrder.push(action);
			if (action === "snapshot") {
				return { snapshot: '- button "Save" [ref=e1]', refs: { e1: { role: "button", name: "Save" } } };
			}
			if (action === "get") return { text: "Saved before this action" };
			if (action === "wait") return { waited: "text" };
			return { clicked: true };
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "save",
			postcondition: { kind: "text", value: "Saved", timeoutMs: 50 },
		});

		expect(result).toMatchObject({
			inputDispatched: true,
			postcondition: { kind: "text", status: "already-satisfied", reason: "pre-existing" },
		});
		expect(callOrder).toEqual(["snapshot", "get", "click"]);
	});

	it("reports a beforeunload-cancelled navigation without retrying the action", async () => {
		let page: ReturnType<typeof setupActHost>;
		page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Leave" [ref=e1]', refs: { e1: { role: "button", name: "Leave" } } };
			}
			if (action === "click") {
				page.webContentsListeners.get("will-prevent-unload")?.({} as never);
				return { clicked: true };
			}
			return {};
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "leave",
			postcondition: { kind: "navigation", timeoutMs: 50 },
		});

		expect(result).toMatchObject({
			outcome: "matched",
			navigation: { status: "cancelled", reason: "beforeunload" },
			postcondition: { kind: "navigation", status: "cancelled", reason: "beforeunload" },
		});
		expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
	});

	it("reports an unmet postcondition without retrying a non-idempotent action", async () => {
		const page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Pay" [ref=e1]', refs: { e1: { role: "button", name: "Pay" } } };
			}
			return { clicked: true };
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "pay",
			postcondition: { kind: "navigation", timeoutMs: 1 },
		});

		expect(result).toMatchObject({
			outcome: "matched",
			inputDispatched: true,
			postcondition: { kind: "navigation", status: "unmet" },
		});
		expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
	});

	it("cancels a pending postcondition promptly without retrying the action", async () => {
		const controller = new AbortController();
		const page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Submit" [ref=e1]', refs: { e1: { role: "button", name: "Submit" } } };
			}
			return { clicked: true };
		});

		const pending = page.host.execute(
			"sess-1",
			"act",
			{ instruction: "submit", postcondition: { kind: "navigation", timeoutMs: 10_000 } },
			controller.signal,
		);
		await vi.waitFor(() => expect(page.runAction).toHaveBeenCalledWith("sess-1", "click", expect.anything(), expect.anything(), controller.signal));
		controller.abort();

		await expect(pending).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
	});

	it("satisfies a DOM-change postcondition only from a fresh snapshot", async () => {
		let snapshots = 0;
		const page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				snapshots += 1;
				return snapshots === 1
					? { snapshot: '- button "Save" [ref=e1]', refs: { e1: { role: "button", name: "Save" } } }
					: { snapshot: '- status "Saved"', refs: {} };
			}
			return { clicked: true };
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "save",
			postcondition: { kind: "dom-change", timeoutMs: 50 },
		});

		expect(result).toMatchObject({ postcondition: { kind: "dom-change", status: "satisfied" } });
		expect(snapshots).toBe(2);
	});

	it("does not treat an identical fresh snapshot as a DOM change", async () => {
		const page = setupActHost(async (_sessionId, action, args) => {
			if (action === "snapshot") {
				expect(args).toMatchObject({ interactive: true });
				return { snapshot: '- button "Save" [ref=e1]', refs: { e1: { role: "button", name: "Save" } } };
			}
			return { clicked: true };
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "save",
			postcondition: { kind: "dom-change", timeoutMs: 1 },
		});

		expect(result).toMatchObject({ postcondition: { kind: "dom-change", status: "unmet", reason: "timeout" } });
		expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
	});

	it("prefers a committed navigation over an earlier beforeunload attempt", async () => {
		let page: ReturnType<typeof setupActHost>;
		page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Leave" [ref=e1]', refs: { e1: { role: "button", name: "Leave" } } };
			}
			if (action === "click") {
				page.webContentsListeners.get("will-prevent-unload")?.({} as never);
				page.setCurrentURL("http://localhost:5173/destination");
				page.webContentsListeners.get("did-navigate")?.({} as never, "http://localhost:5173/destination" as never);
				return { clicked: true };
			}
			return {};
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "leave",
			postcondition: { kind: "navigation", timeoutMs: 50 },
		});

		expect(result).toMatchObject({
			navigation: { status: "observed" },
			postcondition: { kind: "navigation", status: "satisfied" },
		});
		expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
	});

	it("correlates a newly activated popup and reports facts for that tab", async () => {
		const page = setupTabHost();
		const runAction = page.runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		const originalRunAction = runAction.getMockImplementation()! as (...args: unknown[]) => Promise<unknown>;
		runAction.mockImplementation(async (...callArgs: unknown[]) => {
			const [, action] = callArgs as [string, string, Record<string, unknown>];
			if (action === "snapshot") {
				return { snapshot: '- button "Open" [ref=e1]', refs: { e1: { role: "button", name: "Open" } } };
			}
			if (action === "click") {
				page.views[0].webContents.openWindow("http://localhost:3000/popup");
				await vi.waitFor(() => expect(page.views[1]?.webContents.getURL()).toBe("http://localhost:3000/popup"));
				return { clicked: true };
			}
			return originalRunAction(...callArgs);
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "open",
			postcondition: { kind: "navigation", timeoutMs: 50 },
		});

		expect(result).toMatchObject({
			before: { tabId: "t1" },
			after: { tabId: "t2", url: "http://localhost:3000/popup" },
			navigation: { status: "observed" },
			postcondition: { kind: "navigation", status: "satisfied" },
		});
		expect(runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
	});

	it("does not satisfy navigation from an iframe history change", async () => {
		let page: ReturnType<typeof setupActHost>;
		page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Update frame" [ref=e1]', refs: { e1: { role: "button", name: "Update frame" } } };
			}
			if (action === "click") {
				page.webContentsListeners.get("did-navigate-in-page")?.(
					{} as never,
					"http://localhost:5173/frame#changed" as never,
					false as never,
				);
				return { clicked: true };
			}
			return {};
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "update frame",
			postcondition: { kind: "navigation", timeoutMs: 1 },
		});

		expect(result).toMatchObject({
			navigation: { status: "not-observed" },
			postcondition: { kind: "navigation", status: "unmet", reason: "timeout" },
		});
	});

	it.each([0, 55_001, 1.5, "1000"])("rejects invalid direct postcondition timeout %j", async (timeoutMs) => {
		const page = setupActHost(async () => {
			throw new Error("native runtime should not run for invalid input");
		});

		await expect(
			page.host.execute("sess-1", "act", {
				instruction: "continue",
				postcondition: { kind: "navigation", timeoutMs },
			}),
		).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		expect(page.runAction).not.toHaveBeenCalled();
	});

	it("maps a native URL wait timeout to an unmet postcondition", async () => {
		const page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Continue" [ref=e1]', refs: { e1: { role: "button", name: "Continue" } } };
			}
			if (action === "wait") {
				throw Object.assign(new Error("Wait timed out after 50ms"), { code: "AGENT_BROWSER_WAIT_TIMEOUT" });
			}
			return { clicked: true };
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "continue",
			postcondition: { kind: "url", value: "/destination", timeoutMs: 50 },
		});

		expect(result).toMatchObject({
			inputDispatched: true,
			postcondition: { kind: "url", status: "unmet", expected: "/destination", reason: "timeout" },
		});
		expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
	});

	it("polls DOM observations conservatively within the postcondition budget", async () => {
		vi.useFakeTimers();
		try {
			let snapshots = 0;
			const page = setupActHost(async (_sessionId, action) => {
				if (action === "snapshot") {
					snapshots += 1;
					return { snapshot: '- button "Save" [ref=e1]', refs: { e1: { role: "button", name: "Save" } } };
				}
				return { clicked: true };
			});

			const pending = page.host.execute("sess-1", "act", {
				instruction: "save",
				postcondition: { kind: "dom-change", timeoutMs: 450 },
			});
			await vi.waitFor(() => expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1));
			await vi.advanceTimersByTimeAsync(450);

			await expect(pending).resolves.toMatchObject({
				postcondition: { kind: "dom-change", status: "unmet", reason: "timeout" },
			});
			// One matching snapshot plus no more than three observations at a
			// conservative interval. A 25 ms loop would issue about nineteen.
			expect(snapshots).toBe(4);
		} finally {
			vi.useRealTimers();
		}
	});

	it("caps a slow native observation to the remaining postcondition deadline", async () => {
		vi.useFakeTimers();
		try {
			let snapshots = 0;
			const page = setupActHost(async (_sessionId, action, _args, _provider, signal) => {
				if (action === "snapshot") {
					snapshots += 1;
					if (snapshots === 1) {
						return { snapshot: '- button "Save" [ref=e1]', refs: { e1: { role: "button", name: "Save" } } };
					}
					return new Promise((_resolve, reject) => {
						signal?.addEventListener(
							"abort",
							() => reject(Object.assign(new Error("observation cancelled"), { code: "AGENT_BROWSER_CANCELLED" })),
							{ once: true },
						);
					});
				}
				return { clicked: true };
			});

			const pending = page.host.execute("sess-1", "act", {
				instruction: "save",
				postcondition: { kind: "dom-change", timeoutMs: 50 },
			});
			await vi.waitFor(() => expect(snapshots).toBe(2));
			await vi.advanceTimersByTimeAsync(50);

			await expect(pending).resolves.toMatchObject({
				postcondition: { kind: "dom-change", status: "unmet", reason: "timeout" },
			});
			expect(snapshots).toBe(2);
		} finally {
			vi.useRealTimers();
		}
	});

	it("propagates a native observation failure without redispatching the action", async () => {
		let snapshots = 0;
		const page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				snapshots += 1;
				if (snapshots === 1) {
					return { snapshot: '- button "Pay" [ref=e1]', refs: { e1: { role: "button", name: "Pay" } } };
				}
				throw Object.assign(new Error("browser target crashed"), { code: "AGENT_BROWSER_COMMAND_FAILED" });
			}
			return { clicked: true };
		});

		await expect(
			page.host.execute("sess-1", "act", {
				instruction: "pay",
				postcondition: { kind: "dom-change", timeoutMs: 50 },
			}),
		).rejects.toMatchObject({ code: "AGENT_BROWSER_COMMAND_FAILED", message: "browser target crashed" });
		expect(page.runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(1);
	});

	it("uses the action result as immediate evidence of a newly opened dialog", async () => {
		const page = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return { snapshot: '- button "Delete" [ref=e1]', refs: { e1: { role: "button", name: "Delete" } } };
			}
			if (action === "click") return { clicked: true, dialogOpened: true };
			throw new Error(`unexpected observation: ${action}`);
		});

		const result = await page.host.execute("sess-1", "act", {
			instruction: "delete",
			postcondition: { kind: "dialog", timeoutMs: 50 },
		});

		expect(result).toMatchObject({ postcondition: { kind: "dialog", status: "satisfied" } });
		expect(page.runAction.mock.calls.filter(([, action]) => action === "dialog")).toHaveLength(0);
	});

	it("uses --nth to check an unnamed checkbox instead of a button named Check", async () => {
		const { host, runAction } = setupActHost(async (_sessionId, action, args) => {
			if (action === "snapshot") {
				return {
					snapshot: '- checkbox [checked=false, ref=e1]\n- button "Check" [ref=e2]',
					refs: { e1: { role: "checkbox", name: "" }, e2: { role: "button", name: "Check" } },
				};
			}
			if (action === "check") return { checked: args.ref };
			return {};
		});

		const result = await host.execute("sess-1", "act", {
			instruction: "check the checkbox",
			action: "check",
			nth: 0,
		});

		expect(result).toMatchObject({ outcome: "matched", resolvedRef: "e1", retried: false });
		expect(runAction.mock.calls.filter(([, action]) => action === "check")).toEqual([
			expect.arrayContaining(["sess-1", "check", { ref: "e1" }]),
		]);
	});

	it("retries once after a stale reference: re-snapshots, re-matches, and completes", async () => {
		let snapshotCalls = 0;
		const { host, runAction } = setupActHost(async (_sessionId, action, args) => {
			if (action === "snapshot") {
				snapshotCalls += 1;
				const ref = snapshotCalls === 1 ? "e3" : "e5";
				return { snapshot: `- button "Submit" [ref=${ref}]`, refs: { [ref]: { role: "button", name: "Submit" } } };
			}
			if (action === "click") {
				if (args.ref === "e3") {
					parseAgentBrowserJSON(JSON.stringify({ success: false, data: null, error: "Unknown ref: e3" }));
				}
				return { clicked: args.ref };
			}
			return {};
		});

		const result = await host.execute("sess-1", "act", { instruction: "the submit button" });

		expect(result).toMatchObject({ outcome: "matched", resolvedRef: "e5", retried: true });
		expect(snapshotCalls).toBe(2);
		expect(runAction.mock.calls.filter(([, action]) => action === "click")).toHaveLength(2);
	});

	// Regression guard: retrying must not become an unbounded loop — exactly
	// one retry, then the real failure surfaces as itself (an honest error
	// beats silently doing nothing on a mutating action).
	it("rethrows a stale reference as-is once the single retry is also stale", async () => {
		let snapshotCalls = 0;
		const { host } = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				snapshotCalls += 1;
				return { snapshot: '- button "Submit" [ref=e3]', refs: { e3: { role: "button", name: "Submit" } } };
			}
			throw Object.assign(new Error("Reference no longer resolves"), { code: "STALE_REFERENCE" });
		});

		await expect(host.execute("sess-1", "act", { instruction: "the submit button" })).rejects.toMatchObject({
			code: "STALE_REFERENCE",
		});
		expect(snapshotCalls).toBe(2);
	});

	it("does not retry a generic command failure whose message says expired", async () => {
		let snapshotCalls = 0;
		let clickCalls = 0;
		const { host } = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				snapshotCalls += 1;
				return { snapshot: '- button "Submit" [ref=e3]', refs: { e3: { role: "button", name: "Submit" } } };
			}
			clickCalls += 1;
			throw Object.assign(new Error("Browser session expired"), { code: "AGENT_BROWSER_COMMAND_FAILED" });
		});

		await expect(host.execute("sess-1", "act", { instruction: "the submit button" })).rejects.toMatchObject({
			code: "AGENT_BROWSER_COMMAND_FAILED",
		});
		expect(snapshotCalls).toBe(1);
		expect(clickCalls).toBe(1);
	});

	it("reports ambiguous without performing an action when multiple elements match equally", async () => {
		const { host, runAction } = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") {
				return {
					snapshot: ['- button "Add to Cart" [ref=e1]', '- button "Add to Cart" [ref=e2]'].join("\n"),
					refs: { e1: { role: "button", name: "Add to Cart" }, e2: { role: "button", name: "Add to Cart" } },
				};
			}
			return {};
		});

		const result = await host.execute("sess-1", "act", { instruction: "add to cart" });

		expect(result).toMatchObject({ outcome: "ambiguous" });
		expect(runAction.mock.calls.every((call: unknown[]) => call[1] === "snapshot")).toBe(true);
	});

	it("reports no-match without performing an action when nothing scores", async () => {
		const { host, runAction } = setupActHost(async (_sessionId, action) => {
			if (action === "snapshot") return { snapshot: '- textbox "Email" [ref=e1]', refs: {} };
			return {};
		});

		const result = await host.execute("sess-1", "act", { instruction: "the submit button" });

		expect(result).toMatchObject({ outcome: "no-match" });
		expect(runAction.mock.calls.every((call: unknown[]) => call[1] === "snapshot")).toBe(true);
	});

	it("uses --nth to disambiguate a tie instead of declining", async () => {
		const { host } = setupActHost(async (_sessionId, action, args) => {
			if (action === "snapshot") {
				return {
					snapshot: ['- button "Add to Cart" [ref=e1]', '- button "Add to Cart" [ref=e2]'].join("\n"),
					refs: { e1: { role: "button", name: "Add to Cart" }, e2: { role: "button", name: "Add to Cart" } },
				};
			}
			if (action === "click") return { clicked: args.ref };
			return {};
		});

		const result = await host.execute("sess-1", "act", { instruction: "add to cart", nth: 1 });

		expect(result).toMatchObject({ outcome: "matched", resolvedRef: "e2" });
	});

	it("requires an instruction", async () => {
		const { host } = setupActHost(async () => ({}));
		await expect(host.execute("sess-1", "act", {})).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
	});

	it("rejects an unsupported verb rather than silently defaulting", async () => {
		const { host } = setupActHost(async () => ({}));
		await expect(host.execute("sess-1", "act", { instruction: "the submit button", action: "drag" })).rejects.toMatchObject({
			code: "INVALID_ARGUMENT",
		});
	});
});


describe("agent browser screenshot", () => {
	it("captures through the native runtime", async () => {
		const runtime = {
			runAction: vi.fn(async () => ({})),
			screenshot: vi.fn(async () => ({
				data: Buffer.from("png-snapshot").toString("base64"),
				width: 640,
				height: 480,
				untrustedExternalContent: true as const,
			})),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		const { host, webContents } = setupHost(runtime);

		const result = await host.execute("sess-1", "screenshot");

		expect(runtime.screenshot).toHaveBeenCalledWith(
			"sess-1",
			expect.objectContaining({ listTargets: expect.any(Function) }),
			undefined,
			{ annotate: false },
		);
		expect(result).toMatchObject({
			data: Buffer.from("png-snapshot").toString("base64"),
			width: 640,
			height: 480,
			untrustedExternalContent: true,
		});
		expect(webContents.capturePage).not.toHaveBeenCalled();
	});

	it("falls back to capturePage when native capture times out", async () => {
		const runtime = {
			runAction: vi.fn(async () => ({})),
			screenshot: vi.fn(async () => {
				throw Object.assign(new Error("agent-browser command timed out"), { code: "AGENT_BROWSER_TIMEOUT" });
			}),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		const { host, webContents } = setupHost(runtime);

		const result = await host.execute("sess-1", "screenshot");

		expect(webContents.capturePage).toHaveBeenCalledOnce();
		expect(result).toMatchObject({
			data: Buffer.from("png-snapshot").toString("base64"),
			width: 640,
			height: 480,
			untrustedExternalContent: true,
		});
	});

	it("reports SCREENSHOT_UNAVAILABLE when the fallback captures an empty image", async () => {
		const runtime = {
			runAction: vi.fn(async () => ({})),
			screenshot: vi.fn(async () => {
				throw Object.assign(new Error("agent-browser command timed out"), { code: "AGENT_BROWSER_TIMEOUT" });
			}),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		const { host, webContents } = setupHost(runtime);
		webContents.capturePage.mockResolvedValueOnce({
			isEmpty: () => true,
			toJPEG: () => Buffer.alloc(0),
			toPNG: () => Buffer.alloc(0),
			getSize: () => ({ width: 0, height: 0 }),
			resize: vi.fn(),
		} as never);

		await expect(host.execute("sess-1", "screenshot")).rejects.toMatchObject({
			code: "SCREENSHOT_UNAVAILABLE",
		});
	});

	it("cancels a hung screenshot fallback without blocking the next command", async () => {
		const runtime = {
			runAction: vi.fn(async () => ({ snapshot: "ok", refs: {} })),
			screenshot: vi.fn(async () => {
				throw Object.assign(new Error("agent-browser command timed out"), { code: "AGENT_BROWSER_TIMEOUT" });
			}),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		const { host, webContents } = setupHost(runtime);
		webContents.capturePage.mockReturnValueOnce(new Promise(() => undefined));
		const controller = new AbortController();
		const screenshot = host.execute("sess-1", "screenshot", undefined, controller.signal);

		await vi.waitFor(() => expect(webContents.capturePage).toHaveBeenCalledOnce());
		controller.abort();
		await expect(screenshot).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		await expect(host.execute("sess-1", "snapshot")).resolves.toMatchObject({ text: "ok", refs: {} });
	});

	it("propagates non-timeout native screenshot errors without falling back", async () => {
		const runtime = {
			runAction: vi.fn(async () => ({})),
			screenshot: vi.fn(async () => {
				throw Object.assign(new Error("agent-browser exited with code 1"), {
					code: "AGENT_BROWSER_COMMAND_FAILED",
				});
			}),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		const { host, webContents } = setupHost(runtime);

		await expect(host.execute("sess-1", "screenshot")).rejects.toMatchObject({
			code: "AGENT_BROWSER_COMMAND_FAILED",
		});
		expect(webContents.capturePage).not.toHaveBeenCalled();
	});
});


describe("agent browser network capture", () => {
	it("is opt-in and exposes only sanitized request metadata", async () => {
		const { debuggerListeners, debuggerSendCommand, host } = setupHost();
		const emitDebuggerMessage = (method: string, params: Record<string, unknown>) =>
			debuggerListeners.get("message")?.({} as never, method as never, params as never);

		emitDebuggerMessage("Network.requestWillBeSent", {
			requestId: "before-start",
			request: { method: "GET", url: "https://example.test/unobserved" },
		});
		expect(await host.execute("sess-1", "network-list")).toMatchObject({
			active: false,
			requestCount: 0,
			requests: [],
		});

		expect(await host.execute("sess-1", "network-start", { durationSeconds: 30 })).toMatchObject({
			active: true,
			metadataOnly: true,
			untrustedExternalContent: true,
			tabId: "t1",
			requestCount: 0,
			maxEntries: 200,
		});
		expect(debuggerSendCommand).toHaveBeenCalledWith("Network.enable");

		emitDebuggerMessage("Network.requestWillBeSent", {
			requestId: "request-1",
			timestamp: 12,
			wallTime: 1_750_000_000,
			type: "XHR",
			request: {
				method: "POST",
				url: "https://user:password@api.example.test/items?token=secret&page=2#private",
				postData: "must-not-be-stored",
				headers: {
					Authorization: "Bearer very-secret",
					Cookie: "session=very-secret",
					"Content-Type": "application/json",
					Origin: "https://app.example.test",
				},
			},
		});
		emitDebuggerMessage("Network.responseReceived", {
			requestId: "request-1",
			response: {
				status: 401,
				statusText: "Unauthorized",
				mimeType: "application/json",
				headers: {
					"Set-Cookie": "session=server-secret",
					"Content-Type": "application/json",
					"Access-Control-Allow-Origin": "https://app.example.test",
				},
			},
		});
		emitDebuggerMessage("Network.loadingFinished", { requestId: "request-1", timestamp: 12.125 });

		const result = (await host.execute("sess-1", "network-list")) as {
			requestCount: number;
			requests: Array<Record<string, unknown>>;
		};
		expect(result.requestCount).toBe(1);
		expect(result.requests[0]).toMatchObject({
			id: "n1",
			method: "POST",
			resourceType: "xhr",
			status: 401,
			durationMs: 125,
			requestHeaders: {
				"content-type": "application/json",
				origin: "https://app.example.test",
			},
			responseHeaders: {
				"content-type": "application/json",
				"access-control-allow-origin": "https://app.example.test",
			},
		});
		expect(JSON.stringify(result)).not.toContain("very-secret");
		expect(JSON.stringify(result)).not.toContain("must-not-be-stored");
		expect(JSON.stringify(result)).not.toContain("password");
		expect(result.requests[0]?.url).toContain("token=%5Bredacted%5D");
		expect(result.requests[0]).not.toHaveProperty("protocolRequestId");
		expect(result.requests[0]).not.toHaveProperty("startedMonotonic");

		expect(await host.execute("sess-1", "network-stop")).toMatchObject({
			active: false,
			stopReason: "stopped",
			requestCount: 1,
		});
		expect(debuggerSendCommand).not.toHaveBeenCalledWith("Network.disable");
	});

	it("retains only the newest 200 requests and validates the capture duration", async () => {
		const { debuggerListeners, host } = setupHost();
		await expect(host.execute("sess-1", "network-start", { durationSeconds: 0 })).rejects.toMatchObject({
			code: "INVALID_ARGUMENT",
		});
		await expect(host.execute("sess-1", "network-start", { durationSeconds: 301 })).rejects.toMatchObject({
			code: "INVALID_ARGUMENT",
		});

		await host.execute("sess-1", "network-start", { durationSeconds: 30 });
		const emitDebuggerMessage = debuggerListeners.get("message")!;
		for (let index = 0; index < 205; index++) {
			emitDebuggerMessage(
				{} as never,
				"Network.requestWillBeSent" as never,
				{
					requestId: `request-${index}`,
					request: {
						method: "GET",
						url: `https://api.example.test/items?index=${index}`,
					},
				} as never,
			);
		}

		const result = (await host.execute("sess-1", "network-list")) as {
			requestCount: number;
			requests: Array<{ id: string }>;
		};
		expect(result.requestCount).toBe(200);
		expect(result.requests[0]?.id).toBe("n6");
		expect(result.requests.at(-1)?.id).toBe("n205");
		await host.execute("sess-1", "network-stop");
	});

	it("expires automatically without enabling status or list checks", async () => {
		vi.useFakeTimers();
		try {
			const { debuggerSendCommand, host } = setupHost();
			expect(await host.execute("sess-1", "network-status")).toMatchObject({ active: false });
			expect(debuggerSendCommand).not.toHaveBeenCalledWith("Network.enable");

			await host.execute("sess-1", "network-start", { durationSeconds: 1 });
			await vi.advanceTimersByTimeAsync(1_000);

			expect(await host.execute("sess-1", "network-status")).toMatchObject({
				active: false,
				stopReason: "expired",
			});
			expect(debuggerSendCommand).not.toHaveBeenCalledWith("Network.disable");
		} finally {
			vi.useRealTimers();
		}
	});
});

