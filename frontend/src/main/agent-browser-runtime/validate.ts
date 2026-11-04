/* Split out of agent-browser-runtime.ts (B297, P53). */

import { ALLOWED_COMMANDS, FORBIDDEN_FLAGS, MAX_ARGUMENTS, MAX_ARGUMENT_CHARS, isRecord, runtimeError } from "./common";

export function validContentBoundary(value: unknown): Record<string, string> | undefined {
	if (!isRecord(value) || typeof value.nonce !== "string" || !value.nonce || typeof value.origin !== "string") {
		return undefined;
	}
	return { nonce: value.nonce, origin: value.origin };
}

export function validateAgentBrowserArguments(args: string[]): void {
	if (args.length === 0) throw runtimeError("INVALID_ARGUMENT", "An agent-browser command is required");
	if (args.length > MAX_ARGUMENTS) throw runtimeError("INVALID_ARGUMENT", "Too many agent-browser arguments");
	if (args.some((arg) => typeof arg !== "string" || arg.length > MAX_ARGUMENT_CHARS)) {
		throw runtimeError("INVALID_ARGUMENT", "agent-browser arguments are invalid or too large");
	}
	const command = args[0].toLowerCase();
	if (!ALLOWED_COMMANDS.has(command)) {
		throw runtimeError("AGENT_BROWSER_COMMAND_BLOCKED", `agent-browser command is not enabled in Tallpine: ${command}`);
	}
	for (const arg of args) {
		const lower = arg.toLowerCase();
		if (FORBIDDEN_FLAGS.some((flag) => lower === flag || lower.startsWith(`${flag}=`))) {
			throw runtimeError("AGENT_BROWSER_COMMAND_BLOCKED", `agent-browser flag is managed by Tallpine: ${arg}`);
		}
	}
	if (command === "open" && args[1] && !args[1].startsWith("-")) {
		assertHTTPURL(args[1]);
	}
	if (command === "diff" && args[1]?.toLowerCase() !== "snapshot") {
		throw runtimeError("AGENT_BROWSER_COMMAND_BLOCKED", "Only snapshot diff is enabled in Tallpine");
	}
	if (command === "get" && args[1]?.toLowerCase() === "cdp-url") {
		throw runtimeError("AGENT_BROWSER_COMMAND_BLOCKED", "The private Tallpine CDP endpoint cannot be displayed");
	}
}

export function assertHTTPURL(raw: string): void {
	let url: URL;
	try {
		url = new URL(raw);
	} catch {
		throw runtimeError("INVALID_URL", "agent-browser navigation requires an explicit HTTP(S) URL");
	}
	if (url.protocol !== "http:" && url.protocol !== "https:") {
		throw runtimeError("BROWSER_URL_FORBIDDEN", `Unsupported browser URL scheme: ${url.protocol}`);
	}
}
