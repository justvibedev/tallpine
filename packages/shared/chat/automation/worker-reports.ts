// Worker reports read back from the fixed text the daemon writes
// (backend/internal/service/report PreparedBatch.Render). Desktop and phone share this
// reader; packages/shared/testdata/report-render.golden is written by a Go test and read
// by this module's test, so a format change fails on both sides.
// ponytail: parses the daemon's prompt text, so a worker's note can mimic an entry; structured report rows would end the guess.

export interface WorkerUpdate {
	key: string;
	/** The report state (`needs_input`, `stuck`, `done`, ...); absent for a direct message. */
	state?: string;
	name: string;
	sessionId?: string;
	projectId?: string;
	body: string;
	/** True when the text came framed as another project's orchestrator (`<<orchestrator:…>>`). */
	fromOrchestrator?: boolean;
	createdAt: string;
}

const REPORTS_HEADER = "Reports since your previous turn:";
// Matched by prefix: the daemon has reworded the rest of this line (timestamps, then <<worker>> framing) before.
const TIMESTAMPED_REPORTS_HEADER = "Worker reports (";
// The `<<worker:ID n=…>>` / `<</worker n=…>>` markers (whole-line or mid-line) that frame a worker's untrusted text.
export const FRAME_LINE = /<<(?:worker|orchestrator):[^\n]*? n=[0-9a-f]+>>\n?|\n?<<\/(?:worker|orchestrator) n=[0-9a-f]+>>/g;
// Every in-app session-link scheme a stored message can carry: current, then the pre-rename ones.
const SCHEME = "(?:tallpine|vo|ao)";
// One entry per `[state] [name](tallpine://sessions/<project>/<session>)`, or `[state] tallpine://sessions/<project>/<session>`
// with no name, as older daemons wrote it.
const REPORT_ENTRY = new RegExp(String.raw`\n\n\[([a-z_]+)\] (?:\[((?:\\.|[^\]\\])*)\]\(${SCHEME}:\/\/sessions\/([^/)\s]+)\/([^)\s]+)\)|${SCHEME}:\/\/sessions\/([^/\s]+)\/(\S+))(?: \(repeated \d+ times\))?(?: — (\d{4}-\d{2}-\d{2}T[\d:.]+Z))?`, "g");
// The `artifact: <path>` line a report gets per attached artifact.
const ARTIFACT_LINE = /^artifact: (\S+)$/;
const WORKER_REPORT_OPEN = "<ao-worker-reports>";
const WORKER_REPORT_CLOSE = "</ao-worker-reports>";

function decode(value: string): string {
	try {
		return decodeURIComponent(value);
	} catch {
		return value;
	}
}

export function parseReports(text: string, createdAt: string, keyPrefix: string): WorkerUpdate[] {
	if (!text.startsWith(REPORTS_HEADER) && !text.startsWith(TIMESTAMPED_REPORTS_HEADER)) return [];
	const entries = [...text.matchAll(REPORT_ENTRY)];
	return entries.map((entry, index) => {
		const sessionId = decode(entry[4] ?? entry[6]!);
		return {
			key: `${keyPrefix}:${index}`,
			state: entry[1],
			name: entry[2]?.replace(/\\(.)/g, "$1") ?? sessionId,
			projectId: decode(entry[3] ?? entry[5]!),
			sessionId,
			body: text.slice(entry.index! + entry[0].length, entries[index + 1]?.index ?? text.length).replace(FRAME_LINE, "").trim(),
			createdAt: entry[7] && Number.isFinite(Date.parse(entry[7])) ? entry[7] : createdAt,
		};
	});
}

/** What the user typed, without the worker reports the daemon folded in because the orchestrator was busy. */
export function humanVisibleText(text: string): string {
	if (!text.endsWith(WORKER_REPORT_CLOSE)) return text;
	const reportStart = text.lastIndexOf(`\n\n${WORKER_REPORT_OPEN}\n`);
	return reportStart < 0 ? text : text.slice(0, reportStart);
}

/** The reports folded into a human message, unwrapped from their envelope; "" when there are none. */
export function foldedReportsEnvelope(text: string): string {
	const visible = humanVisibleText(text);
	if (visible === text) return "";
	return text.slice(visible.length).trim().replace(/^<ao-worker-reports>/, "").replace(/<\/ao-worker-reports>$/, "").trim();
}

const plain = (text: string) =>
	text.replace(/\[([^\]]+)\]\([^)]*\)/g, "$1").replace(/`([^`]*)`/g, "$1").replace(/\*\*([^*]+)\*\*/g, "$1");

export function splitArtifacts(body: string): { text: string; artifacts: string[] } {
	const artifacts: string[] = [];
	const lines = body.split("\n").filter((line) => {
		const path = ARTIFACT_LINE.exec(line.trim())?.[1];
		if (path) artifacts.push(path);
		return !path;
	});
	return { text: lines.join("\n").trim(), artifacts };
}

/** A short first line followed by more is the worker's headline; otherwise its first sentence is. */
export function splitHeadline(body: string): { headline: string; detail: string } {
	const newline = body.indexOf("\n");
	const firstLine = (newline < 0 ? body : body.slice(0, newline)).trim();
	if (newline > 0 && firstLine.length <= 100) {
		return { headline: plain(firstLine), detail: body.slice(newline + 1).trim() };
	}
	const sentence = /^.+?[.;](?=\s)/.exec(firstLine)?.[0] ?? firstLine;
	return { headline: plain(sentence.replace(/[.;]$/, "")), detail: body };
}

/** Drops the untrusted-text frame markers around a message body. */
export function stripFrames(text: string): string {
	return text.replace(FRAME_LINE, "");
}

/** The frame opener of a message from another project's orchestrator, if the text has one. */
export const ORCHESTRATOR_FRAME = /^\s*(?:\[from [^\]]*\]\s*)?<<orchestrator:([^\s>]+?) n=[0-9a-f]+>>/;
