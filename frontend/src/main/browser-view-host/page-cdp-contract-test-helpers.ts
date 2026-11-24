/**
 * The CDP-direct page command contract (B857): back, forward, reload, eval and
 * upload, run against both backends with the same harness as
 * cdp-contract-test-helpers.ts.
 */
import { describe, expect, it, vi } from "vitest";
import type { CdpContractHarness, NativeMessages } from "./cdp-contract-test-helpers";

type Make = (native?: NativeMessages) => CdpContractHarness | Promise<CdpContractHarness>;

const ENTRIES = [
	{ id: 11, url: "https://a.example/one", title: "One" },
	{ id: 12, url: "https://a.example/two?token=secret", title: "Two" },
];

/** A two-page history at index `start`; navigating commits and emits frameNavigated. */
function withHistory(harness: CdpContractHarness, start: number, commit = true): void {
	let index = start;
	harness.respond("Page.getNavigationHistory", () => ({ currentIndex: index, entries: ENTRIES }));
	harness.respond("Page.navigateToHistoryEntry", (params) => {
		index = ENTRIES.findIndex((entry) => entry.id === params.entryId);
		if (commit) harness.emit("Page.frameNavigated", { frame: { id: "main", url: ENTRIES[index]!.url } });
		return {};
	});
	harness.respond("Page.reload", () => {
		if (commit) harness.emit("Page.frameNavigated", { frame: { id: "main", url: ENTRIES[index]!.url } });
		return {};
	});
}

export function describePageCdpCommandContract(backend: string, make: Make): void {
	const methods = (harness: CdpContractHarness) => harness.commands().map((command) => command.method);
	const sent = (harness: CdpContractHarness, method: string) => harness.commands().filter((command) => command.method === method);

	describe(`CDP-direct page commands (${backend})`, () => {
		it("back and forward walk the tab's history and report the committed page", async () => {
			const harness = await make();
			withHistory(harness, 1);
			const back = await harness.execute("back");
			expect(back).toMatchObject({ url: "https://a.example/one", title: "One", isLoading: false, target: expect.anything() });
			expect(sent(harness, "Page.navigateToHistoryEntry")).toEqual([{ method: "Page.navigateToHistoryEntry", params: { entryId: 11 } }]);
			await expect(harness.execute("back")).rejects.toMatchObject({ code: "BROWSER_NO_HISTORY" });
			const forward = (await harness.execute("forward")) as { url: string };
			expect(forward).toMatchObject({ title: "Two", isLoading: false });
			// The reported URL is sanitized like every other page URL.
			expect(forward.url).not.toContain("secret");
			await expect(harness.execute("forward")).rejects.toMatchObject({ code: "BROWSER_NO_HISTORY" });
			expect(sent(harness, "Page.navigateToHistoryEntry")).toHaveLength(2);
		});

		it("reload reloads the page and reports it still loading when it does not commit in time", async () => {
			const harness = await make();
			withHistory(harness, 0);
			expect(await harness.execute("reload")).toMatchObject({ url: "https://a.example/one", isLoading: false });
			expect(methods(harness)).toContain("Page.reload");
			vi.useFakeTimers();
			try {
				withHistory(harness, 0, false);
				const pending = harness.execute("reload");
				await vi.advanceTimersByTimeAsync(10_000);
				expect(await pending).toMatchObject({ isLoading: true });
			} finally {
				vi.useRealTimers();
			}
		});

		it("eval returns the value by JSON, terminates at its timeout, and caps the result", async () => {
			const harness = await make();
			await expect(harness.execute("eval", {})).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
			await expect(harness.execute("eval", { expression: "1", timeoutMs: 30_001 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
			await expect(harness.execute("eval", { expression: "x".repeat(100_001) })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
			expect(methods(harness)).not.toContain("Runtime.evaluate");

			harness.respond("Runtime.evaluate", () => ({ result: { type: "object", value: { title: "Hi", n: [1, 2] } } }));
			expect(await harness.execute("eval", { expression: "({title: document.title})" })).toMatchObject({
				type: "object",
				json: '{"title":"Hi","n":[1,2]}',
				value: { title: "Hi", n: [1, 2] },
				untrustedExternalContent: true,
				target: expect.anything(),
			});
			expect(sent(harness, "Runtime.evaluate").at(-1)?.params).toMatchObject({
				expression: "({title: document.title})",
				returnByValue: true,
				awaitPromise: true,
				userGesture: false,
				timeout: 5_000,
			});

			harness.respond("Runtime.evaluate", () => ({ result: { type: "string", value: "y".repeat(70_000) } }));
			const big = (await harness.execute("eval", { expression: "big" })) as Record<string, unknown>;
			expect(big).toMatchObject({ truncated: true, length: 70_002 });
			expect((big.json as string).length).toBe(64 * 1024);
			expect(big).not.toHaveProperty("value");

			harness.respond("Runtime.evaluate", () => ({ result: { type: "undefined" } }));
			expect(await harness.execute("eval", { expression: "void 0" })).toMatchObject({ type: "undefined", json: "undefined" });

			harness.respond("Runtime.evaluate", () => ({
				result: { type: "object" },
				exceptionDetails: { text: "Uncaught", exception: { description: "ReferenceError: nope is not defined" } },
			}));
			await expect(harness.execute("eval", { expression: "nope" })).rejects.toMatchObject({
				code: "BROWSER_EVAL_FAILED",
				message: "ReferenceError: nope is not defined",
			});

			vi.useFakeTimers();
			try {
				harness.respond("Runtime.evaluate", () => new Promise(() => undefined));
				const pending = harness.execute("eval", { expression: "new Promise(() => {})", timeoutMs: 100 });
				const failed = expect(pending).rejects.toMatchObject({ code: "BROWSER_EVAL_TIMEOUT" });
				await vi.advanceTimersByTimeAsync(1_100);
				await failed;
				expect(sent(harness, "Runtime.evaluate").at(-1)?.params).toMatchObject({ timeout: 100 });
			} finally {
				vi.useRealTimers();
			}
		});

		it("upload clicks with the file chooser intercepted and hands it the files", async () => {
			let chooserMode = "selectMultiple";
			let harness!: CdpContractHarness;
			harness = await make((action) => {
				if (action !== "click") return undefined;
				harness.emit("Page.fileChooserOpened", { frameId: "main", mode: chooserMode, backendNodeId: 7 });
				return { clicked: true };
			});
			await expect(harness.execute("upload", { ref: "e3", paths: ["relative.txt"] })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
			await expect(harness.execute("upload", { ref: "e3", paths: [] })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
			expect(methods(harness)).not.toContain("Page.setInterceptFileChooserDialog");

			const result = await harness.execute("upload", { ref: "e3", paths: ["/work/a.txt", "/tmp/s/b.png"] });
			expect(result).toMatchObject({ uploaded: 2, files: ["a.txt", "b.png"], multiple: true, target: expect.anything() });
			const flow = harness
				.commands()
				.filter((command) => command.method === "Page.setInterceptFileChooserDialog" || command.method === "DOM.setFileInputFiles");
			expect(flow).toEqual([
				{ method: "Page.setInterceptFileChooserDialog", params: { enabled: true } },
				{ method: "DOM.setFileInputFiles", params: { files: ["/work/a.txt", "/tmp/s/b.png"], backendNodeId: 7 } },
				{ method: "Page.setInterceptFileChooserDialog", params: { enabled: false } },
			]);

			// A single-file chooser given two files is answered empty, never left pending.
			chooserMode = "selectSingle";
			await expect(harness.execute("upload", { ref: "e3", paths: ["/work/a.txt", "/work/b.txt"] })).rejects.toMatchObject({
				code: "INVALID_ARGUMENT",
			});
			expect(sent(harness, "DOM.setFileInputFiles").at(-1)?.params).toEqual({ files: [], backendNodeId: 7 });
			expect(sent(harness, "Page.setInterceptFileChooserDialog").at(-1)?.params).toEqual({ enabled: false });
		});

		it("upload fails when the click opens no file chooser, and turns interception off again", async () => {
			const harness = await make((action) => (action === "click" ? { clicked: true } : undefined));
			vi.useFakeTimers();
			try {
				const pending = harness.execute("upload", { ref: "e1", paths: ["/work/a.txt"] });
				const failed = expect(pending).rejects.toMatchObject({ code: "BROWSER_FILE_CHOOSER_NOT_OPENED" });
				await vi.advanceTimersByTimeAsync(5_000);
				await failed;
			} finally {
				vi.useRealTimers();
			}
			expect(methods(harness)).not.toContain("DOM.setFileInputFiles");
			expect(sent(harness, "Page.setInterceptFileChooserDialog").at(-1)?.params).toEqual({ enabled: false });
		});
	});
}
