import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { AgentPolicySection } from "./AgentPolicySection";
import { RetentionSection } from "./RetentionSection";

const { get, patch, post } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), post: vi.fn() }));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, PATCH: patch, POST: post },
	apiErrorMessage: (error: unknown) => (error instanceof Error ? error.message : "request failed"),
}));

const field = (name: string, kind: string, value: unknown, extra: Record<string, unknown> = {}) => ({
	name,
	kind,
	labelKey: `settings.agentPolicy.sessionRetention.${name}.label`,
	descriptionKey: `settings.agentPolicy.sessionRetention.${name}.description`,
	default: value,
	value,
	source: "default",
	locked: false,
	projectOverride: "any",
	...(name === "enabled" ? {} : { visibleWhen: { field: "enabled", equals: "true" } }),
	...extra,
});

const policy = (enabled: boolean, overrides: Record<string, Record<string, unknown>> = {}) => ({
	schemaVersion: 2,
	rules: [
		{
			name: "sessionRetention",
			titleKey: "settings.agentPolicy.title",
			fields: [
				field("enabled", "bool", enabled, overrides.enabled),
				field("days", "int", 7, overrides.days),
				field("dropProviderEvents", "bool", true),
				field("dropWorktrees", "bool", true),
				field("dropTranscripts", "bool", false),
				field("includeOrchestrators", "bool", false),
			],
		},
	],
	managedPath: "",
	warnings: [],
});

const category = (dryRun: boolean) => ({
	category: "session_retention",
	freedBytes: dryRun ? 0 : 3 * 1024 * 1024,
	wouldFreeBytes: dryRun ? 3 * 1024 * 1024 : 0,
	pendingBytes: 0,
	freed: dryRun ? 0 : 2,
	wouldFree: dryRun ? 2 : 0,
	pending: 0,
	kept: 0,
	failed: 0,
	items: dryRun
		? [
				{ sessionId: "worker-12", what: "raw provider events", bytes: 2 * 1024 * 1024, status: "would_free" },
				{ sessionId: "worker-12", what: "worktree", path: "/w/worker-12", bytes: 1024 * 1024, status: "would_free" },
			]
		: [],
});

function respond() {
	post.mockImplementation(async (_path: string, { body }: { body: { dryRun: boolean } }) => ({
		data: { id: 1, startedAt: "", finishedAt: "", trigger: "manual", pass: "full", dryRun: body.dryRun, freedBytes: 0, categories: [category(body.dryRun)] },
	}));
}

function renderUI(ui: React.ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	get.mockReset();
	patch.mockReset();
	post.mockReset();
});

describe("RetentionSection", () => {
	it("shows every setting once enabled, with the 7-day default and orchestrators off", async () => {
		get.mockResolvedValue({ data: policy(true) });
		renderUI(<RetentionSection />);
		expect(await screen.findByRole("switch", { name: /include orchestrator sessions/i })).not.toBeChecked();
		expect(screen.getByRole("button", { name: /purge after/i })).toHaveTextContent("7");
		for (const name of [/drop worktrees/i, /drop transcripts/i]) {
			expect(screen.getByRole("switch", { name })).toBeInTheDocument();
		}
		// Raw events have one control: the provider-event retention days on Storage.
		expect(screen.queryByRole("switch", { name: /drop raw provider events/i })).toBeNull();
	});

	it("shows MDM-locked settings read-only with who locks them", async () => {
		get.mockResolvedValue({ data: policy(true, { enabled: { source: "managed", locked: true }, days: { source: "managed", locked: true } }) });
		renderUI(<RetentionSection />);
		const toggle = await screen.findByRole("switch", { name: /purge ended sessions/i });
		expect(toggle).toBeDisabled();
		expect(screen.getAllByTestId("agent-policy-lock")).toHaveLength(2);
		expect(screen.getAllByTestId("agent-policy-lock")[0]).toHaveTextContent("Managed by your organization");
		expect(screen.queryByRole("button", { name: /purge after/i })).toBeNull();
	});

	it("previews items and bytes with a dry run limited to retention", async () => {
		get.mockResolvedValue({ data: policy(true) });
		respond();
		renderUI(<RetentionSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Preview" }));
		expect(await screen.findByText(/would free 3\.0 MB \(2 items\)/i)).toBeInTheDocument();
		const list = screen.getByRole("list", { name: /items that would be removed/i });
		expect(within(list).getAllByRole("listitem")).toHaveLength(2);
		expect(within(list).getByText(/worktree/)).toBeInTheDocument();
		expect(post).toHaveBeenCalledTimes(1);
		expect(post.mock.calls[0][1].body).toEqual({ dryRun: true, only: ["session_retention"] });
	});

	it("runs only after the confirmation, and never while cancelled", async () => {
		get.mockResolvedValue({ data: policy(true) });
		respond();
		renderUI(<RetentionSection />);
		const runNow = await screen.findByRole("button", { name: "Run cleanup now" });
		expect(runNow).toBeDisabled();
		await userEvent.click(screen.getByRole("button", { name: "Preview" }));
		await waitFor(() => expect(runNow).toBeEnabled());

		await userEvent.click(runNow);
		await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
		expect(post.mock.calls.filter(([, { body }]) => !body.dryRun)).toHaveLength(0);

		await userEvent.click(runNow);
		await userEvent.click(await screen.findByRole("button", { name: "Remove now" }));
		await waitFor(() => expect(post.mock.calls.filter(([, { body }]) => !body.dryRun)).toHaveLength(1));
		expect(await screen.findByText(/freed 3\.0 MB/i)).toBeInTheDocument();
	});

	it("previews while off but cannot run until enabled", async () => {
		get.mockResolvedValue({ data: policy(false) });
		respond();
		renderUI(<RetentionSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Preview" }));
		expect(await screen.findByText(/retention is off/i)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Run cleanup now" })).toBeDisabled();
		expect(screen.queryByRole("button", { name: /purge after/i })).toBeNull();
	});

	it("is not repeated in the generic agent policy section", async () => {
		get.mockResolvedValue({ data: policy(true) });
		renderUI(<AgentPolicySection />);
		await screen.findByRole("heading", { name: "Agent policy" });
		expect(screen.queryByRole("switch", { name: /purge ended sessions|include orchestrator sessions/i })).toBeNull();
	});
});

describe("RetentionSection stale preview", () => {
	it("requires a new preview after any setting changes", async () => {
		get.mockResolvedValue({ data: policy(true) });
		patch.mockResolvedValue({ data: {} });
		respond();
		renderUI(<RetentionSection />);
		const runNow = await screen.findByRole("button", { name: "Run cleanup now" });
		await userEvent.click(screen.getByRole("button", { name: "Preview" }));
		await waitFor(() => expect(runNow).toBeEnabled());
		await userEvent.click(screen.getByRole("switch", { name: /drop transcripts/i }));
		await waitFor(() => expect(patch).toHaveBeenCalled());
		expect(runNow).toBeDisabled();
		expect(screen.queryByRole("list", { name: /items that would be removed/i })).toBeNull();
	});
});
