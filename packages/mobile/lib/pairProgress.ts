// Plain-language progress for pairing a phone with a computer.
//
// The scanner used to show one "Connecting…" spinner from the scan until the
// screen closed, so when something stalled nobody could tell where. Pairing is
// four steps the user can recognise; each is shown as it starts and ends, and
// every change is also kept as a short log the user can copy and share.
//
// The log is built only from what this module is told, and every line passes
// through redactSecrets: no pairing code, device token or certificate pin may
// end up on the clipboard.

export type PairStepId = "find" | "security" | "paired" | "load";
export type PairStepState = "pending" | "active" | "done" | "failed";

export const PAIR_STEPS: readonly { id: PairStepId; name: string; active: string; done: string; failed: string }[] = [
	{ id: "find", name: "Find computer", active: "Looking for your computer", done: "Found your computer", failed: "Couldn't find your computer" },
	{ id: "security", name: "Security code", active: "Checking security code", done: "Security code checked", failed: "Security code not accepted" },
	{ id: "paired", name: "Pairing", active: "Pairing", done: "Paired", failed: "Pairing didn't finish" },
	{ id: "load", name: "Load sessions", active: "Loading sessions", done: "Sessions loaded", failed: "Sessions didn't load" },
];

export type PairLogLine = { at: number; text: string };

export type PairProgress = {
	steps: Record<PairStepId, PairStepState>;
	log: PairLogLine[];
};

/** Keeps the copied log short enough to paste into a chat. */
const MAX_LOG_LINES = 40;

export function startPairProgress(now: number): PairProgress {
	return {
		steps: { find: "pending", security: "pending", paired: "pending", load: "pending" },
		log: [{ at: now, text: "Code scanned" }],
	};
}

/** Whether any step has started, i.e. there is something worth showing. */
export function hasStarted(progress: PairProgress | null): progress is PairProgress {
	return !!progress && PAIR_STEPS.some((s) => progress.steps[s.id] !== "pending");
}

/**
 * Moves one step to `state` and records it. A `note` adds what the user can
 * act on ("at 192.168.1.20:65221", "the code was already used"); it is
 * redacted like everything else.
 */
export function markStep(progress: PairProgress, id: PairStepId, state: PairStepState, now: number, note?: string): PairProgress {
	const step = PAIR_STEPS.find((s) => s.id === id)!;
	const label = state === "done" ? step.done : state === "failed" ? step.failed : state === "active" ? step.active : null;
	const steps = { ...progress.steps, [id]: state };
	if (!label) return { ...progress, steps };
	return { steps, log: appendLog(progress.log, { at: now, text: note ? `${label} (${note})` : label }) };
}

/** Records a line that is not a step change (a retry, a timeout). */
export function noteProgress(progress: PairProgress, text: string, now: number): PairProgress {
	return { ...progress, log: appendLog(progress.log, { at: now, text }) };
}

function appendLog(log: PairLogLine[], line: PairLogLine): PairLogLine[] {
	const next = [...log, { at: line.at, text: redactSecrets(line.text) }];
	return next.length > MAX_LOG_LINES ? next.slice(next.length - MAX_LOG_LINES) : next;
}

/**
 * Removes anything that looks like a credential: pairing links (their fragment
 * is the whole offer, token included), bearer values, and long runs of hex or
 * base64 such as tokens and certificate pins. Addresses and ports stay; they
 * are what makes the log useful and are not secret on a home network.
 */
export function redactSecrets(text: string): string {
	return text
		.replace(/(tallpine-mobile:\/\/pair|https?:\/\/[^\s#]+\/pair)#\S*/gi, "$1#[hidden]")
		.replace(/\b(bearer)\s+\S+/gi, "$1 [hidden]")
		.replace(/\b(token|code|pin|password)(\s*[:=]\s*)\S+/gi, "$1$2[hidden]")
		.replace(/[A-Za-z0-9_\-+/=]{24,}/g, "[hidden]");
}

function clock(at: number): string {
	const d = new Date(at);
	const pad = (n: number) => String(n).padStart(2, "0");
	return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** The text "Copy details" puts on the clipboard. */
export function formatPairDetails(progress: PairProgress, context: { app: string; platform: string }): string {
	const lines = [
		`${context.app} pairing details (${context.platform})`,
		...PAIR_STEPS.map((s) => `- ${s.name}: ${progress.steps[s.id]}`),
		"",
		// Redacted again on the way out: the step lines above are fixed text.
		...progress.log.map((l) => `${clock(l.at)} ${redactSecrets(l.text)}`),
	];
	return lines.join("\n");
}
