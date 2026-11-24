/**
 * The backend-neutral parts of agent browser actions: argument parsing, the
 * snapshot-delta baseline, act matching with its one stale-ref retry, and
 * result shaping. The Electron view host and the Chrome host (B849) both run
 * actions through these, so the command surface and results stay the same.
 */
import { matchInstruction, type ActCandidate } from "../browser-act-matcher";
import { actionPostcondition } from "./action-postconditions";
import { browserError, numberArg, stringArg } from "./args";
import { isStaleReferenceError, parseSnapshotDelta } from "./agent-results";
import { ACT_VERBS } from "./constants";
import { sanitizeBrowserTitle, sanitizeBrowserURL } from "./url";

export type NativeResult = Record<string, unknown>;
export type NativeRunner = (action: string, args?: Record<string, unknown>) => Promise<NativeResult>;

const ref = (args: Record<string, unknown>) => stringArg(args, "ref", "REFERENCE_REQUIRED", "ref is required");

/** Native arguments of a ref/key/scroll input action (all but unhighlight). */
export function inputNativeArgs(action: string, args: Record<string, unknown>): Record<string, unknown> {
	const human = args.human === true ? { human: true } : {};
	switch (action) {
		case "click":
			return { ref: ref(args), ...human };
		case "dblclick":
		case "focus":
		case "hover":
		case "highlight":
		case "scrollintoview":
		case "check":
		case "uncheck":
			return { ref: ref(args) };
		case "fill":
		case "type":
			return { ref: ref(args), text: stringArg(args, "text", "INVALID_ARGUMENT", "text is required", true) };
		case "press":
			return { key: stringArg(args, "key", "INVALID_ARGUMENT", "key is required") };
		case "drag":
			return { ref: ref(args), targetRef: stringArg(args, "targetRef", "REFERENCE_REQUIRED", "target ref is required"), ...human };
		case "scroll":
			return {
				direction: stringArg(args, "direction", "INVALID_ARGUMENT", "direction is required"),
				amount: numberArg(args.amount, 1, 5_000) || 600,
			};
		case "select":
			return { ref: ref(args), value: stringArg(args, "value", "INVALID_ARGUMENT", "value is required", true) };
	}
	throw new Error(`Unhandled input action: ${action}`);
}

/** get: reads one property and sanitizes url and title for transcripts. */
export async function getAction(args: Record<string, unknown>, runNative: NativeRunner): Promise<NativeResult> {
	const property = stringArg(args, "property", "INVALID_ARGUMENT", "property is required");
	const result = await runNative("get", {
		property,
		ref: typeof args.ref === "string" && args.ref.trim() ? args.ref : undefined,
	});
	const value = result.value ?? result[property];
	const safeValue =
		property === "url"
			? sanitizeBrowserURL(String(value ?? ""))
			: property === "title"
				? sanitizeBrowserTitle(String(value ?? ""))
				: value;
	return {
		...result,
		...(property === "url" || property === "title" ? { [property]: safeValue } : {}),
		value: safeValue,
	};
}

export type SnapshotBaseline = { tabId: string; interactive: boolean; revision: number };

/** Where a host keeps the tab in front and the snapshot-delta baseline. */
export type SnapshotState = {
	activeTabId(): string;
	baseline(): SnapshotBaseline | undefined;
	setBaseline(baseline: SnapshotBaseline | undefined): void;
};

export async function snapshotAction(args: Record<string, unknown>, state: SnapshotState, runNative: NativeRunner): Promise<NativeResult> {
	const interactive = Boolean(args.interactive);
	if (args.delta === true) {
		const tabId = state.activeTabId();
		const baseline = state.baseline();
		const current = baseline && baseline.tabId === tabId && baseline.interactive === interactive ? baseline : undefined;
		const request = async (full: boolean) => {
			const result = await runNative("snapshot", { interactive, delta: true, ...(full ? { full: true } : {}) });
			return { delta: parseSnapshotDelta(result.snapshot), boundary: result._boundary };
		};
		let full = args.full === true || !current;
		let response = await request(full);
		if (!full && response.delta.kind !== "full" && response.delta.baseRevision !== current?.revision) {
			full = true;
			response = await request(true);
		}
		if (full && response.delta.kind !== "full") {
			throw browserError("BROWSER_AUTOMATION_INVALID_OUTPUT", "Browser snapshot delta output was invalid");
		}
		// Anything that invalidated the baseline while this snapshot was in
		// flight (a background tab closing, a profile switch) must win over
		// a response captured before it.
		state.setBaseline(
			state.activeTabId() === tabId && state.baseline() === baseline
				? { tabId, interactive, revision: response.delta.revision }
				: undefined,
		);
		return { ...response.delta, ...(response.boundary ? { _boundary: response.boundary } : {}), untrustedExternalContent: true };
	}
	state.setBaseline(undefined);
	const result = await runNative("snapshot", { interactive });
	if (typeof result.snapshot !== "string") {
		throw browserError("BROWSER_AUTOMATION_INVALID_OUTPUT", "Browser snapshot output was invalid");
	}
	return {
		text: result.snapshot,
		refs: result.refs,
		...(result._boundary ? { _boundary: result._boundary } : {}),
		target: result.target,
		untrustedExternalContent: true,
	};
}

export type ActStep = {
	verb: string;
	value: string | undefined;
	postcondition: ReturnType<typeof actionPostcondition>;
	dispatched: { current: boolean };
};

export type ActDeps = {
	runNative: NativeRunner;
	/** Dispatches the matched action and reports it; sets step.dispatched once input is sent. */
	perform(step: ActStep, candidate: ActCandidate, retried: boolean, beforeSnapshot: string): Promise<unknown>;
	/** An unresolved outcome hands the agent a tree of its own: drop the delta baseline. */
	invalidateBaseline(): void;
};

export async function actAction(args: Record<string, unknown>, deps: ActDeps): Promise<unknown> {
	const instruction = stringArg(args, "instruction", "INVALID_ARGUMENT", "instruction is required");
	const postcondition = actionPostcondition(args);
	const verb = typeof args.action === "string" && args.action.trim() ? args.action.trim() : "click";
	if (!ACT_VERBS.has(verb)) throw browserError("INVALID_ARGUMENT", `Unsupported act verb: ${verb}`);
	const value = verb === "fill" || verb === "type" ? stringArg(args, "value", "INVALID_ARGUMENT", "value is required", true) : undefined;
	const nth = typeof args.nth === "number" && Number.isFinite(args.nth) ? Math.trunc(args.nth) : undefined;
	const snapshotOnce = async (): Promise<{ text: string; refs: unknown }> => {
		const result = await deps.runNative("snapshot", { interactive: true });
		if (typeof result.snapshot !== "string") {
			throw browserError("BROWSER_AUTOMATION_INVALID_OUTPUT", "Browser snapshot output was invalid");
		}
		return { text: result.snapshot, refs: result.refs };
	};
	const unresolved = (outcome: "ambiguous" | "no-match", candidates: unknown, snapshot: string) => {
		deps.invalidateBaseline();
		return {
			outcome,
			instruction,
			...(outcome === "ambiguous" ? { candidates } : {}),
			snapshot,
			untrustedExternalContent: true as const,
		};
	};
	const step: ActStep = { verb, value, postcondition, dispatched: { current: false } };

	const snapshot1 = await snapshotOnce();
	const match1 = matchInstruction(instruction, snapshot1.refs, { nth });
	if (match1.outcome !== "matched")
		return unresolved(match1.outcome, "candidates" in match1 ? match1.candidates : undefined, snapshot1.text);
	try {
		return await deps.perform(step, match1.candidate, false, snapshot1.text);
	} catch (error) {
		// Element attributes/positions on real pages shift between the snapshot
		// that resolved a ref and the action that uses it, going stale: the one
		// case worth a single automatic retry (re-snapshot, re-match the original
		// instruction, try once more). A stale-ref failure is known to happen
		// before dispatch; once the native action resolved, no observation
		// failure may replay a potentially non-idempotent action, and a second
		// stale ref rethrows rather than loop.
		if (step.dispatched.current || !isStaleReferenceError(error)) throw error;
		const snapshot2 = await snapshotOnce();
		const match2 = matchInstruction(instruction, snapshot2.refs, { nth });
		if (match2.outcome !== "matched")
			return unresolved(match2.outcome, "candidates" in match2 ? match2.candidates : undefined, snapshot2.text);
		return deps.perform(step, match2.candidate, true, snapshot2.text);
	}
}
