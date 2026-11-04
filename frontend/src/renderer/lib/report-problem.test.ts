import { afterEach, describe, expect, it, vi } from "vitest";
import {
	collectReportProblemDiagnostics,
	formatReportProblemDraft,
	formatReportTimestamp,
	reportProblemDestinationUrl,
	type ReportProblemDiagnostics,
	type ReportProblemInput,
	type ReportProblemOutput,
} from "./report-problem";

const diagnostics: ReportProblemDiagnostics = {
	appVersion: "1.2.3-test",
	buildMode: "dev",
	daemonState: "ready",
	generatedAt: "2026-07-02T00:00:00.000Z",
	platform: "darwin-arm64",
	routeSurface: "session_detail",
};

const completeInput: ReportProblemInput = {
	summary: "Terminal keeps reconnecting after daemon restart",
	details:
		"Open /Users/alice/work/secret-app and visit http://127.0.0.1:5173/?token=secret-token. The app should reconnect without losing the current route.",
};

describe("report problem drafts", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		window.location.hash = "";
	});

	it("formats GitHub, Discord, and email drafts with user text plus safe diagnostics", () => {
		const outputs: ReportProblemOutput[] = ["github"];

		for (const output of outputs) {
			const draft = formatReportProblemDraft(completeInput, diagnostics, output);

			expect(draft).toContain("Terminal keeps reconnecting after daemon restart");
			expect(draft).toContain("The app should reconnect without losing the current route.");
			expect(draft).toContain("Tallpine version: 1.2.3-test");
			expect(draft).toContain("Daemon: ready");
			expect(draft).toContain("Route surface: session_detail");
		}
	});

	it("shows the generated time as readable local time with a UTC reference", () => {
		expect(formatReportTimestamp("2026-10-21T20:50:24.025Z", "America/Los_Angeles")).toBe(
			"Oct 21, 2026, 1:50:24 PM PDT (2026-10-21 20:50:24 UTC)",
		);
		expect(formatReportTimestamp("2026-10-21T20:50:24.025Z", "Asia/Kolkata")).toBe(
			"Oct 22, 2026, 2:20:24 AM GMT+5:30 (2026-10-21 20:50:24 UTC)",
		);
		expect(formatReportTimestamp("unknown")).toBe("unknown");

		for (const output of ["github"] as const) {
			const draft = formatReportProblemDraft(completeInput, diagnostics, output);
			expect(draft).toContain("(2026-07-02 00:00:00 UTC)");
			expect(draft).not.toContain("2026-07-02T00:00:00.000Z");
		}
	});

	it("redacts local paths, local URLs, and token-like values from drafts", () => {
		const draft = formatReportProblemDraft(
			{
				summary: "Setup fails with OPENAI_API_KEY=sk-proj-secret and password=hunter2",
				details:
					"Repo is C:\\Users\\alice\\repo and file:///Users/alice/private/index.html?api_key=abc failed. Tell me what prerequisite is missing.",
			},
			{
				...diagnostics,
				daemonMessage: "Serving http://localhost:31001/api/v1/sessions?access_token=local-secret",
			},
			"github",
		);

		expect(draft).toContain("[redacted-local-path]");
		expect(draft).toContain("[redacted-local-url]");
		expect(draft).toContain("[redacted-secret]");
		expect(draft).not.toContain("/Users/alice");
		expect(draft).not.toContain("C:\\Users\\alice");
		expect(draft).not.toContain("localhost:31001");
		expect(draft).not.toContain("sk-proj-secret");
		expect(draft).not.toContain("hunter2");
	});

	it("redacts JSON secrets, authorization headers, and GitHub token forms", () => {
		const githubToken = `ghp_${"abcdefghijklmnopqrstuvwxyz"}${"1234567890AB"}`;
		const githubOauthToken = `gho_${"abcdefghijklmnopqrstuvwxyz"}${"1234567890AB"}`;
		const fineGrainedGithubToken = `github_pat_11${"AAAAAAAAAAAAAAAAAAAA"}_${"B".repeat(74)}`;

		const draft = formatReportProblemDraft(
			{
				summary: `GitHub token leaked: ${githubToken}`,
				details: [
					'{"token": "json-token-secret", "api_key": "json-api-key-secret"}',
					`Authorization: token ${githubOauthToken}`,
					"authorization: Bearer header-token-secret",
					fineGrainedGithubToken,
				].join("\n"),
			},
			diagnostics,
			"github",
		);

		expect(draft).toContain("[redacted-secret]");
		expect(draft).not.toContain("json-token-secret");
		expect(draft).not.toContain("json-api-key-secret");
		expect(draft).not.toContain(githubToken);
		expect(draft).not.toContain(githubOauthToken);
		expect(draft).not.toContain("header-token-secret");
		expect(draft).not.toContain(fineGrainedGithubToken);
	});

	it("produces a useful draft when user input is partial", () => {
		const draft = formatReportProblemDraft({ summary: "", details: "" }, diagnostics, "github");

		expect(draft).toContain("Tallpine feedback");
		expect(draft).toContain("Not provided");
		expect(draft).toContain("Safe diagnostics");
		expect(draft).toContain("Tallpine version: 1.2.3-test");
	});

	it("omits report type and footer copy from generated drafts", () => {
		const outputs: ReportProblemOutput[] = ["github"];

		for (const output of outputs) {
			const draft = formatReportProblemDraft(completeInput, diagnostics, output);

			expect(draft).toContain("Summary");
			expect(draft).toContain("Details");
			expect(draft).not.toContain("## Type");
			expect(draft).not.toContain("Bug report");
			expect(draft).not.toContain("Generated locally by Tallpine");
			expect(draft).not.toContain("No logs, repo contents");
		}
	});

	it("builds the GitHub issue handoff URL without email or chat destinations", () => {
		const github = new URL(reportProblemDestinationUrl(completeInput, diagnostics, "github")!);
		expect(`${github.origin}${github.pathname}`).toBe("https://github.com/justvibedev/tallpine/issues/new");
		expect(github.searchParams.get("title")).toBe("Terminal keeps reconnecting after daemon restart");
		expect(github.searchParams.get("body")).toContain("[redacted-local-path]");
		expect(github.searchParams.get("body")).toContain("[redacted-local-url]");
	});

	it("derives route surface from the hash-history route", async () => {
		window.tallpine!.app.getVersion = vi.fn().mockResolvedValue("1.2.3-test");
		window.tallpine!.daemon.getStatus = vi.fn().mockResolvedValue({ state: "ready" });
		window.location.hash = "#/projects/demo/sessions/demo-1";

		const nextDiagnostics = await collectReportProblemDiagnostics(new Date("2026-07-02T00:00:00.000Z"));

		expect(nextDiagnostics.routeSurface).toBe("session_detail");
	});
});
