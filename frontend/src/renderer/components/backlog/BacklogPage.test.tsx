import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogItem as item } from "../../test/backlog-fixtures";
import { renderBacklog, resetBacklogApi, workspaceData } from "../../test/backlog-harness";
import { BacklogPage } from "./BacklogPage";

const { navigate, search } = vi.hoisted(() => ({ navigate: vi.fn(), search: { current: {} as { project?: string; item?: string } } }));

vi.mock("@tanstack/react-router", async (importActual) => ({
	...(await importActual<typeof import("@tanstack/react-router")>()),
	getRouteApi: () => ({ useSearch: () => search.current }),
	useNavigate: () => navigate,
}));
vi.mock("../../lib/backlog-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-api")>()), ...(await import("../../test/backlog-harness")).backlogApi }));
vi.mock("../../hooks/useWorkspaceQuery", async (importActual) => {
	const harness = await import("../../test/backlog-harness");
	return { ...(await importActual<typeof import("../../hooks/useWorkspaceQuery")>()), useWorkspaceQuery: () => ({ data: harness.workspaceData.current }) };
});

beforeEach(() => {
	navigate.mockReset();
	search.current = {};
	workspaceData.current = [
		{ id: "proj-1", name: "Project One", path: "/p1", sessions: [] },
		{ id: "proj-2", name: "Project Two", path: "/p2", sessions: [] },
	];
	resetBacklogApi([item({ id: "i1", ref: "B1", title: "First", projectId: "proj-1" })]);
});

describe("BacklogPage", () => {
	it("keeps the project in the URL when the scope changes", async () => {
		search.current = { project: "proj-1" };
		renderBacklog(<BacklogPage />);
		await screen.findByText("First");
		await userEvent.click(screen.getByRole("combobox", { name: "Project" }));
		await userEvent.click(await screen.findByRole("option", { name: "Project Two" }));
		expect(navigate).toHaveBeenCalledWith({ to: "/backlog", search: { project: "proj-2" }, replace: true });
	});

	it("keeps the selected item in the URL and the project with it", async () => {
		search.current = { project: "proj-1" };
		renderBacklog(<BacklogPage />);
		await userEvent.click(await screen.findByRole("button", { name: /^B1, / }));
		const call = navigate.mock.calls.at(-1)?.[0] as { to: string; search: (previous: { project?: string }) => unknown; replace: boolean };
		expect(call.to).toBe("/backlog");
		expect(call.search({ project: "proj-1" })).toEqual({ project: "proj-1", item: "i1" });
	});

	it("renders the selected item from the URL", async () => {
		search.current = { project: "proj-1", item: "i1" };
		renderBacklog(<BacklogPage />);
		expect(await screen.findByRole("article", { name: /B1 First/ })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Back to list" }));
		await waitFor(() => expect(navigate).toHaveBeenCalled());
	});
});
