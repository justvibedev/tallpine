import { vi } from "vitest";
import type { AgentBrowserTargetProvider } from "./agent-browser-cdp-bridge";
import type { AgentBrowserRuntime } from "./agent-browser-runtime/runtime";
import { setupHost } from "./browser-view-host-test-helpers";
import { describeCdpCommandContract, type CdpContractHarness, type NativeMessages } from "./browser-view-host/cdp-contract-test-helpers";
import { describePageCdpCommandContract } from "./browser-view-host/page-cdp-contract-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});

function nativeRuntime(native: NativeMessages): AgentBrowserRuntime {
	return {
		runAction: vi.fn(async (_sessionId: string, action: string, args: Record<string, unknown>, provider: AgentBrowserTargetProvider) => {
			const answer = native(action);
			if (answer) return answer;
			if (action === "tab-select") await provider.activateTarget(String(args.tabId));
			return {};
		}),
		screenshot: vi.fn(),
		closeSession: vi.fn(async () => undefined),
		dispose: vi.fn(async () => undefined),
	} as unknown as AgentBrowserRuntime;
}

function inAppHarness(native?: NativeMessages): CdpContractHarness {
	const { host, debuggerListeners, debuggerSendCommand } = setupHost(native ? nativeRuntime(native) : undefined);
	const answers = new Map<string, (params: Record<string, unknown>) => unknown>();
	const base = debuggerSendCommand.getMockImplementation()!;
	debuggerSendCommand.mockImplementation(async (method, params) => {
		const answer = answers.get(method);
		return answer ? answer(params ?? {}) : base(method, params);
	});
	return {
		execute: (action, args = {}) => host.execute("sess-1", action, args),
		emit: (method, params) => debuggerListeners.get("message")?.({} as never, method as never, params as never),
		commands: () =>
			debuggerSendCommand.mock.calls.map(([method, params]) => ({ method, ...(params === undefined ? {} : { params }) })),
		respond: (method, answer) => {
			answers.set(method, answer);
		},
	};
}

describeCdpCommandContract("in-app browser", inAppHarness);
describePageCdpCommandContract("in-app browser", inAppHarness);
