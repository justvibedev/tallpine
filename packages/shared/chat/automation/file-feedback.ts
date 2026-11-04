// Reads the file-feedback and diff-selection handoffs (packages/shared/files/file-annotations.ts and
// frontend/src/shared/diff-selection.ts write them). Desktop and phone share this reader.

export type FileFeedback = {
	path: string;
	line?: number;
	/** The commented line's code, when the comment was on one. */
	code?: string;
	/** Text selected in a rendered view, when the comment was on a selection. */
	quote?: string;
	/** The comment was on the whole file, not a line. */
	wholeFile?: boolean;
	feedback: string;
};

export type FileFeedbackAutomation =
	| { kind: "fileFeedback"; comments: FileFeedback[] }
	| { kind: "diffSelection"; path: string; start?: number; end?: number; instruction: string };

// The "- Key: value" lines file-annotations' commentLines writes under "File context:".
export function fileFeedback(block: string): FileFeedback | null {
	const feedback = /^Feedback:\n([\s\S]*?)\n\nFile context:\n/.exec(block.trim());
	if (!feedback) return null;
	const fields = new Map<string, string>();
	for (const line of block.slice(block.indexOf("File context:\n")).split("\n")) {
		const field = /^- ([A-Za-z ]+): (.*)$/.exec(line);
		if (field) fields.set(field[1]!, field[2]!);
	}
	const path = fields.get("Path");
	if (!path) return null;
	// Only a new-side line exists in the file Files opens; a deleted line has none.
	const location = fields.get("Location");
	const line = Number(fields.get("New line") ?? /^New side, line (\d+)$/.exec(location ?? "")?.[1]);
	const code = fields.get("Code");
	return {
		path,
		line: Number.isSafeInteger(line) && line > 0 ? line : undefined,
		code: code && code !== "(blank line)" ? code : undefined,
		quote: fields.get("Selected text"),
		wholeFile: location === "Entire file" || undefined,
		feedback: feedback[1]!.trim() === "(empty)" ? "" : feedback[1]!.trim(),
	};
}

// diff-selection's message; its instruction and path are each one line.
const DIFF_SELECTION = /^The user selected lines in the diff viewer and asked for a change\.\n\nInstruction:\n([^\n]*)\n\nFile: ([^\n]+)\n(?:(?:Comparison scope|Workspace version|File fingerprint): [^\n]*\n)*Selected lines (?:(\d+)-(\d+)|[^\n]*):\n\n/;


export function readFileFeedback(text: string): FileFeedbackAutomation | null {
	if (text.startsWith("The user left inline feedback while reviewing a file in Tallpine and asked for a change.\n")) {
		const comment = fileFeedback(text.slice(text.indexOf("\n\n") + 2).replace(/\n\nApply this feedback in the current workspace\.[^\n]*$/, ""));
		return comment ? { kind: "fileFeedback", comments: [comment] } : null;
	}
	if (/^The user left \d+ inline feedback comments? while reviewing files in Tallpine and asked for changes\./.test(text)) {
		const blocks = text.replace(/\n\nApply every comment in the current workspace\.[^\n]*$/, "").split(/\n\nComment \d+:\n/).slice(1);
		const comments = blocks.map(fileFeedback);
		return comments.length > 0 && comments.every(Boolean) ? { kind: "fileFeedback", comments: comments as FileFeedback[] } : null;
	}
	const selection = DIFF_SELECTION.exec(text);
	if (selection) {
		const [, instruction, path, start, end] = selection;
		// diff-selection numbers the range by the new side unless an end of it is a
		// deleted line ("- " row); only new-side numbers name a line in the file.
		const rows = text.slice(selection[0].length).split("\n");
		const truncated = rows.at(-1) === "[truncated]";
		const newSide = rows.some((row) => !row.startsWith("-"));
		const startsNew = newSide && rows[0] !== undefined && !rows[0].startsWith("-");
		const endsNew = newSide && !truncated && !rows.at(-1)!.startsWith("-");
		return {
			kind: "diffSelection",
			path: path!,
			start: start && startsNew ? Number(start) : undefined,
			end: start && startsNew && endsNew ? Number(end) : undefined,
			instruction: instruction === "(empty)" ? "" : instruction!,
		};
	}
	return null;
}
