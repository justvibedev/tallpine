import { describe, expect, it, vi } from "vitest";


import { previewFilePath } from "./api";

describe("daemon preview-files route", () => {
	it("escapes the session id and each path segment while keeping the separators", () => {
		expect(previewFilePath("sess 1", ".tallpine/attachments/attachment-a b.png")).toBe(
			"/api/v1/sessions/sess%201/preview/files/.tallpine/attachments/attachment-a%20b.png",
		);
		expect(previewFilePath("s", "dist/index.html")).toBe("/api/v1/sessions/s/preview/files/dist/index.html");
	});
});
