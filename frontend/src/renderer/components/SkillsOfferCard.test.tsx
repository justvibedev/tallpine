import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { useSkillsOfferOnProjectAdd } from "../hooks/useSkillsOfferOnProjectAdd";
import { useSkillsOfferStore } from "../stores/skills-offer-store";
import { useUiStore } from "../stores/ui-store";
import { ProjectSkillsSettings } from "./ProjectSkillsSettings";
import { SkillsOfferCard } from "./SkillsOfferCard";
import { SkillsOfferDialogHost } from "./SkillsOfferDialog";

const { getMock, postMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, POST: postMock, PUT: putMock }, apiErrorMessage: () => "request failed" }));

let skills: Record<string, unknown>;
const offer = { id: "claude-md", harness: "claude-code", items: [{ path: "CLAUDE.md", kind: "file", content: "@AGENTS.md\n" }] };
const second = { id: "claude-skills", harness: "claude-code", items: [{ path: ".claude/skills/qa", kind: "link", target: "../../.codex/skills/qa" }, { path: ".claude/skills/lint", kind: "link", target: "../../.codex/skills/lint" }] };
const skillsBody = (over: Record<string, unknown> = {}) => ({
	projectId: "p", workspace: "/repo", agentsMd: true, claudeMd: false, harnesses: ["claude-code"], instructions: [{ harness: "claude-code", status: "missing" }],
	skills: [], collisions: [], offersEnabled: true, pendingCommit: [], offers: [offer], ...over,
});
let projectConfig: Record<string, unknown>;

beforeEach(() => {
	skills = skillsBody();
	projectConfig = { autoReview: true, env: { A: "b" } };
	useSkillsOfferStore.setState({ shown: new Set(), active: new Set(), reviewing: null });
	useUiStore.setState({ globalToasts: [] });
	getMock.mockReset().mockImplementation(async (path: string) =>
		path === "/api/v1/projects/{id}/skills" ? { data: skills } : { data: { status: "ok", project: { id: "p", name: "Example", config: projectConfig } } });
	putMock.mockReset().mockResolvedValue({ data: { status: "ok" } });
	postMock.mockReset().mockResolvedValue({ data: { created: [{ path: "CLAUDE.md", mode: "file" }] } });
});

const client = () => new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
function wrap(ui: React.ReactNode, c = client()) {
	return <QueryClientProvider client={c}>{ui}</QueryClientProvider>;
}
const card = (sessionId = "s1") => wrap(<><SkillsOfferCard projectId="p" sessionId={sessionId} /><SkillsOfferDialogHost /></>);

// The query resolves a tick after the request starts; wait it out so an absent card is really absent.
const settle = async () => { await act(async () => { await new Promise((r) => setTimeout(r, 20)); }); };

it("shows a one-line card on session start without creating anything", async () => {
	render(card());
	const el = await screen.findByTestId("skills-offer-card");
	expect(el).toHaveTextContent("Claude Code can't see AGENTS.md in this project.");
	expect(within(el).getByRole("button", { name: "Review" })).toBeInTheDocument();
	expect(within(el).getByRole("button", { name: "Not now" })).toBeInTheDocument();
	expect(postMock).not.toHaveBeenCalled();
	expect(screen.queryByTestId("skills-offer-dialog")).not.toBeInTheDocument();
});

it("names the skills in the line", async () => {
	skills = skillsBody({ offers: [offer, second] });
	render(card());
	expect(await screen.findByTestId("skills-offer-card")).toHaveTextContent("Claude Code can't see AGENTS.md or these skills: lint, qa.");
});

it("stays away when there is nothing to offer or the project opted out", async () => {
	skills = skillsBody({ offers: [] });
	const { unmount } = render(card());
	await waitFor(() => expect(getMock).toHaveBeenCalled());
	await settle();
	expect(screen.queryByTestId("skills-offer-card")).not.toBeInTheDocument();
	unmount();
	skills = skillsBody({ offersEnabled: false });
	render(card("s2"));
	await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
	await settle();
	expect(screen.queryByTestId("skills-offer-card")).not.toBeInTheDocument();
});

it("Review opens the dialog listing each offer with its exact paths; Create applies one and shows the result and commit hint", async () => {
	skills = skillsBody({ offers: [offer, second] });
	render(card());
	await userEvent.click(await screen.findByRole("button", { name: "Review" }));
	const dialog = await screen.findByTestId("skills-offer-dialog");
	expect(dialog).toHaveTextContent("CLAUDE.md @AGENTS.md");
	expect(dialog).toHaveTextContent("qa: link .claude/skills/qa to .codex/skills/qa (same project, no copy)");
	expect(dialog).toHaveTextContent("lint: link .claude/skills/lint to .codex/skills/lint (same project, no copy)");
	expect(dialog).not.toHaveTextContent("../");
	expect(postMock).not.toHaveBeenCalled();
	skills = skillsBody({ offers: [second], pendingCommit: ["CLAUDE.md"] });
	await userEvent.click(within(dialog).getAllByRole("button", { name: "Create files only" })[0]);
	expect(await within(dialog).findByRole("status")).toHaveTextContent("Created, not committed yet.");
	expect(postMock).toHaveBeenCalledOnce();
	expect(postMock.mock.calls[0][1].params.path).toEqual({ id: "p", offerId: "claude-md" });
	await settle();
	// The applied offer keeps its row (disabled) even though the daemon stopped listing it; the other is still creatable.
	expect(within(dialog).getAllByRole("button", { name: "Create files only" })[0]).toBeDisabled();
	expect(within(dialog).getAllByRole("button", { name: "Create files only" })[1]).toBeEnabled();
	expect(dialog).toHaveTextContent("Instructions: CLAUDE.md");
});

it("Create all applies every offer not yet created", async () => {
	skills = skillsBody({ offers: [offer, second] });
	postMock.mockImplementation(async (_p: string, init: { params: { path: { offerId: string } } }) =>
		({ data: { created: [{ path: "x", mode: init.params.path.offerId === "claude-md" ? "file" : "symlink" }] } }));
	render(card());
	await userEvent.click(await screen.findByRole("button", { name: "Review" }));
	const dialog = await screen.findByTestId("skills-offer-dialog");
	await userEvent.click(within(dialog).getByRole("button", { name: "Create all without committing" }));
	await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2));
	expect(postMock.mock.calls.map((c) => c[1].params.path.offerId)).toEqual(["claude-md", "claude-skills"]);
	await waitFor(() => expect(within(dialog).getAllByRole("status")).toHaveLength(2));
	expect(within(dialog).queryByRole("button", { name: "Create all without committing" })).not.toBeInTheDocument();
});

it("shows a failed Create next to its offer", async () => {
	postMock.mockResolvedValue({ error: { message: "boom" } });
	render(card());
	await userEvent.click(await screen.findByRole("button", { name: "Review" }));
	const dialog = await screen.findByTestId("skills-offer-dialog");
	await userEvent.click(within(dialog).getByRole("button", { name: "Create files only" }));
	expect(await within(dialog).findByRole("alert")).toHaveTextContent("request failed");
	expect(within(dialog).getByRole("button", { name: "Create files only" })).toBeEnabled();
});

it("shows once per conversation start: 'Not now' stays dismissed, a new session offers again", async () => {
	const first = render(card());
	await userEvent.click(await screen.findByRole("button", { name: "Not now" }));
	expect(screen.queryByTestId("skills-offer-card")).not.toBeInTheDocument();
	first.unmount();
	// Reopening the same session does not nag again.
	const again = render(card());
	await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
	await settle();
	expect(screen.queryByTestId("skills-offer-card")).not.toBeInTheDocument();
	again.unmount();
	// A new conversation start does.
	render(card("s2"));
	expect(await screen.findByTestId("skills-offer-card")).toBeInTheDocument();
});

it("persists 'Don't ask again' from the overflow menu without dropping other settings", async () => {
	render(card());
	await userEvent.click(await screen.findByRole("button", { name: "More options" }));
	await userEvent.click(await screen.findByRole("menuitem", { name: "Don't ask again for this project" }));
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body).toEqual({ displayName: "Example", config: { autoReview: true, env: { A: "b" }, skillsOfferOff: true } });
	await waitFor(() => expect(screen.queryByTestId("skills-offer-card")).not.toBeInTheDocument());
});

it("re-enables the automatic offer from Settings > Skills", async () => {
	projectConfig = { autoReview: true, skillsOfferOff: true };
	skills = skillsBody({ offersEnabled: false });
	render(wrap(<ProjectSkillsSettings projectId="p" />));
	expect(await screen.findByText("Automatic offers are off for this project")).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Turn on" }));
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body.config).toEqual({ autoReview: true });
});

it("says sessions can't see uncommitted files, listing them in the dialog, when nothing is left to create", async () => {
	skills = skillsBody({ offers: [], pendingCommit: ["CLAUDE.md", ".claude/skills/qa"] });
	render(card());
	const el = await screen.findByTestId("skills-offer-card");
	expect(el).toHaveTextContent("Not committed yet, so sessions can't see: CLAUDE.md, qa.");
	await userEvent.click(within(el).getByRole("button", { name: "Review" }));
	const dialog = await screen.findByTestId("skills-offer-dialog");
	expect(within(dialog).getByRole("heading")).toHaveTextContent("Commit these files so sessions can see them.");
	expect(dialog).toHaveTextContent("Instructions: CLAUDE.md");
	expect(dialog).toHaveTextContent("Skills: qa");
	expect(dialog).not.toHaveTextContent("1 link");
	// Without a resolvable session base the dialog says so instead of offering a commit.
	expect(dialog).toHaveTextContent("Couldn't find the branch new sessions start from");
	expect(within(dialog).queryByRole("button", { name: "Create files only" })).not.toBeInTheDocument();
});

it("does not carry one project's offers into another project or session", async () => {
	const c = client();
	const ui = (projectId: string, sessionId: string) => wrap(<><SkillsOfferCard projectId={projectId} sessionId={sessionId} /><SkillsOfferDialogHost /></>, c);
	const { rerender } = render(ui("a", "s1"));
	await screen.findByTestId("skills-offer-card");
	getMock.mockImplementation(async (path: string, init?: { params?: { path?: { id?: string } } }) =>
		path === "/api/v1/projects/{id}/skills"
			? { data: init?.params?.path?.id === "b" ? skillsBody({ projectId: "b", offers: [{ id: "codex-md", harness: "codex", items: [{ path: "AGENTS.md", kind: "link", target: "CLAUDE.md" }] }] }) : skillsBody({ offers: [], pendingCommit: ["CLAUDE.md"] }) }
			: { data: { status: "ok", project: { id: "p", name: "Example", config: {} } } });
	rerender(ui("b", "s2"));
	await waitFor(() => expect(screen.getByTestId("skills-offer-card")).toHaveTextContent("Codex can't see CLAUDE.md in this project."));
	await userEvent.click(screen.getByRole("button", { name: "Review" }));
	const dialog = await screen.findByTestId("skills-offer-dialog");
	expect(dialog).toHaveTextContent("Create AGENTS.md linked to CLAUDE.md");
	expect(dialog).not.toHaveTextContent("Create CLAUDE.md that imports AGENTS.md");
});

it("project add: a toast with Review opens the same dialog, and no card is claimed", async () => {
	const c = client();
	const { rerender } = renderHook(({ ids }) => useSkillsOfferOnProjectAdd(ids), { initialProps: { ids: undefined as string[] | undefined }, wrapper: ({ children }) => wrap(children, c) });
	rerender({ ids: ["a"] });
	await settle();
	expect(useUiStore.getState().globalToasts).toHaveLength(0);
	rerender({ ids: ["a", "p"] });
	await waitFor(() => expect(useUiStore.getState().globalToasts).toHaveLength(1));
	const toast = useUiStore.getState().globalToasts[0];
	expect(toast.title).toBe("Some of this project's instructions or skills are hidden from your agents.");
	expect(toast.action?.label).toBe("Review");
	expect(useSkillsOfferStore.getState().active.size).toBe(0);
	render(wrap(<SkillsOfferDialogHost />, c));
	await act(async () => { toast.action?.onClick(); });
	expect(await screen.findByTestId("skills-offer-dialog")).toHaveTextContent("CLAUDE.md @AGENTS.md");
});

it("project add: no toast when the project has nothing to offer or opted out", async () => {
	skills = skillsBody({ offers: [] });
	const c = client();
	const { rerender } = renderHook(({ ids }) => useSkillsOfferOnProjectAdd(ids), { initialProps: { ids: ["a"] as string[] | undefined }, wrapper: ({ children }) => wrap(children, c) });
	rerender({ ids: ["a", "p"] });
	await waitFor(() => expect(getMock).toHaveBeenCalled());
	await settle();
	expect(useUiStore.getState().globalToasts).toHaveLength(0);
	skills = skillsBody({ offersEnabled: false });
	rerender({ ids: ["a", "p", "q"] });
	await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
	await settle();
	expect(useUiStore.getState().globalToasts).toHaveLength(0);
});

it("a reused dialog host never shows the previous project's offers or results", async () => {
	const c = client();
	render(wrap(<SkillsOfferDialogHost />, c));
	act(() => useSkillsOfferStore.getState().openReview("a"));
	const dialogA = await screen.findByTestId("skills-offer-dialog");
	await userEvent.click(await within(dialogA).findByRole("button", { name: "Create files only" }));
	await within(dialogA).findByRole("status");
	getMock.mockImplementation(async (path: string, init?: { params?: { path?: { id?: string } } }) =>
		path === "/api/v1/projects/{id}/skills"
			? { data: init?.params?.path?.id === "b" ? skillsBody({ projectId: "b", offers: [{ id: "codex-md", harness: "codex", items: [{ path: "AGENTS.md", kind: "link", target: "CLAUDE.md" }] }] }) : skillsBody({ offers: [], pendingCommit: ["CLAUDE.md"] }) }
			: { data: {} });
	act(() => useSkillsOfferStore.getState().openReview("b"));
	await screen.findByText("Create AGENTS.md linked to CLAUDE.md");
	const dialogB = screen.getByTestId("skills-offer-dialog");
	expect(dialogB).not.toHaveTextContent("Create CLAUDE.md that imports AGENTS.md");
	expect(within(dialogB).queryByRole("status")).not.toBeInTheDocument();
	expect(within(dialogB).getByRole("button", { name: "Create files only" })).toBeEnabled();
});

it("a failed 'Don't ask again' error does not follow the user into another session", async () => {
	putMock.mockResolvedValue({ error: { message: "nope" } });
	const c = client();
	const ui = (sessionId: string) => wrap(<SkillsOfferCard projectId="p" sessionId={sessionId} />, c);
	const { rerender } = render(ui("s1"));
	await userEvent.click(await screen.findByRole("button", { name: "More options" }));
	await userEvent.click(await screen.findByRole("menuitem", { name: "Don't ask again for this project" }));
	expect(await screen.findByRole("alert")).toBeInTheDocument();
	rerender(ui("s2"));
	await screen.findByTestId("skills-offer-card");
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("is never in a window drag region: the card and dialog opt out of -webkit-app-region and sit outside the top bar", async () => {
	const css = (await import("node:fs")).readFileSync("src/renderer/styles.css", "utf8");
	const rule = css.slice(css.indexOf('[data-testid="skills-offer-card"],'));
	const block = rule.slice(0, rule.indexOf("}"));
	expect(block).toContain('[data-testid="skills-offer-card"] *');
	expect(block).toContain('[data-testid="skills-offer-dialog"] *');
	expect(block).toContain("-webkit-app-region: no-drag");
	render(card());
	const el = await screen.findByTestId("skills-offer-card");
	expect(el.closest('[data-testid="session-topbar-host"]')).toBeNull();
});

it("a single Create refreshes the project-skills data so the chat card updates", async () => {
	render(card());
	await userEvent.click(await screen.findByRole("button", { name: "Review" }));
	const dialog = await screen.findByTestId("skills-offer-dialog");
	const before = getMock.mock.calls.filter((c) => c[0] === "/api/v1/projects/{id}/skills").length;
	skills = skillsBody({ offers: [], pendingCommit: ["CLAUDE.md"] });
	await userEvent.click(within(dialog).getByRole("button", { name: "Create files only" }));
	await within(dialog).findByRole("status");
	await waitFor(() => expect(getMock.mock.calls.filter((c) => c[0] === "/api/v1/projects/{id}/skills").length).toBeGreaterThan(before));
	await userEvent.click(within(dialog).getAllByRole("button", { name: "Close" })[0]);
	await waitFor(() => expect(screen.getByTestId("skills-offer-card")).toHaveTextContent("Not committed yet, so sessions can't see: CLAUDE.md."));
});
