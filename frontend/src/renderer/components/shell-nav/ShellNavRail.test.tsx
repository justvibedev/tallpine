import { TooltipProvider } from "@/components/ui/tooltip";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import type { WorkspaceSession, WorkspaceSummary } from "../../types/workspace";
import { ShellNavRail } from "./ShellNavRail";

const { navigateMock, location } = vi.hoisted(() => ({ navigateMock: vi.fn(), location: { pathname: "/sessions" } }));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigateMock,
	useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) => select({ location }),
}));

const { unread } = vi.hoisted(() => ({ unread: { count: 0 } }));
vi.mock("../../hooks/useUnreadNotificationCount", () => ({ useUnreadNotificationCount: () => unread.count }));

const session = (id: string, over: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
	id,
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title: id,
	provider: "claude-code",
	kind: "worker",
	branch: `session/${id}`,
	status: "working",
	updatedAt: "2026-06-30T00:00:00Z",
	prs: [],
	...over,
});

const workspaceWith = (sessions: WorkspaceSession[]): WorkspaceSummary => ({
	id: "proj-1",
	name: "Project One",
	path: "/repo/one",
	orchestratorAgent: "claude-code",
	sessions,
});

const needsYou = (id: string) => session(id, { status: "needs_input", activity: "waiting_input" } as unknown as Partial<WorkspaceSession>);

function renderRail(workspaces: WorkspaceSummary[] = []) {
	return render(
		<TooltipProvider>
			<ShellNavRail topbarOffset="toolbar" underTopbar workspaces={workspaces} />
		</TooltipProvider>,
	);
}

beforeEach(() => {
	navigateMock.mockReset();
	location.pathname = "/sessions";
	unread.count = 0;
	window.localStorage.clear();
	useUiStore.setState({ isRailExpanded: false, isSidebarOpen: true, isNarrowShell: false, isNarrowPanelOpen: false, settingsModal: null, lastSessionsPath: "/sessions" });
});

describe("ShellNavRail", () => {
	it("lists the main pages with accessible names, Connections and Settings last", () => {
		renderRail();
		const rail = screen.getByRole("navigation", { name: "Main navigation" });
		const names = within(rail).getAllByRole("button").map((button) => button.getAttribute("aria-label"));
		expect(names).toEqual(["Go to home", "Sessions", "Automations", "Artifacts", "Backlog", "Inbox", "Terminals", "Expand navigation", "Connect a phone", "Settings"]);
	});

	it("puts the app logo (home) at the top of the rail, above every page icon", () => {
		renderRail();
		const rail = screen.getByRole("navigation", { name: "Main navigation" });
		const first = within(rail).getAllByRole("button")[0];
		expect(first).toHaveAttribute("data-testid", "rail-home");
		expect(first.querySelector("svg")).not.toBeNull();
	});

	it("has exactly one panel toggle: the Sessions icon", () => {
		renderRail();
		const rail = screen.getByRole("navigation", { name: "Main navigation" });
		expect(within(rail).getAllByRole("button").filter((button) => button.hasAttribute("aria-pressed"))).toHaveLength(1);
	});

	it.each([
		["rail-home", { to: "/" }],
		["rail-automations", { to: "/automations" }],
		["rail-artifacts", { to: "/artifacts" }],
		["rail-backlog", { to: "/backlog" }],
		["rail-inbox", { to: "/inbox" }],
		["rail-terminals", { to: "/terminals" }],
	])("%s navigates to its page", async (testId, target) => {
		renderRail();
		await userEvent.setup().click(screen.getByTestId(testId));
		expect(navigateMock).toHaveBeenCalledWith(target);
	});

	it("opens global settings and the Connections page in place", async () => {
		const user = userEvent.setup();
		renderRail();
		await user.click(screen.getByTestId("rail-settings"));
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global" });
		await user.click(screen.getByTestId("rail-mobile"));
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "connections" });
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("marks the current page and leaves the others unmarked", () => {
		location.pathname = "/artifacts";
		renderRail();
		expect(screen.getByTestId("rail-artifacts")).toHaveAttribute("aria-current", "page");
		expect(screen.getByTestId("rail-automations")).not.toHaveAttribute("aria-current");
		expect(screen.getByTestId("rail-sessions")).not.toHaveAttribute("data-active", "true");
	});

	it("toggles the Sessions panel from the Sessions icon without leaving the page", async () => {
		const user = userEvent.setup();
		renderRail();
		const sessions = screen.getByTestId("rail-sessions");
		expect(sessions).toHaveAttribute("aria-pressed", "true");
		await user.click(sessions);
		expect(useUiStore.getState().isSidebarOpen).toBe(false);
		expect(sessions).toHaveAttribute("aria-pressed", "false");
		expect(window.localStorage.getItem("ao.sidebar.open")).toBe("false");
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("opens the Sessions view from the Sessions icon when another page (Backlog) is showing", async () => {
		const user = userEvent.setup();
		location.pathname = "/backlog";
		renderRail();
		await user.click(screen.getByTestId("rail-sessions"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/sessions" });
	});

	it("returns to the last project session after a round trip through Backlog", async () => {
		const user = userEvent.setup();
		location.pathname = "/projects/p1/sessions/s1";
		const view = renderRail();
		location.pathname = "/backlog";
		view.rerender(
			<TooltipProvider>
				<ShellNavRail topbarOffset="toolbar" underTopbar workspaces={[]} />
			</TooltipProvider>,
		);
		await user.click(screen.getByTestId("rail-sessions"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/p1/sessions/s1" });
	});

	it("reopens a hidden Sessions panel when navigating back from Backlog", async () => {
		const user = userEvent.setup();
		location.pathname = "/backlog";
		useUiStore.setState({ isSidebarOpen: false });
		renderRail();
		await user.click(screen.getByTestId("rail-sessions"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/sessions" });
		expect(useUiStore.getState().isSidebarOpen).toBe(true);
	});

	it("keeps an already-open Sessions panel open when navigating back from Backlog", async () => {
		const user = userEvent.setup();
		location.pathname = "/backlog";
		renderRail();
		await user.click(screen.getByTestId("rail-sessions"));
		expect(useUiStore.getState().isSidebarOpen).toBe(true);
	});

	it("shows icons only until the rail is expanded, then a text label per item", async () => {
		const user = userEvent.setup();
		renderRail();
		const toggle = screen.getByTestId("rail-toggle");
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByTestId("rail-inbox-label")).toBeNull();
		await user.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		expect(toggle).toHaveAccessibleName("Collapse navigation");
		expect(screen.getByTestId("rail-inbox-label")).toHaveTextContent("Inbox");
		expect(screen.getByTestId("rail-sessions-label")).toHaveTextContent("Sessions");
		expect(screen.getByTestId("rail-backlog-label")).toHaveTextContent("Backlog");
		expect(window.localStorage.getItem("ao.rail.expanded")).toBe("true");
	});

	it("toggles the labelled rail from the keyboard and keeps accessible names", async () => {
		const user = userEvent.setup();
		unread.count = 2;
		renderRail();
		screen.getByTestId("rail-toggle").focus();
		await user.keyboard("{Enter}");
		expect(useUiStore.getState().isRailExpanded).toBe(true);
		await user.keyboard(" ");
		expect(useUiStore.getState().isRailExpanded).toBe(false);
		await user.keyboard("{Enter}");
		expect(screen.getByTestId("rail-inbox")).toHaveAccessibleName("Inbox");
		expect(screen.getByTestId("rail-inbox-badge")).toHaveTextContent("2");
	});

	it("badges the Inbox with the unread notification count and hides it at zero", () => {
		unread.count = 3;
		const view = renderRail();
		expect(screen.getByTestId("rail-inbox-badge")).toHaveTextContent("3");
		unread.count = 0;
		view.unmount();
		renderRail();
		expect(screen.queryByTestId("rail-inbox-badge")).toBeNull();
	});

	it("badges sessions that need attention and the artifact count", () => {
		const withArtifacts = session("done-1", {
			outputType: "artifact",
			artifactFiles: [
				{ kind: "html", name: "a.html", path: "a.html", size: 1, updatedAt: "2026-10-11T10:00:00Z" },
				{ kind: "markdown", name: "b.md", path: "b.md", size: 1, updatedAt: "2026-10-10T10:00:00Z" },
			],
		} as Partial<WorkspaceSession>);
		renderRail([workspaceWith([needsYou("a"), needsYou("b"), withArtifacts])]);
		expect(screen.getByTestId("rail-sessions-badge")).toHaveTextContent("2");
		expect(screen.getByTestId("rail-sessions")).toHaveAccessibleName("Sessions (2 need attention)");
		expect(screen.getByTestId("rail-artifacts-badge")).toHaveTextContent("2");
	});

	it("shows no badges when nothing needs attention and there are no artifacts", () => {
		renderRail([workspaceWith([session("quiet")])]);
		expect(screen.queryByTestId("rail-sessions-badge")).toBeNull();
		expect(screen.queryByTestId("rail-artifacts-badge")).toBeNull();
	});

	it("does not count terminated sessions", () => {
		renderRail([workspaceWith([{ ...needsYou("gone"), isTerminated: true }])]);
		expect(screen.queryByTestId("rail-sessions-badge")).toBeNull();
	});
});

describe("ShellNavRail leaving full-screen views", () => {
	it("re-clicking the active rail tab returns to the remembered chat", async () => {
		location.pathname = "/projects/p1/sessions/s1";
		const view = renderRail();
		view.unmount();
		location.pathname = "/backlog";
		renderRail();
		await userEvent.click(screen.getByTestId("rail-backlog"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/p1/sessions/s1" });
	});

	it("Escape on a rail page returns to the chat, but not from a text field", async () => {
		useUiStore.setState({ lastSessionsPath: "/projects/p1/sessions/s1" });
		location.pathname = "/inbox";
		renderRail();
		const input = document.body.appendChild(document.createElement("input"));
		input.focus();
		await userEvent.keyboard("{Escape}");
		expect(navigateMock).not.toHaveBeenCalled();
		input.remove();
		await userEvent.keyboard("{Escape}");
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/p1/sessions/s1" });
	});

	it("a rail tab closes open Settings and the Sessions tab does not toggle the panel", async () => {
		location.pathname = "/projects/p1/sessions/s1";
		useUiStore.setState({ settingsModal: { scope: "global" } as never, isSidebarOpen: true });
		renderRail();
		await userEvent.click(screen.getByTestId("rail-sessions"));
		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(useUiStore.getState().isSidebarOpen).toBe(true);
	});
});
