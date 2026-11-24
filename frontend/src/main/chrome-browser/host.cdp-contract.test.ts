import { describeCdpCommandContract, type CdpContractHarness, type NativeMessages } from "../browser-view-host/cdp-contract-test-helpers";
import { describePageCdpCommandContract } from "../browser-view-host/page-cdp-contract-test-helpers";
import { setupChromeHost } from "./chrome-test-helpers";

async function chromeHarness(native?: NativeMessages): Promise<CdpContractHarness> {
	const { host, connection } = setupChromeHost((action) => native?.(action) ?? { ok: true });
	// The session's tab (T1, page session S-T1) exists before the contract emits page events.
	await host.execute("s1", "snapshot");
	const pageSession = () => "S-T1";
	return {
		execute: (action, args = {}) => host.execute("s1", action, args),
		emit: (method, params) => connection().emit({ method, params, sessionId: pageSession() }),
		commands: () =>
			connection()
				.sent.filter((entry) => entry.sessionId === pageSession())
				.map(({ method, params }) => ({ method, ...(Object.keys(params).length ? { params } : {}) })),
		respond: (method, answer) => {
			connection().handlers[method] = (params) => answer(params) as Record<string, unknown>;
		},
	};
}

describeCdpCommandContract("Chrome mode", chromeHarness);
describePageCdpCommandContract("Chrome mode", chromeHarness);
