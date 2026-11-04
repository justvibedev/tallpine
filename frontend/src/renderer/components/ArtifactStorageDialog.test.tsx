import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { formatBytes } from "@tallpine/shared/format/bytes";
import { ArtifactStorageDialog } from "./ArtifactStorageDialog";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: get, POST: post },
	apiErrorMessage: (error: unknown) => (error instanceof Error ? error.message : "request failed"),
}));

const report = {
	olderThanDays: 30,
	warnBytes: 1024 ** 3,
	totalBytes: 3 * 1024 ** 3 + 100,
	versionsBytes: 1024 ** 3,
	eligibleBytes: 2 * 1024 ** 3,
	sessions: [
		{ sessionId: "old", projectId: "p1", displayName: "Old run", terminated: true, updatedAt: "2026-01-01T00:00:00Z", sizeBytes: 2 * 1024 ** 3, versionsBytes: 1024 ** 3, files: 9, overWarn: true, eligible: true },
		{ sessionId: "kept", projectId: "p2", displayName: "Design doc run", terminated: true, updatedAt: "2026-01-01T00:00:00Z", sizeBytes: 500, versionsBytes: 0, files: 1, overWarn: false, eligible: false, protected: true },
		{ sessionId: "live", projectId: "p1", displayName: "Live run", terminated: false, updatedAt: "2026-10-01T00:00:00Z", sizeBytes: 100, versionsBytes: 0, files: 1, overWarn: false, eligible: false },
	],
};

function renderRow(ui: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	get.mockReset();
	post.mockReset();
	get.mockResolvedValue({ data: report });
	post.mockResolvedValue({ data: { freedBytes: 2 * 1024 ** 3, results: [{ sessionId: "old", status: "archived", freedBytes: 2 * 1024 ** 3 }] } });
});

describe("formatArtifactBytes", () => {
	it("scales units", () => {
		expect(formatBytes(0)).toBe("0 B");
		expect(formatBytes(1536)).toBe("1.5 KB");
		expect(formatBytes(54 * 1024 ** 3)).toBe("54 GB");
	});
});

describe("ArtifactStorageDialog", () => {
	it("flags large sessions and only lets finished ones be selected", async () => {
		renderRow(<ArtifactStorageDialog onClose={() => undefined} />);
		expect(await screen.findByText("Large")).toBeInTheDocument();
		expect(screen.getByRole("checkbox", { name: "Old run" })).toBeEnabled();
		expect(screen.getByRole("checkbox", { name: "Live run" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Archive…" })).toBeDisabled();
	});

	it("archives only after an explicit confirmation", async () => {
		const user = userEvent.setup();
		renderRow(<ArtifactStorageDialog onClose={() => undefined} />);
		await user.click(await screen.findByRole("checkbox", { name: "Old run" }));
		await user.click(screen.getByRole("button", { name: "Archive…" }));
		expect(post).not.toHaveBeenCalled();
		expect(screen.getByText(/Archive the artifacts of 1 selected sessions/)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Archive" }));
		await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
		expect(post.mock.calls[0][1].body).toEqual({ action: "archive", olderThanDays: 30, sessionIds: ["old"], confirm: true });
		expect(await screen.findByText("Freed 2.0 GB.")).toBeInTheDocument();
	});

	it("cancelling the confirmation sends nothing", async () => {
		const user = userEvent.setup();
		renderRow(<ArtifactStorageDialog onClose={() => undefined} />);
		await user.click(await screen.findByRole("checkbox", { name: "Old run" }));
		await user.click(screen.getByRole("button", { name: "Delete…" }));
		await user.click(screen.getByRole("button", { name: "Cancel" }));
		expect(post).not.toHaveBeenCalled();
		expect(screen.getByRole("button", { name: "Delete…" })).toBeInTheDocument();
	});

	it("marks sessions an open backlog item references and does not let them be selected", async () => {
		renderRow(<ArtifactStorageDialog onClose={() => undefined} />);
		expect(await screen.findByText(/Kept: an open backlog item references it/)).toBeInTheDocument();
		expect(screen.getByRole("checkbox", { name: "Design doc run" })).toBeDisabled();
	});

	it("preselects finished sessions and lists names and sizes in the confirmation", async () => {
		const user = userEvent.setup();
		renderRow(<ArtifactStorageDialog onClose={() => undefined} preselectFinished />);
		await waitFor(() => expect(screen.getByRole("checkbox", { name: "Old run" })).toBeChecked());
		expect(screen.getByRole("checkbox", { name: "Design doc run" })).not.toBeChecked();
		await user.click(screen.getByRole("button", { name: "Archive…" }));
		expect(screen.getByText("Old run")).toBeInTheDocument();
		expect(screen.getByText("2.0 GB")).toBeInTheDocument();
		expect(post).not.toHaveBeenCalled();
	});

	it("shows only the given project's sessions", async () => {
		renderRow(<ArtifactStorageDialog onClose={() => undefined} projectId="p2" />);
		expect(await screen.findByText("Design doc run")).toBeInTheDocument();
		expect(screen.queryByText("Old run")).not.toBeInTheDocument();
	});
});
