import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import type { StorageEntry } from "../../hooks/useStorage";
import { StorageSection } from "./StorageSection";
import { ageParts, groupEntries } from "./storageFormat";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post },
	apiErrorMessage: (error: unknown) => (error instanceof Error ? error.message : "request failed"),
}));

const GB = 1024 ** 3;
const entry = (over: Partial<StorageEntry>): StorageEntry => ({
	id: "x", category: "worktrees", label: "worktree", path: "/d/worktrees/p/s", bytes: 1, files: 1, ...over,
});
const report = {
	refreshing: false,
	totalBytes: 5 * GB,
	computedAt: "2026-10-30T10:00:00Z",
	entries: [
		entry({ id: "w1", path: "/d/worktrees/p/old", bytes: 2 * GB, projectId: "p", sessionId: "old", state: "ended", newestAt: "2026-10-20T10:00:00Z" }),
		entry({ id: "w2", path: "/d/worktrees/p/live", bytes: GB, projectId: "p", sessionId: "live", state: "live", protected: "session is live" }),
		entry({ id: "r1", category: "releases", label: "rollback point", path: "/r/releases/a", bytes: 2 * GB, protected: "newest rollback point is kept" }),
	],
	goCache: { path: "/c/go-build", bytes: 40 * GB, capBytes: 30 * GB },
	processes: [{ pid: 4242, command: "/x/vite", sessionId: "old", projectId: "p", state: "ended" }],
};

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><StorageSection /></QueryClientProvider>);
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	get.mockReset();
	post.mockReset();
	get.mockResolvedValue({ data: report });
});

describe("StorageSection polish", () => {
	it("shows a measuring state, not the page title, while the first report is empty", async () => {
		get.mockResolvedValue({ data: { ...report, refreshing: true, totalBytes: 0, entries: [], processes: [] } });
		renderSection();
		expect(await screen.findAllByText("Measuring…", { exact: false })).not.toHaveLength(0);
		await new Promise((r) => setTimeout(r, 50));
		expect(screen.queryByText(/used by Tallpine/)).toBeNull();
		expect(screen.queryByText("Storage", { selector: "div,span,p" })).toBeNull();
	});
	it("labels the database plainly without the file name", async () => {
		get.mockResolvedValue({ data: { ...report, entries: [entry({ id: "db", category: "database", label: "App database", path: "/d/tallpine.db", protected: "the app's database" })] } });
		renderSection();
		expect((await screen.findAllByText("App database")).length).toBeGreaterThan(0);
		expect(screen.queryByText(/tallpine\.db/)).toBeNull();
	});
});

describe("storageFormat", () => {
	it("groups in page order, largest first", () => {
		const groups = groupEntries([entry({ id: "a", category: "logs", bytes: 1 }), ...report.entries]);
		expect(groups.map((g) => g.category)).toEqual(["worktrees", "releases", "logs"]);
		expect(groups[0].entries.map((e) => e.id)).toEqual(["w1", "w2"]);
		expect(groups[0].bytes).toBe(3 * GB);
	});
	it("ages in the largest whole unit", () => {
		const now = Date.parse("2026-10-30T10:00:00Z");
		expect(ageParts("2026-10-30T09:30:00Z", now)).toEqual({ unit: "minutes", count: 30 });
		expect(ageParts("2026-10-30T05:00:00Z", now)).toEqual({ unit: "hours", count: 5 });
		expect(ageParts("2026-10-20T10:00:00Z", now)).toEqual({ unit: "days", count: 10 });
		expect(ageParts(undefined, now)).toBeNull();
	});
});

describe("StorageSection", () => {
	// ages are relative to now; pin the clock (Date only, so waitFor still works)
	beforeEach(() => {
		vi.useFakeTimers({ toFake: ["Date"], now: Date.parse("2026-10-30T10:00:00Z") });
	});
	afterEach(() => {
		vi.useRealTimers();
	});
	it("lists sizes, owner, state, the go cache cap and reaper processes", async () => {
		renderSection();
		expect(await screen.findByText("5.0 GB used by Tallpine and its agents")).toBeInTheDocument();
		expect(screen.getByText(/p \/ old · ended · 10 d old/)).toBeInTheDocument();
		expect(screen.getByText("Shared by agents; capped at 30 GB.")).toBeInTheDocument();
		expect(screen.getByText("vite")).toBeInTheDocument();
		expect(screen.getByText(/PID 4242/)).toBeInTheDocument();
	});

	it("offers remove only where the daemon allows it", async () => {
		renderSection();
		await screen.findByText("5.0 GB used by Tallpine and its agents");
		expect(screen.getAllByRole("button", { name: "Remove" })).toHaveLength(1);
		expect(screen.getByText("session is live")).toBeInTheDocument();
		expect(screen.getByText("newest rollback point is kept")).toBeInTheDocument();
	});

	it("removes an entry only after a second confirming click", async () => {
		const user = userEvent.setup();
		post.mockResolvedValue({ data: { removed: "w1" } });
		renderSection();
		await user.click(await screen.findByRole("button", { name: "Remove" }));
		expect(post).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Remove for good" }));
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/storage/delete", { body: { id: "w1" } }));
	});

	it("shows the daemon's refusal next to the row", async () => {
		const user = userEvent.setup();
		post.mockResolvedValue({ error: new Error("cannot remove: session is live") });
		renderSection();
		await user.click(await screen.findByRole("button", { name: "Remove" }));
		await user.click(screen.getByRole("button", { name: "Remove for good" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("cannot remove: session is live");
	});

	it("previews a clean-up as a dry run and only runs it after confirmation", async () => {
		const user = userEvent.setup();
		post.mockImplementation(async (_path: string, { body }: { body: { dryRun: boolean } }) => ({
			data: body.dryRun
				? { freedBytes: 0, categories: [{ category: "worktrees", wouldFreeBytes: 3 * GB }] }
				: { freedBytes: 3 * GB, categories: [{ category: "worktrees", freedBytes: 3 * GB, failed: 0, items: [] }] },
		}));
		renderSection();
		await user.click(await screen.findByRole("button", { name: "Clean up everything safe" }));
		expect(post).toHaveBeenCalledTimes(1);
		expect(post).toHaveBeenLastCalledWith("/api/v1/storage/cleanup", { body: { dryRun: true } });
		expect(await screen.findByText(/will free about 3.0 GB/)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Clean up" }));
		await waitFor(() => expect(post).toHaveBeenLastCalledWith("/api/v1/storage/cleanup", { body: { dryRun: false } }));
		expect(await screen.findByText("Freed 3.0 GB.")).toBeInTheDocument();
	});

	it("says what a clean-up could not remove", async () => {
		const user = userEvent.setup();
		post.mockImplementation(async (_path: string, { body }: { body: { dryRun: boolean } }) => ({
			data: body.dryRun
				? { freedBytes: 0, categories: [{ category: "worktrees", wouldFreeBytes: 3 * GB, items: [] }] }
				: { freedBytes: 0, categories: [{ category: "releases", failed: 2, items: [{ status: "failed", reason: "permission denied" }] }] },
		}));
		renderSection();
		await user.click(await screen.findByRole("button", { name: "Clean up everything safe" }));
		await user.click(await screen.findByRole("button", { name: "Clean up" }));
		expect(await screen.findByText(/Freed 0 B\. 2 items could not be removed: permission denied/)).toBeInTheDocument();
	});

	it("lists the go build cache once", async () => {
		get.mockResolvedValue({ data: { ...report, entries: [...report.entries, entry({ id: "g", category: "go_build_cache", label: "Go build cache (shared by agents)", path: "/c/go-build", bytes: 40 * GB, protected: "trimmed" })] } });
		renderSection();
		await screen.findByText("Shared by agents; capped at 30 GB.");
		expect(screen.getAllByText("40 GB")).toHaveLength(1);
		expect(screen.queryByText("trimmed")).not.toBeInTheDocument();
	});

	it("marks sizes that are still being measured as a lower bound", async () => {
		get.mockResolvedValue({ data: { ...report, entries: [entry({ id: "p", bytes: 10 * GB, partial: true, protected: "x" })] } });
		renderSection();
		const row = (await screen.findByText(/≥ 10 GB/)).closest("div");
		expect(row && within(row).getByText(/≥ 10 GB/)).toBeInTheDocument();
	});
});
