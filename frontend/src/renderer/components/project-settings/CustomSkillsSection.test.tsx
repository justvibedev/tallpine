import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";
import { CustomSkillsSection } from "./CustomSkillsSection";

const { getMock, postMock, putMock, deleteMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn(), putMock: vi.fn(), deleteMock: vi.fn() }));
vi.mock("../../lib/api-client", () => ({ apiClient: { GET: getMock, POST: postMock, PUT: putMock, DELETE: deleteMock }, apiErrorMessage: () => "request failed" }));

const skill: components["schemas"]["ProjectSkill"] = { name: "sql", description: "SQL checks", scope: "custom", convention: "custom", path: "/data/projects/p/skills/sql", harnesses: [], roles: [], access: [], deselected: false, removable: false };

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}><CustomSkillsSection projectId="p" skills={[skill]} /></QueryClientProvider>);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { name: "sql", description: "SQL checks", path: skill.path, content: "OLD BODY", files: [], skipped: [] } });
	postMock.mockReset().mockResolvedValue({ data: { name: "new-one", description: "", path: "/x", content: "", files: [], skipped: ["custom skill \"imp\" file \"run.sh\": executable"] } });
	putMock.mockReset().mockResolvedValue({ data: { name: "sql", description: "", path: skill.path, content: "NEW", files: [], skipped: [] } });
	deleteMock.mockReset().mockResolvedValue({});
});

it("creates a skill from a name and SKILL.md", async () => {
	renderSection();
	await userEvent.click(screen.getByRole("button", { name: "New skill" }));
	await userEvent.type(screen.getByLabelText("Skill name"), "new-one");
	await userEvent.click(screen.getByRole("button", { name: "Save" }));
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/custom", expect.objectContaining({ body: expect.objectContaining({ name: "new-one", content: expect.stringContaining("name: new-one\ndescription:") }) })));
});

it("imports a folder and names what it left out", async () => {
	renderSection();
	await userEvent.click(screen.getByRole("button", { name: "Import" }));
	await userEvent.type(screen.getByLabelText("Skill name"), "imp");
	await userEvent.type(screen.getByLabelText("Skill folder (absolute path)"), "/home/u/skills/imp");
	await userEvent.click(screen.getByRole("button", { name: "Save" }));
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/custom/import", expect.objectContaining({ body: { name: "imp", source: "/home/u/skills/imp" } })));
	expect(await screen.findByText(/Left out: .*run\.sh/)).toBeInTheDocument();
});

it("edits the stored SKILL.md and deletes only after confirming", async () => {
	renderSection();
	const row = screen.getByTestId("custom-skill-sql");
	await userEvent.click(within(row).getByRole("button", { name: "Edit" }));
	const area = await screen.findByLabelText("SKILL.md");
	expect(area).toHaveValue("OLD BODY");
	await userEvent.clear(area);
	await userEvent.type(area, "NEW");
	await userEvent.click(screen.getByRole("button", { name: "Save" }));
	await waitFor(() => expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/custom/{name}", expect.objectContaining({ params: { path: { id: "p", name: "sql" } }, body: { content: "NEW" } })));

	await userEvent.click(within(row).getByRole("button", { name: "Delete" }));
	expect(deleteMock).not.toHaveBeenCalled();
	await userEvent.click(within(row).getByRole("button", { name: "Confirm delete" }));
	await waitFor(() => expect(deleteMock).toHaveBeenCalledWith("/api/v1/projects/{id}/skills/custom/{name}", { params: { path: { id: "p", name: "sql" } } }));
});
