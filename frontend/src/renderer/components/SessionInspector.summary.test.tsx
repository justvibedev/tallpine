import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import { TooltipProvider } from "./ui/tooltip";
import { conversationQueryKey } from "../hooks/conversation";
import { useSessionGitActionStore } from "../stores/session-git-action-store";
import { pr, session, InspectorWithSwitcher, switcherLabels, renderWithQuery, commonGetsResponder, installInspectorLifecycle } from "../test/session-inspector-fixtures";
import { getMock, postMock, postCallsFor } from "../test/session-inspector-mocks";

vi.mock("@tanstack/react-router", () => import("../test/session-inspector-mocks").then((m) => m.routerModule()));
vi.mock("../lib/preview-mode", () => import("../test/session-inspector-mocks").then((m) => m.previewModeModule()));
vi.mock("../lib/api-client", () => import("../test/session-inspector-mocks").then((m) => m.apiClientModule()));
vi.mock("../lib/host-clients", async (importOriginal) =>
  (await import("../test/session-inspector-mocks")).hostClientsModule(await importOriginal<typeof import("../lib/host-clients")>()),
);

installInspectorLifecycle();

describe("SessionInspector branch summary", () => {
  const changedFile = (path: string, additions: number, deletions: number) => ({
    path,
    status: "modified" as const,
    additions,
    deletions,
    size: 100,
    binary: false,
    editable: true,
    fileFingerprint: path,
  });
  type ChangedFile = ReturnType<typeof changedFile>;

  // Uncommitted work comes from the workspace manifest; commit and push facts
  // come from the daemon on the session itself (branchState).
  function mockGitState({
    uncommitted = [],
    committed = [],
  }: { uncommitted?: ChangedFile[]; committed?: ChangedFile[] }) {
    const respond = commonGetsResponder();
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/sessions/{sessionId}/workspace/manifest") {
        return {
          data: {
            sessionId: "sess-1",
            workspaceVersion: "v1",
            compareBaseRef: "origin/main",
            files: [...committed, ...uncommitted],
            sections: { staged: [], unstaged: uncommitted, untracked: [], committed },
            summary: { files: 0, additions: 0, deletions: 0 },
            truncated: false,
            degraded: false,
            stale: false,
            refreshing: false,
          },
          error: undefined,
        };
      }
      return respond(path);
    });
  }
  const pushed = (commits: number) => ({ commits, remoteBranch: "origin/feat/ns", unpushed: 0 });

  const sendCalls = () => postCallsFor("/api/v1/sessions/{sessionId}/send");
  const branch = () => within(screen.getByRole("region", { name: "Branch" }));

  it.each(["provisioning", "ready"] as const)("shimmers while naming in %s and reveals the final branch when its worktree is ready", (provisionState) => {
    mockGitState({});
    const starting = session([], {
      branch: "ao/sess-1/root",
      provisionState,
      provisionSteps: provisionState === "ready" ? [{ id: "worktree", status: "running" }] : [],
    });
    const view = renderWithQuery(<SessionInspector session={starting} />);
    const namingLabel = branch().getByRole("status");
    const namingText = namingLabel.textContent;
    expect(namingLabel).toHaveClass("chat-working-shimmer");
    expect(namingLabel).toHaveTextContent(/^(Generating branch name|Naming branch|Preparing branch|Initializing branch|Resolving branch name)$/);
    expect(screen.queryByText("ao/sess-1/root")).not.toBeInTheDocument();

    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <TooltipProvider>
          <SessionInspector session={{ ...starting, provisionSteps: [{ id: "worktree", status: "running" }] }} />
        </TooltipProvider>
      </QueryClientProvider>,
    );
    expect(branch().getByRole("status").textContent).toBe(namingText);

    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <TooltipProvider><SessionInspector session={{ ...starting, provisionState: "failed" }} /></TooltipProvider>
      </QueryClientProvider>,
    );
    expect(screen.queryByText(/^(Generating branch name|Naming branch|Preparing branch|Initializing branch|Resolving branch name)$/)).not.toBeInTheDocument();
    expect(screen.queryByText("ao/sess-1/root")).not.toBeInTheDocument();

    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <TooltipProvider>
          <SessionInspector session={{ ...starting, branch: "ao/fix-login/root", provisionSteps: [{ id: "worktree", status: "done" }] }} />
        </TooltipProvider>
      </QueryClientProvider>,
    );
    expect(branch().getByText("ao/fix-login/root")).toBeInTheDocument();
    expect(screen.queryByText(/^(Generating branch name|Naming branch|Preparing branch|Initializing branch|Resolving branch name)$/)).not.toBeInTheDocument();
  });

  beforeEach(() => {
    useSessionGitActionStore.setState({ pending: {} });
  });

  it("shows uncommitted work and commits, pushes, and opens a PR in one step", async () => {
    mockGitState({ uncommitted: [changedFile("src/a.ts", 10, 2), changedFile("src/b.ts", 3, 1)] });
    renderWithQuery(<SessionInspector session={session([])} />);

    expect(await screen.findByText("2 uncommitted files")).toBeInTheDocument();
    expect(branch().getByText("feat/ns")).toBeInTheDocument();
    expect(branch().getByText("→ main")).toBeInTheDocument();
    expect(branch().getByText("+13")).toBeInTheDocument();
    expect(within(screen.getByRole("tab", { name: "Files" })).getByText("2")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Commit & create PR" }));

    await waitFor(() => expect(sendCalls()).toHaveLength(1));
    expect(sendCalls()[0][1]).toEqual({
      params: { path: { sessionId: "sess-1" } },
      body: { message: expect.stringContaining("open a pull request against `main`") },
    });
    expect(await screen.findByRole("button", { name: "Creating PR…" })).toBeDisabled();
    expect(screen.queryByText(/agent/i)).not.toBeInTheDocument();
  });

  it("opens the Files tab from a status row", async () => {
    mockGitState({ uncommitted: [changedFile("src/a.ts", 1, 0)] });
    renderWithQuery(<SessionInspector session={session([])} />);

    await userEvent.click(await screen.findByRole("button", { name: /1 uncommitted file/ }));

    expect(screen.getByRole("tab", { name: "Files" })).toHaveAttribute("aria-selected", "true");
  });

  it("offers push before a PR when commits never left this machine", async () => {
    mockGitState({ committed: [changedFile("src/a.ts", 5, 0)] });
    renderWithQuery(<SessionInspector session={session([], { branchState: { commits: 2, unpushed: 2 } })} />);

    expect(await screen.findByText("2 commits")).toBeInTheDocument();
    expect(screen.getByText("not pushed")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Push & create PR" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "More git actions" }));
    expect(await screen.findByRole("menuitem", { name: "Push" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("menuitem", { name: "Create draft PR" }));

    await waitFor(() => expect(sendCalls()).toHaveLength(1));
    expect(sendCalls()[0][1].body.message).toContain("open a draft pull request");
    expect(await screen.findByRole("button", { name: "Creating PR…" })).toBeDisabled();
  });

  it("offers a plain Create PR once every commit is pushed", async () => {
    mockGitState({ committed: [changedFile("src/a.ts", 5, 0)] });
    renderWithQuery(<SessionInspector session={session([], { branchState: pushed(2) })} />);

    expect(await screen.findByText("pushed")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Create PR" })).toBeInTheDocument();
  });

  it("commits without pushing from the menu", async () => {
    mockGitState({ uncommitted: [changedFile("src/a.ts", 1, 0)] });
    renderWithQuery(<SessionInspector session={session([])} />);

    await userEvent.click(await screen.findByRole("button", { name: "More git actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Commit" }));

    await waitFor(() => expect(sendCalls()).toHaveLength(1));
    expect(sendCalls()[0][1].body.message).toBe("Commit all uncommitted changes with a clear message. Do not push.");
    expect(await screen.findByRole("button", { name: "Committing…" })).toBeDisabled();
  });

  it("updates the open PR when new work is uncommitted", async () => {
    mockGitState({ uncommitted: [changedFile("src/c.ts", 4, 0)], committed: [changedFile("src/a.ts", 5, 0)] });
    renderWithQuery(<SessionInspector session={session([pr(7, "open")], { branchState: pushed(3) })} />);

    expect(await screen.findByText("1 uncommitted file")).toBeInTheDocument();
    expect(branch().getByText("PR #7")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create PR" })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Commit & push to #7" }));

    await waitFor(() => expect(sendCalls()).toHaveLength(1));
    expect(sendCalls()[0][1].body.message).toContain("push this branch to update PR #7");
    expect(await screen.findByRole("button", { name: "Committing and pushing…" })).toBeDisabled();
  });

  it("shows no git action when the PR already has every commit", async () => {
    mockGitState({ committed: [changedFile("src/a.ts", 5, 0)] });
    renderWithQuery(<SessionInspector session={session([pr(7, "open")], { branchState: pushed(3) })} />);

    expect(await screen.findByText("3 commits")).toBeInTheDocument();
    expect(screen.getByText("pushed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More git actions" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^(Push|Commit & push) to #7$/ })).not.toBeInTheDocument();
  });

  it("leaves the loading state once the PR appears", async () => {
    mockGitState({ uncommitted: [changedFile("src/a.ts", 1, 0)] });
    const { queryClient, rerender } = renderWithQuery(<SessionInspector session={session([])} />);

    await userEvent.click(await screen.findByRole("button", { name: "Commit & create PR" }));
    expect(await screen.findByRole("button", { name: "Creating PR…" })).toBeInTheDocument();

    rerender(
      <QueryClientProvider client={queryClient}>
        <TooltipProvider>
          <SessionInspector session={session([pr(7, "open")])} />
        </TooltipProvider>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(screen.queryByRole("button", { name: "Creating PR…" })).not.toBeInTheDocument());
    expect(useSessionGitActionStore.getState().pending).toEqual({});
  });

  it("finishes Pushing when the daemon reports the branch pushed", async () => {
    mockGitState({ committed: [changedFile("src/a.ts", 5, 0)] });
    const { queryClient, rerender } = renderWithQuery(
      <SessionInspector session={session([pr(7, "open")], { branchState: { commits: 3, remoteBranch: "origin/feat/ns", unpushed: 1 } })} />,
    );

    expect(await screen.findByText("1 not pushed")).toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: "Push to #7" }));
    expect(await screen.findByRole("button", { name: "Pushing…" })).toBeDisabled();

    rerender(
      <QueryClientProvider client={queryClient}>
        <TooltipProvider>
          <SessionInspector session={session([pr(7, "open")], { branchState: pushed(3) })} />
        </TooltipProvider>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(screen.queryByRole("button", { name: "Pushing…" })).not.toBeInTheDocument());
    expect(screen.getByText("pushed")).toBeInTheDocument();
  });

  it("restores the action and shows the error when the request fails", async () => {
    mockGitState({ uncommitted: [changedFile("src/a.ts", 1, 0)] });
    postMock.mockImplementation(async (path: string) =>
      path === "/api/v1/sessions/{sessionId}/send"
        ? { data: undefined, error: { message: "Session is terminated" } }
        : { data: { ok: true, sessionId: "sess-1" }, error: undefined },
    );
    renderWithQuery(<SessionInspector session={session([])} />);

    await userEvent.click(await screen.findByRole("button", { name: "Commit & create PR" }));

    expect(await screen.findByText("Session is terminated")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Commit & create PR" })).toBeEnabled();
  });
});

describe("SessionInspector Plan tab", () => {
  const withPlan = (sessionId: string, spansTurns: boolean) => (client: QueryClient) =>
    client.setQueryData(conversationQueryKey(sessionId), {
      pageParams: [undefined],
      pages: [
        {
          sessionId,
          items: [],
          turns: [{ id: "turn-1", plan: { spansTurns, steps: [{ text: "Ship it", status: "in_progress" }] } }],
        },
      ],
    });

  it("appears only when the session keeps a plan across turns", () => {
    const current = session([]);
    const none = renderWithQuery(<SessionInspector session={current} />);
    expect(screen.queryByRole("button", { name: "Plan" })).not.toBeInTheDocument();
    none.unmount();

    const perTurn = renderWithQuery(<SessionInspector session={current} />, undefined, withPlan(current.id, false));
    expect(screen.queryByRole("button", { name: "Plan" })).not.toBeInTheDocument();
    perTurn.unmount();

    renderWithQuery(<InspectorWithSwitcher session={current} />, undefined, withPlan(current.id, true));
    expect(screen.getByRole("button", { name: "Plan" })).toBeInTheDocument();
  });

  it("offers the Plan tab beside Browser in an orchestrator's inspector", () => {
    const orchestrator = session([], { kind: "orchestrator" });
    renderWithQuery(
      <InspectorWithSwitcher browserOnly session={orchestrator} view="plan" />,
      undefined,
      withPlan(orchestrator.id, true),
    );
    expect(switcherLabels()).toEqual(["Browser", "Plan"]);
    expect(screen.getByRole("button", { name: "Plan", pressed: true })).toBeInTheDocument();
    expect(screen.getByText("Ship it")).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Files" })).not.toBeInTheDocument();
  });

  it("falls back to Browser when an orchestrator has no plan to show", () => {
    renderWithQuery(<InspectorWithSwitcher browserOnly session={session([], { kind: "orchestrator" })} view="plan" />);
    expect(screen.queryByRole("button", { name: "Plan" })).not.toBeInTheDocument();
    expect(screen.getByRole("complementary", { name: "Session inspector" }).querySelector(".session-inspector__body--browser")).toBeInTheDocument();
  });
});
