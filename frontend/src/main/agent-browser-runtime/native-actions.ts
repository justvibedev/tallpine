/* Split out of agent-browser-runtime.ts (B297, P53). */

import { AgentBrowserJSONResult, isRecord, runtimeError } from "./common";
import { assertHTTPURL, validContentBoundary } from "./validate";

export function nativeArgumentsForAction(action: string, args: Record<string, unknown>): string[] {
	const ref = () => nativeRef(stringValue(args.ref, "ref is required"));
	switch (action) {
		case "open":
			return ["open", httpURL(stringValue(args.url, "url is required"))];
		case "snapshot":
			return [
				"snapshot",
				...(args.interactive === true ? ["--interactive"] : []),
				"--compact",
				...(args.delta === true ? ["--delta", ...(args.full === true ? ["--full"] : [])] : []),
			];
		case "click":
			return ["click", ref(), ...(args.human === true ? ["--human"] : [])];
		case "dblclick":
		case "focus":
		case "hover":
		case "highlight":
		case "scrollintoview":
		case "check":
		case "uncheck":
			return [action, ref()];
		case "fill":
		case "type":
			return [action, ref(), stringValue(args.text, "text is required", true)];
		case "press":
			return ["press", stringValue(args.key, "key is required")];
		case "drag":
			return [
				"drag",
				ref(),
				nativeRef(stringValue(args.targetRef, "target ref is required")),
				...(args.human === true ? ["--human"] : []),
			];
		case "select":
			return ["select", ref(), stringValue(args.value, "value is required", true)];
		case "tabs":
			return ["tab", "list"];
		case "tab-new": {
			const url = optionalStringValue(args.url);
			return ["tab", "new", ...(url ? [httpURL(url)] : [])];
		}
		case "tab-select":
			return ["tab", stringValue(args.tabId, "tabId is required")];
		case "tab-close": {
			const tabId = optionalStringValue(args.tabId);
			return ["tab", "close", ...(tabId ? [tabId] : [])];
		}
		case "scroll": {
			const direction = stringValue(args.direction, "direction is required").toLowerCase();
			if (!["up", "down", "left", "right"].includes(direction)) {
				throw runtimeError("INVALID_ARGUMENT", "direction must be up, down, left, or right");
			}
			const amount = numberValue(args.amount, 600, 1, 5_000);
			return ["scroll", direction, String(amount)];
		}
		case "get": {
			const property = stringValue(args.property, "property is required").toLowerCase();
			if (!["url", "title", "text", "value", "checked"].includes(property)) {
				throw runtimeError("INVALID_ARGUMENT", `Unsupported browser property: ${property}`);
			}
			const target = optionalStringValue(args.ref);
			if (["url", "title"].includes(property) && target) {
				throw runtimeError("INVALID_ARGUMENT", `${property} does not accept an element ref`);
			}
			if (["value", "checked"].includes(property) && !target) {
				throw runtimeError("REFERENCE_REQUIRED", `${property} requires an element ref`);
			}
			return ["get", property, ...(target ? [nativeRef(target)] : [])];
		}
		case "wait":
			return nativeWaitArguments(args);
		case "frame": {
			const target = stringValue(args.target, "frame target is required");
			return ["frame", target === "main" ? target : nativeRef(target)];
		}
		case "dialog": {
			const operation = stringValue(args.operation, "dialog operation is required").toLowerCase();
			if (!["accept", "dismiss", "status"].includes(operation)) {
				throw runtimeError("INVALID_ARGUMENT", "dialog operation must be accept, dismiss, or status");
			}
			const text = optionalStringValue(args.text);
			return ["dialog", operation, ...(text ? [text] : [])];
		}
		case "console":
		case "errors":
			return [action];
		default:
			throw runtimeError("INVALID_ARGUMENT", `Unsupported native browser action: ${action}`);
	}
}

export function nativeWaitArguments(args: Record<string, unknown>): string[] {
	const timeout = String(numberValue(args.timeoutMs, 10_000, 1, 55_000));
	if (typeof args.text === "string" && args.text) return ["wait", "--text", args.text, "--timeout", timeout];
	if (typeof args.textGone === "string" && args.textGone) {
		return ["wait", `text=${args.textGone}`, "--state", "hidden", "--timeout", timeout];
	}
	if (typeof args.selector === "string" && args.selector) {
		return ["wait", args.selector, "--timeout", timeout];
	}
	if (typeof args.selectorGone === "string" && args.selectorGone) {
		return ["wait", args.selectorGone, "--state", "detached", "--timeout", timeout];
	}
	if (typeof args.url === "string" && args.url) return ["wait", "--url", `**${args.url}**`, "--timeout", timeout];
	if (args.load === true) return ["wait", "--load", "load", "--timeout", timeout];
	if (typeof args.stableMs === "number" && args.stableMs > 0) {
		const stableMs = numberValue(args.stableMs, 500, 1, 60_000);
		const expression = `(() => { const key = "__tallpineDomStability"; const now = performance.now(); let state = globalThis[key]; if (!state) { state = { lastMutation: now }; state.observer = new MutationObserver(() => { state.lastMutation = performance.now(); }); state.observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true }); globalThis[key] = state; } if (performance.now() - state.lastMutation < ${stableMs}) return false; state.observer.disconnect(); delete globalThis[key]; return true; })()`;
		return ["wait", "--fn", expression, "--timeout", timeout];
	}
	if (typeof args.ms === "number" && args.ms > 0) return ["wait", String(args.ms)];
	throw runtimeError("INVALID_ARGUMENT", "A wait condition is required");
}

export function parseAgentBrowserJSON(stdout: string): AgentBrowserJSONResult {
	let envelope: unknown;
	try {
		envelope = JSON.parse(stdout);
	} catch {
		throw runtimeError("AGENT_BROWSER_INVALID_OUTPUT", "Browser automation returned invalid structured output");
	}
	if (!isRecord(envelope)) throw runtimeError("AGENT_BROWSER_INVALID_OUTPUT", "Browser automation returned invalid output");
	if (envelope.success === false) {
		throw runtimeError(
			staleReferenceCode(envelope.error) ?? waitTimeoutCode(envelope.error) ?? "AGENT_BROWSER_COMMAND_FAILED",
			stringError(envelope.error) || "Browser automation failed",
		);
	}
	const boundary = validContentBoundary(envelope._boundary);
	const result: Record<string, unknown> = isRecord(envelope.data) ? { ...envelope.data } : { value: envelope.data };
	// `_boundary` is native-output metadata. Never forward a page-shaped field
	// with the same name as trusted metadata, but preserve the root field emitted
	// by agent-browser 0.38.1 so downstream adapters retain its nonce and origin.
	delete result._boundary;
	if (boundary) result._boundary = boundary;
	return { ...result, untrustedExternalContent: true };
}

export function nativeRef(value: string): string {
	return /^@?e\d+$/i.test(value) ? `@${value.replace(/^@/, "")}` : value;
}

export function stringValue(value: unknown, message: string, allowEmpty = false): string {
	if (typeof value !== "string" || (!allowEmpty && !value.trim())) throw runtimeError("INVALID_ARGUMENT", message);
	return allowEmpty ? value : value.trim();
}

export function optionalStringValue(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

export function numberValue(value: unknown, fallback: number, minimum: number, maximum: number): number {
	if (value === undefined) return fallback;
	if (typeof value !== "number" || !Number.isFinite(value) || value < minimum || value > maximum) {
		throw runtimeError("INVALID_ARGUMENT", `Numeric argument must be between ${minimum} and ${maximum}`);
	}
	return Math.round(value);
}

export function httpURL(value: string): string {
	assertHTTPURL(value);
	return value;
}

export function pngDimensions(image: Buffer): { width: number; height: number } {
	if (image.length < 24 || image.toString("ascii", 1, 4) !== "PNG") {
		throw runtimeError("AGENT_BROWSER_INVALID_OUTPUT", "Browser automation returned an invalid PNG screenshot");
	}
	return { width: image.readUInt32BE(16), height: image.readUInt32BE(20) };
}

export function stringError(value: unknown): string {
	if (typeof value === "string") return value;
	if (isRecord(value) && typeof value.message === "string") return value.message;
	return "";
}

export const STALE_REFERENCE_MESSAGE = /^(?:Unknown ref:|Could not locate element with)/;

export const WAIT_TIMEOUT_MESSAGE = /^Wait timed out after \d+ms$/;

// Preserve only the stale-reference signal needed by `act`. Other native
// command failures stay generic so tab drift and close recovery paths continue
// to recognize AGENT_BROWSER_COMMAND_FAILED.
export function staleReferenceCode(value: unknown): "STALE_REFERENCE" | undefined {
	if (isRecord(value) && value.code === "STALE_REFERENCE") return "STALE_REFERENCE";
	return typeof value === "string" && STALE_REFERENCE_MESSAGE.test(value) ? "STALE_REFERENCE" : undefined;
}

export function waitTimeoutCode(value: unknown): "AGENT_BROWSER_WAIT_TIMEOUT" | undefined {
	return WAIT_TIMEOUT_MESSAGE.test(stringError(value)) ? "AGENT_BROWSER_WAIT_TIMEOUT" : undefined;
}
