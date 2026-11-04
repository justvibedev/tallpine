import { describe, expect, it } from "vitest";
import { readAutomation } from "@tallpine/shared/chat/automation/read";
import { createBrowserAnnotationSession, formatBrowserAnnotationMessage, type BrowserAnnotationContext } from "./browser-annotations";
import { formatDiffSelectionMessage } from "./diff-selection";

const context: BrowserAnnotationContext = {
	url: "http://localhost:5173/", title: "Home", tag: "button", id: "save", classes: [], selector: "button#save",
	size: { width: 10, height: 10 }, rect: { x: 0, y: 0, width: 10, height: 10 }, visibleText: 'Say "hi"', ariaLabel: "Save", computedStyle: {},
};

describe("writer -> reader round trips", () => {
	for (const kind of ["comment", "adjustment"] as const) {
		it(`reads a browser ${kind} annotation back with its kind and quoted text`, () => {
			const session = createBrowserAnnotationSession("http://localhost:5173/", "Home");
			session.annotations.push({
				id: "a", number: 1, kind, body: "Looks off", target: { context },
				adjustments: kind === "adjustment" ? [{ property: "width", previousValue: "10px", value: "20px" }] : [],
				createdAt: "t", updatedAt: "t",
			});
			const message = formatBrowserAnnotationMessage({ viewId: "v", tabId: "t", pageKey: "k", sessionToken: "s", session });
			const read = readAutomation(message);
			expect(read?.kind).toBe("browserFeedback");
			if (read?.kind !== "browserFeedback") return;
			expect(read.annotations.items).toMatchObject([{ number: 1, kind, text: 'Say "hi"', comment: "Looks off" }]);
			expect(read.annotations.items[0]!.changes).toHaveLength(kind === "adjustment" ? 1 : 0);
		});
	}

	it("reads a diff selection back with its range", () => {
		const message = formatDiffSelectionMessage({
			instruction: "Simplify", filePath: "src/a.ts",
			lines: [{ kind: "add", oldNo: null, newNo: 3, text: "x" }, { kind: "context", oldNo: 4, newNo: 4, text: "y" }],
		});
		expect(readAutomation(message)).toEqual({ kind: "diffSelection", path: "src/a.ts", start: 3, end: 4, instruction: "Simplify" });
	});
});
