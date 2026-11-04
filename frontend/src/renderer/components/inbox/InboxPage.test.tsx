import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { NotificationDTO } from "../../lib/notifications";
import { InboxPage } from "./InboxPage";

const { search, navigateToSession, notifications } = vi.hoisted(() => ({
	search: { item: undefined as string | undefined },
	navigateToSession: vi.fn(),
	notifications: { items: [] as NotificationDTO[] },
}));

vi.mock("@tanstack/react-router", () => ({ getRouteApi: () => ({ useSearch: () => search }) }));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => navigateToSession }));
vi.mock("../../hooks/useNotificationsQuery", () => ({ useNotificationsQuery: () => ({ data: { pages: [{ notifications: notifications.items }] } }) }));

const notification = { id: "n1", type: "needs_input", status: "unread", title: "Needs you", body: "Pick a plan", projectId: "p1", sessionId: "s1", createdAt: "2026-10-29T10:00:00Z", target: { kind: "session", sessionId: "s1" } } as unknown as NotificationDTO;

function renderPage() {
	return render(<QueryClientProvider client={new QueryClient()}><InboxPage /></QueryClientProvider>);
}

beforeEach(() => {
	search.item = undefined;
	notifications.items = [notification];
	navigateToSession.mockReset();
});

describe("InboxPage", () => {
	it("asks for a selection when no item is picked", () => {
		renderPage();
		expect(screen.getByTestId("inbox-empty")).toHaveTextContent("Select a notification");
	});

	it("shows the picked notification and opens its session", async () => {
		search.item = "n1";
		renderPage();
		expect(screen.getByTestId("inbox-detail")).toHaveTextContent("Pick a plan");
		await userEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(navigateToSession).toHaveBeenCalledWith("p1", "s1");
	});

	it("opens the PR in the system browser from a PR notification", async () => {
		notifications.items = [{ ...notification, target: { kind: "pr", sessionId: "s1", prUrl: "https://example.test/o/r/pull/9", prNumber: 9 } } as unknown as NotificationDTO];
		search.item = "n1";
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		renderPage();
		await userEvent.click(screen.getByRole("button", { name: /Open PR #9/ }));
		expect(open).toHaveBeenCalledWith("https://example.test/o/r/pull/9", "_blank", "noopener,noreferrer");
		open.mockRestore();
	});

	it("falls back to the prompt when the item is gone (cleared)", () => {
		search.item = "missing";
		renderPage();
		expect(screen.getByTestId("inbox-empty")).toBeInTheDocument();
	});
});
