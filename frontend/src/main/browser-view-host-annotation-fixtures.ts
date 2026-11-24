import type { BrowserAnnotationDraft, BrowserAnnotationSession } from "../shared/browser-annotations";

// Annotation sessions the browser-view-host annotation tests feed the host.
export function annotationDraft(url = "http://localhost:4173/"): BrowserAnnotationDraft {
	return {
		kind: "comment",
		body: "Keep this text after refresh",
		target: {
			context: {
				url,
				tag: "button",
				classes: [],
				selector: "button#save",
				size: { width: 80, height: 30 },
				rect: { x: 10, y: 20, width: 80, height: 30 },
				computedStyle: {},
			},
		},
		adjustments: [],
	};
}


export function annotationSession(url = "http://localhost:4173/"): BrowserAnnotationSession {
	return {
		version: 1,
		page: { url, title: "Preview" },
		annotations: [],
		draft: annotationDraft(url),
		screenshots: [],
	};
}


export function submittedAnnotationSession(
	url = "http://localhost:5173/",
	comments: Array<{ body: string; selector: string }> = [{ body: "Make this button blue.", selector: "button" }],
): BrowserAnnotationSession {
	const timestamp = "2026-06-15T00:00:00Z";
	return {
		version: 1,
		page: { url, title: "Preview" },
		annotations: comments.map((comment, index) => ({
			id: `annotation-${index + 1}`,
			number: index + 1,
			kind: "comment",
			body: comment.body,
			target: {
				context: {
					url,
					tag: "button",
					classes: [],
					selector: comment.selector,
					size: { width: 80, height: 30 },
					rect: { x: 10, y: 20 + index * 40, width: 80, height: 30 },
					computedStyle: {},
				},
			},
			adjustments: [],
			createdAt: timestamp,
			updatedAt: timestamp,
		})),
		screenshots: [],
	};
}
