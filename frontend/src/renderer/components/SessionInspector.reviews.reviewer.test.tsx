import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import { pr, session, sessionWithProvider, renderWithQuery, commonGetsResponder, approvedReview, failedReview, reviewState, mockCommonGets, installInspectorLifecycle } from "../test/session-inspector-fixtures";
import { getMock, postMock, postCallsFor } from "../test/session-inspector-mocks";

vi.mock("@tanstack/react-router", () => import("../test/session-inspector-mocks").then((m) => m.routerModule()));
vi.mock("../lib/preview-mode", () => import("../test/session-inspector-mocks").then((m) => m.previewModeModule()));
vi.mock("../lib/api-client", () => import("../test/session-inspector-mocks").then((m) => m.apiClientModule()));
vi.mock("../lib/host-clients", async (importOriginal) =>
  (await import("../test/session-inspector-mocks")).hostClientsModule(await importOriginal<typeof import("../lib/host-clients")>()),
);

installInspectorLifecycle();

describe("SessionInspector summary reviews", () => {
  // Review rows start collapsed. Open the Reviews tab and reveal every row,
  // since these tests are about what a review says.
  const openReviewsSection = async () => {
    await userEvent.click(screen.getByRole("tab", { name: "Reviews" }));
    // Rows arrive with the reviews query, so wait for them before expanding.
    const rows = await screen.findAllByTestId("review-pr-row", {}, { timeout: 1_000 }).catch(() => []);
    for (const row of rows) {
      if (row.getAttribute("aria-expanded") === "false")
        await userEvent.click(row);
    }
  };
  it("persists the chosen reviewer for the session and uses it for the run", async () => {
    mockCommonGets([], "reviewer-pane", [
      reviewState(3, "needs_review", "sha-1"),
    ]);
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      response: { status: 201 },
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    await userEvent.click(
      await screen.findByRole("button", { name: /Select reviewer agent/ }),
    );
    await userEvent.click(
      await screen.findByRole("menuitem", { name: /kiro/ }),
    );
    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: "kiro" },
        },
      ),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Review latest commit" }),
    );

    expect(postMock).toHaveBeenCalledWith(
      "/api/v1/sessions/{sessionId}/reviews/trigger",
      {
        params: { path: { sessionId: "sess-1" } },
        body: { harness: "kiro" },
      },
    );
  });

  it("preserves hidden reviewer config fields when saving a session reviewer model", async () => {
    getMock.mockImplementation(async (path: string, options?: { params?: { path?: { agent?: string } } }) => {
      if (path === "/api/v1/agents/{agent}/models" && options?.params?.path?.agent === "codex") {
        return {
          data: {
            agentId: "codex",
            selectionMode: "catalog",
            models: [
              { id: "gpt-5", label: "GPT-5", isDefault: true },
              { id: "gpt-5-mini", label: "GPT-5 Mini" },
            ],
            allowCustom: false,
            source: "official-catalog",
            fetchedAt: "2026-08-30T00:00:00Z",
            stale: false,
          },
          error: undefined,
        };
      }
      return commonGetsResponder([], "reviewer-pane", [reviewState(3, "needs_review", "sha-1")])(path);
    });
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      error: undefined,
      response: { status: 200 },
    });

    renderWithQuery(
      <SessionInspector
        session={session([pr(3, "open")], {
          reviewerHarness: "codex",
          reviewerConfig: { model: "gpt-5", permissions: "bypass-permissions" },
        })}
      />,
    );
    await openReviewsSection();

    await userEvent.click(await screen.findByRole("button", { name: /Select reviewer agent/ }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /codex/i }));
    await waitFor(() =>
      expect(screen.getByRole("menuitem", { name: "GPT-5 Mini" })).toBeInTheDocument(),
    );
    expect(postCallsFor("/api/v1/sessions/{sessionId}/reviews/switch")).toHaveLength(0);
    await userEvent.click(screen.getByRole("menuitem", { name: "GPT-5 Mini" }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: undefined, agentConfig: { model: "gpt-5-mini", permissions: "bypass-permissions" } },
        },
      ),
    );
  });

  it("preserves hidden reviewer config when the explicit override matches the default harness", async () => {
    getMock.mockImplementation(async (path: string, options?: { params?: { path?: { agent?: string } } }) => {
      if (path === "/api/v1/agents/{agent}/models" && options?.params?.path?.agent === "codex") {
        return {
          data: {
            agentId: "codex",
            selectionMode: "catalog",
            models: [
              { id: "gpt-5", label: "GPT-5", isDefault: true },
              { id: "gpt-5-mini", label: "GPT-5 Mini" },
            ],
            allowCustom: false,
            source: "official-catalog",
            fetchedAt: "2026-08-30T00:00:00Z",
            stale: false,
          },
          error: undefined,
        };
      }
      return commonGetsResponder([], "reviewer-pane", [reviewState(3, "needs_review", "sha-1")])(path);
    });
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      error: undefined,
      response: { status: 200 },
    });

    renderWithQuery(
      <SessionInspector
        session={session([pr(3, "open")], {
          provider: "codex",
          reviewerHarness: "codex",
          reviewerConfig: { permissions: "bypass-permissions" },
        })}
      />,
    );
    await openReviewsSection();

    await userEvent.click(await screen.findByRole("button", { name: /Select reviewer agent/ }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /codex/i }));
    await waitFor(() =>
      expect(screen.getByRole("menuitem", { name: "GPT-5 Mini" })).toBeInTheDocument(),
    );
    expect(postCallsFor("/api/v1/sessions/{sessionId}/reviews/switch")).toHaveLength(0);

    await userEvent.click(screen.getByRole("menuitem", { name: "GPT-5 Mini" }));
    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: undefined, agentConfig: { model: "gpt-5-mini", permissions: "bypass-permissions" } },
        },
      ),
    );
  });

  it("does not expose arbitrary custom reviewer models from another reviewer row", async () => {
    getMock.mockImplementation(async (path: string, options?: { params?: { path?: { agent?: string } } }) => {
      if (path === "/api/v1/agents/{agent}/models") {
        const agent = options?.params?.path?.agent ?? "";
        return {
          data: {
            agentId: agent,
            selectionMode: agent === "kiro" ? "text" : "catalog",
            models: [],
            allowCustom: agent === "kiro",
            source: "manual",
            fetchedAt: "2026-08-30T00:00:00Z",
            stale: false,
          },
          error: undefined,
        };
      }
      return commonGetsResponder([], "reviewer-pane", [reviewState(3, "needs_review", "sha-1")])(path);
    });
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      error: undefined,
      response: { status: 200 },
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    await userEvent.click(await screen.findByRole("button", { name: /Select reviewer agent/ }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /^kiro$/i }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: "kiro" },
        },
      ),
    );
    expect(screen.queryByRole("menuitem", { name: /^custom kiro model$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /custom kiro/i })).not.toBeInTheDocument();
    expect(postCallsFor("/api/v1/sessions/{sessionId}/reviews/switch")).toHaveLength(1);
  });

  it("shows suggested reviewer models after reopening the selected reviewer picker", async () => {
    getMock.mockImplementation(async (path: string, options?: { params?: { path?: { agent?: string } } }) => {
      if (path === "/api/v1/agents/{agent}/models") {
        const agent = options?.params?.path?.agent ?? "";
        if (agent === "kiro") {
          return {
            data: {
              agentId: agent,
              selectionMode: "text",
              models: [
                { id: "suggested-a", label: "Suggested A" },
                { id: "suggested-b", label: "Suggested B" },
              ],
              allowCustom: true,
              source: "manual",
              fetchedAt: "2026-08-30T00:00:00Z",
              stale: false,
            },
            error: undefined,
          };
        }
        return {
          data: {
            agentId: agent,
            selectionMode: "catalog",
            models: [],
            allowCustom: false,
            source: "manual",
            fetchedAt: "2026-08-30T00:00:00Z",
            stale: false,
          },
          error: undefined,
        };
      }
      return commonGetsResponder([], "reviewer-pane", [reviewState(3, "needs_review", "sha-1")])(path);
    });
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      error: undefined,
      response: { status: 200 },
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    await userEvent.click(await screen.findByRole("button", { name: /Select reviewer agent/ }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /^kiro$/i }));
    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: "kiro" },
        },
      ),
    );
    await userEvent.click(await screen.findByRole("button", { name: /Select reviewer agent/ }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /^kiro$/i }));
    expect(await screen.findByRole("menuitem", { name: "Suggested A" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /^custom kiro model$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /custom kiro/i })).not.toBeInTheDocument();
  });

  it("allows selecting a text-selection reviewer harness without exposing custom reviewer entry", async () => {
    getMock.mockImplementation(async (path: string, options?: { params?: { path?: { agent?: string } } }) => {
      if (path === "/api/v1/agents/{agent}/models") {
        const agent = options?.params?.path?.agent ?? "";
        return {
          data: {
            agentId: agent,
            selectionMode: agent === "kiro" ? "text" : "catalog",
            models: [],
            allowCustom: agent === "kiro",
            source: "manual",
            fetchedAt: "2026-08-30T00:00:00Z",
            stale: false,
          },
          error: undefined,
        };
      }
      return commonGetsResponder([], "reviewer-pane", [reviewState(3, "needs_review", "sha-1")])(path);
    });
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      error: undefined,
      response: { status: 200 },
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    await userEvent.click(await screen.findByRole("button", { name: /Select reviewer agent/ }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /^kiro$/i }));
    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: "kiro" },
        },
      ),
    );
    expect(screen.queryByRole("menuitem", { name: /^custom kiro model$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /custom kiro/i })).not.toBeInTheDocument();
  });

  it("keeps the current default reviewer open for model selection", async () => {
    getMock.mockImplementation(async (path: string, options?: { params?: { path?: { agent?: string } } }) => {
      if (path === "/api/v1/agents/{agent}/models" && options?.params?.path?.agent === "codex") {
        return {
          data: {
            agentId: "codex",
            selectionMode: "catalog",
            models: [
              { id: "gpt-5", label: "GPT-5", isDefault: true },
              { id: "gpt-5-mini", label: "GPT-5 Mini" },
            ],
            allowCustom: false,
            source: "official-catalog",
            fetchedAt: "2026-08-29T00:00:00Z",
            stale: false,
          },
          error: undefined,
        };
      }
      return commonGetsResponder([], "reviewer-pane", [reviewState(3, "needs_review", "sha-1")])(path);
    });
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      error: undefined,
      response: { status: 200 },
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    await userEvent.click(await screen.findByRole("button", { name: /Select reviewer agent/ }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /codex/i }));

    await waitFor(() =>
      expect(screen.getByRole("menuitem", { name: "GPT-5 Mini" })).toBeInTheDocument(),
    );
    expect(postCallsFor("/api/v1/sessions/{sessionId}/reviews/switch")).toHaveLength(0);
    await userEvent.click(screen.getByRole("menuitem", { name: "GPT-5 Mini" }));
    await waitFor(() =>
      expect(postMock).toHaveBeenLastCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: undefined, agentConfig: { model: "gpt-5-mini" } },
        },
      ),
    );
    expect(postCallsFor("/api/v1/sessions/{sessionId}/reviews/switch")).toHaveLength(1);
  });

  it("clears hidden session reviewer config when returning an explicit default-matching reviewer to project default", async () => {
    mockCommonGets([], "reviewer-pane", [reviewState(3, "needs_review", "sha-1")]);
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      response: { status: 200 },
    });

    renderWithQuery(
      <SessionInspector
        session={session([pr(3, "open")], {
          provider: "codex",
          reviewerHarness: "codex",
          reviewerConfig: { permissions: "bypass-permissions" },
        })}
      />,
    );
    await openReviewsSection();

    const picker = await screen.findByRole("button", {
      name: /Select reviewer agent/,
    });
    await userEvent.click(picker);
    await userEvent.click(screen.getByRole("menuitem", { name: /codex/i }));

    await waitFor(() =>
      expect(postMock).toHaveBeenLastCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: undefined, agentConfig: undefined },
        },
      ),
    );
  });

  it("keeps an explicit reviewer visible and lets it return to the resolved default", async () => {
    mockCommonGets([], "reviewer-pane", [
      reviewState(3, "needs_review", "sha-1"),
    ]);
    postMock.mockResolvedValue({
      data: { reviewerHandleId: "", reviews: [] },
      response: { status: 201 },
    });

    renderWithQuery(
      <SessionInspector
        session={sessionWithProvider([pr(3, "open")], "codex")}
      />,
    );
    await openReviewsSection();

    const picker = await screen.findByRole("button", {
      name: /Select reviewer agent/,
    });
    await userEvent.click(picker);
    expect(screen.getAllByRole("menuitem", { name: /codex/i })).toHaveLength(1);
    expect(
      screen.getByRole("menuitem", { name: /kiro/ }),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("menuitem", { name: /kiro/ }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: "kiro" },
        },
      ),
    );

    await userEvent.click(picker);
    expect(screen.getAllByRole("menuitem", { name: /codex/i })).toHaveLength(1);
    await userEvent.click(screen.getByRole("menuitem", { name: /codex/i }));

    await waitFor(() =>
      expect(postMock).toHaveBeenLastCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/switch",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { harness: undefined },
        },
      ),
    );
  });

  it("names the reviewer that is actually running, not whichever PR comes first", async () => {
    // One PR reviewed earlier by claude-code, another running under codex.
    const done = {
      ...reviewState(3, "up_to_date", "sha-a"),
      latestRun: {
        ...approvedReview,
        id: "run-done",
        harness: "claude-code",
        status: "complete",
      },
    };
    const running = {
      ...reviewState(4, "running", "sha-b"),
      latestRun: {
        ...approvedReview,
        id: "run-live",
        harness: "codex",
        status: "running",
        verdict: "",
        createdAt: "2026-01-02T00:00:00Z",
      },
    };
    mockCommonGets([], "reviewer-pane", [done, running], "active");

    renderWithQuery(
      <SessionInspector session={session([pr(3, "open"), pr(4, "open")])} />,
    );
    await openReviewsSection();

    expect(
      await screen.findByText("Review in progress · Codex"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Review in progress · Claude Code"),
    ).not.toBeInTheDocument();
  });

  it("keeps older harness summaries behind explicit pagination when selecting the next agent", async () => {
    const state = {
      ...reviewState(3, "changes_requested", "sha-1"),
      latestRun: {
        ...approvedReview,
        id: "run-codex",
        harness: "codex",
        verdict: "changes_requested",
        body: "codex asked for tests.",
        createdAt: "2026-01-03T00:00:00Z",
      },
    };
    mockCommonGets([], "reviewer-pane", [state]);
    const previous = getMock.getMockImplementation()!;
    getMock.mockImplementation(async (path: string, opts?: unknown) => {
      if (path === "/api/v1/sessions/{sessionId}/reviews") {
        return {
          data: {
            reviewerHandleId: "reviewer-pane",
            reviews: [state],
            runs: [
              state.latestRun,
              {
                ...approvedReview,
                id: "run-claude",
                harness: "claude-code",
                verdict: "approved",
                body: "claude-code found nothing blocking.",
                createdAt: "2026-01-01T00:00:00Z",
              },
            ],
          },
        };
      }
      return previous(path, opts);
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(
      await screen.findByText("codex asked for tests."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("claude-code found nothing blocking."),
    ).not.toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Load more · 1 earlier" }),
    );
    expect(
      screen.getByText("claude-code found nothing blocking."),
    ).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Show latest only" }),
    );
    expect(
      screen.queryByText("claude-code found nothing blocking."),
    ).not.toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /Select reviewer agent/ }),
    );
    await userEvent.click(
      screen.getByRole("menuitem", { name: /claude-code/ }),
    );
    expect(
      screen.queryByText("claude-code found nothing blocking."),
    ).not.toBeInTheDocument();
    expect(screen.getByText("codex asked for tests.")).toBeInTheDocument();
    expect(screen.getByText("Reviewable change 3")).toBeInTheDocument();
  });

  it("locks the reviewer choice while one is running", async () => {
    const running = {
      ...reviewState(3, "running", "sha-1"),
      latestRun: {
        ...approvedReview,
        id: "run-live",
        harness: "codex",
        status: "running",
        verdict: "",
      },
    };
    mockCommonGets([], "reviewer-pane", [running], "active");

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    // Tallpine runs one reviewer per worker, so a second harness cannot start
    // alongside it. Say so rather than silently ignoring the choice.
    expect(screen.getByText("Review in progress · Codex")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Select reviewer agent/ }),
    ).toBeDisabled();
  });

  it("hides the review in progress strip when the reviewer hook reports idle", async () => {
    const running = {
      ...reviewState(3, "running", "sha-1"),
      latestRun: {
        ...approvedReview,
        id: "run-live",
        harness: "codex",
        status: "running",
        verdict: "",
      },
    };
    mockCommonGets([], "reviewer-pane", [running], "idle");

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(
      screen.queryByText("Review in progress · Codex"),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Stop review" }),
    ).toBeInTheDocument();
  });

  it("shows the review in progress strip when the reviewer hook reports blocked", async () => {
    const running = {
      ...reviewState(3, "running", "sha-1"),
      latestRun: {
        ...approvedReview,
        id: "run-live",
        harness: "codex",
        status: "running",
        verdict: "",
      },
    };
    mockCommonGets([], "reviewer-pane", [running], "blocked");

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(
      await screen.findByText("Review in progress · Codex"),
    ).toBeInTheDocument();
  });

  it("hides the previous verdict after the current head review completes", async () => {
    const current = {
      ...reviewState(3, "up_to_date", "sha-current"),
      previousRun: {
        ...approvedReview,
        id: "run-previous",
        status: "delivered",
        verdict: "changes_requested",
        targetSha: "sha-previous",
      },
    };
    mockCommonGets([], "reviewer-pane", [current]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(await screen.findAllByText("Approved")).not.toHaveLength(0);
    expect(screen.queryByText(/Previous:/)).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Re-run review" }),
    ).toBeInTheDocument();
  });

  it.each([false, true])("confirms a same-commit re-review before sending it (confirm=%s)", async (confirm) => {
    mockCommonGets([approvedReview], "reviewer-pane", [reviewState(3, "up_to_date")]);
    postMock.mockResolvedValue({
      response: { status: 201 },
      data: { reviewerHandleId: "reviewer-pane", reviews: [{ ...reviewState(3, "running"), latestRun: { ...approvedReview, id: "rerun-1", status: "running", verdict: "" } }] },
    });
    const onOpenReviewerTerminal = vi.fn();
    renderWithQuery(<SessionInspector onOpenReviewerTerminal={onOpenReviewerTerminal} session={session([pr(3, "open")])} />);
    await openReviewsSection();
    await userEvent.click(await screen.findByRole("button", { name: "Re-run review" }));
    expect(await screen.findByText("This commit has already been reviewed, are you sure you want to re-review the same commit?")).toBeInTheDocument();
    expect(postMock).not.toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/trigger", expect.anything());
    await userEvent.click(screen.getByRole("button", { name: confirm ? "Re-review commit" : "Cancel" }));
    if (confirm) {
      await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/trigger", expect.objectContaining({ body: expect.objectContaining({ rerun: true }) })));
      await waitFor(() => expect(onOpenReviewerTerminal).toHaveBeenCalled());
    } else {
      expect(postMock).not.toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/trigger", expect.anything());
      expect(onOpenReviewerTerminal).not.toHaveBeenCalled();
    }
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("cancels the running review instead of allowing rerun", async () => {
    mockCommonGets([approvedReview], "reviewer-pane", [
      reviewState(3, "running", "abc123"),
      reviewState(4, "up_to_date", "def456"),
    ]);
    const onOpenReviewerTerminal = vi.fn();

    renderWithQuery(
      <SessionInspector
        onOpenReviewerTerminal={onOpenReviewerTerminal}
        session={session([pr(3, "open")])}
      />,
    );
    await openReviewsSection();

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Stop review" })).toBeEnabled(),
    );
    expect(
      screen.queryByRole("button", { name: /re-run review/i }),
    ).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^stop review$/i }));

    await waitFor(() => {
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/cancel",
        {
          params: { path: { sessionId: "sess-1" } },
        },
      );
    });
    expect(onOpenReviewerTerminal).not.toHaveBeenCalled();
  });

  it("does not list a cancelled run as a reviewed outcome", async () => {
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "needs_review", "abc123"),
        latestRun: { ...failedReview, status: "cancelled" },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(screen.queryByText("Cancelled")).not.toBeInTheDocument();
    expect(screen.queryByText("Failed")).not.toBeInTheDocument();
    expect(screen.queryByText("reviewer crashed")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Review latest commit" }),
    ).toBeEnabled();
  });

  it("shows the reviewer identity and aggregate verdict", async () => {
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "changes_requested", "abc123"),
        latestRun: {
          ...approvedReview,
          verdict: "changes_requested",
          body: "Please fix auth.",
        },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(
      await screen.findByRole("button", { name: /Select reviewer agent/ }),
    ).toHaveTextContent("Codex");
    expect(screen.queryByText("reviewer")).not.toBeInTheDocument();
    expect(screen.queryByText("sess-1")).not.toBeInTheDocument();
    expect(screen.queryByText("review session")).not.toBeInTheDocument();
    expect(screen.getAllByText("Changes requested")).not.toHaveLength(0);
  });

  it("does not list a failed run without a verdict and still allows reviewing the latest commit", async () => {
    mockCommonGets([failedReview], "reviewer-pane", [
      { ...reviewState(3, "needs_review", "abc123"), latestRun: failedReview },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(screen.queryByText("Failed")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Review latest commit" }),
    ).toBeEnabled();
  });

  it("surfaces the latest automatic review failure while auto review is enabled", async () => {
    const failedAutoReview = {
      ...failedReview,
      triggerSource: "auto",
      body: 'reviewer preflight: reviewer harness "kimi" is unauthorized',
    };
    mockCommonGets([], "", [
      {
        ...reviewState(3, "needs_review", "abc123"),
        latestRun: failedAutoReview,
      },
    ]);

    renderWithQuery(
      <SessionInspector
        session={session([pr(3, "open")], { autoReviewEnabled: true })}
      />,
    );
    // No PR rows render for this state, so skip openReviewsSection's row wait
    // and wait for the status itself.
    await userEvent.click(screen.getByRole("tab", { name: "Reviews" }));

    expect(await screen.findByRole("status")).toHaveTextContent(
      'Auto review Failed: reviewer preflight: reviewer harness "kimi" is unauthorized',
    );
  });

  it("hides Reviews and the review preference when the session has no PR", async () => {
    mockCommonGets();
    renderWithQuery(<SessionInspector session={session([], { outputType: "pr" })} />);

    await screen.findByRole("tab", { name: /Summary/ });
    expect(screen.queryByRole("tab", { name: /Reviews/ })).not.toBeInTheDocument();
    // No side chat view from the host (side chats off or unsupported): no tab.
    expect(screen.getAllByRole("tab").map((tab) => tab.textContent?.trim())).toEqual([
      "Summary",
      "Files",
    ]);
    expect(
      screen.queryByRole("switch", { name: "Automatically fix review comments" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("No pull request opened yet.")).not.toBeInTheDocument();
  });

  it("falls back to Summary when a controlled Reviews selection has no PR", async () => {
    const onViewChange = vi.fn();
    renderWithQuery(
      <SessionInspector
        onViewChange={onViewChange}
        session={session([])}
        view="reviews"
      />,
    );

    expect(screen.getByRole("tab", { name: "Summary" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.queryByRole("tab", { name: "Reviews" })).not.toBeInTheDocument();
    expect(screen.getByText("Activity")).toBeInTheDocument();
    await waitFor(() => expect(onViewChange).toHaveBeenCalledWith("summary"));
  });
});
