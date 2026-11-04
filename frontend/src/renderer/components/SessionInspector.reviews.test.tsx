import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { useUiStore } from "../stores/ui-store";
import { setRenderedOverflow, pr, session, sessionWithProvider, renderWithQuery, commonGetsResponder, approvedReview, reviewState, mockCommonGets, installInspectorLifecycle } from "../test/session-inspector-fixtures";
import { getMock, putMock, postMock, postCallsFor } from "../test/session-inspector-mocks";

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

  it("triggers a review and opens the returned reviewer terminal", async () => {
    mockCommonGets([], "", [reviewState(3, "needs_review")]);
    const runningReview = {
      ...approvedReview,
      status: "running",
      verdict: "",
      body: "",
    };
    postMock.mockResolvedValue({
      response: { status: 201 },
      data: {
        reviewerHandleId: "reviewer-pane",
        reviews: [{ ...reviewState(3, "running"), latestRun: runningReview }],
      },
    });
    const onOpenReviewerTerminal = vi.fn();

    renderWithQuery(
      <SessionInspector
        onOpenReviewerTerminal={onOpenReviewerTerminal}
        session={session([pr(3, "open")])}
      />,
    );
    await openReviewsSection();

    await userEvent.click(
      await screen.findByRole("button", { name: "Review latest commit" }),
    );

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/reviews/trigger",
        {
          params: { path: { sessionId: "sess-1" } },
          body: {},
        },
      ),
    );
    expect(onOpenReviewerTerminal).toHaveBeenCalledWith({
      handleId: "reviewer-pane",
      harness: "codex",
    });
  });

  it("opens reviewer Chat when triggering a review from a Chat session", async () => {
    mockCommonGets([], "", [reviewState(3, "needs_review")]);
    postMock.mockResolvedValue({
      response: { status: 201 },
      data: {
        reviewerHandleId: "reviewer-pane",
        reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" },
        reviews: [{ ...reviewState(3, "running"), latestRun: { ...approvedReview, status: "running", verdict: "", body: "" } }],
      },
    });
    const onOpenReviewerTerminal = vi.fn();
    const onOpenReviewerChat = vi.fn();

    renderWithQuery(
      <SessionInspector
        onOpenReviewerTerminal={onOpenReviewerTerminal}
        onOpenReviewerChat={onOpenReviewerChat}
        session={session([pr(3, "open")], { mode: "chat" })}
      />,
    );
    await openReviewsSection();
    await userEvent.click(await screen.findByRole("button", { name: "Review latest commit" }));

    await waitFor(() => expect(onOpenReviewerChat).toHaveBeenCalledWith("review-1"));
    expect(onOpenReviewerTerminal).not.toHaveBeenCalled();
  });

  it("lets the daemon select the reviewer interface", async () => {
    mockCommonGets([], "", [reviewState(3, "needs_review")]);
    postMock.mockResolvedValue({ response: { status: 200 }, data: { reviews: [], runs: [], reviewerHandleId: "" } });
    renderWithQuery(<SessionInspector session={session([pr(3, "open")], { provider: "codex" })} />);
    await openReviewsSection();
    expect(screen.queryByRole("button", { name: "Chat" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Terminal" })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: "Review latest commit" }));
    await waitFor(() => expect(postCallsFor("/api/v1/sessions/{sessionId}/reviews/trigger")).toHaveLength(1));
    expect(postCallsFor("/api/v1/sessions/{sessionId}/reviews/trigger")[0][1].body).not.toHaveProperty("interfaceMode");
  });

  it("does not override the stored Terminal surface on an ordinary review trigger", async () => {
    const common = commonGetsResponder([], "terminal-pane", [reviewState(3, "needs_review")]);
    getMock.mockImplementation(async (path: string) => path === "/api/v1/sessions/{sessionId}/reviews"
      ? { data: { reviewerHandleId: "terminal-pane", reviewerSurface: { mode: "tui", reviewId: "review-1", harness: "codex", handleId: "terminal-pane" }, reviews: [reviewState(3, "needs_review")] } }
      : common(path));
    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();
    await userEvent.click(await screen.findByRole("button", { name: "Review latest commit" }));
    await waitFor(() => expect(postCallsFor("/api/v1/sessions/{sessionId}/reviews/trigger")[0]?.[1]).toEqual({
      params: { path: { sessionId: "sess-1" } }, body: {},
    }));
  });

  it("shows the worker-compatible default reviewer before a run exists", async () => {
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/sessions/{sessionId}/reviews") {
        return { data: { reviewerHandleId: "", reviews: [] } };
      }
      if (path === "/api/v1/projects/{id}") {
        return {
          data: {
            status: "ok",
            project: {
              id: "ws-1",
              kind: "git",
              name: "my-app",
              path: "/repo",
              repo: "my-app",
              defaultBranch: "main",
              config: {},
            },
          },
        };
      }
      return { data: undefined };
    });

    renderWithQuery(
      <SessionInspector
        session={sessionWithProvider([pr(3, "open")], "codex")}
      />,
    );
    await openReviewsSection();

    expect(
      await screen.findByRole("button", { name: /Select reviewer agent/ }),
    ).toHaveTextContent("Codex");
    expect(screen.queryByText("reviewer")).not.toBeInTheDocument();
  });

  // The label is a display name, not the wire id: the trigger used to print the
  // raw harness id, which read as a second, selectable "claude-code" entry
  // alongside the catalog's properly-cased "Claude Code".
  it("labels the default reviewer with its display name, not the raw id", async () => {
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/agents/readiness") {
        const agents = ["claude-code", "codex", "kiro"].map((id) => agentReadiness(id));
        return { data: { agents } };
      }
      if (path === "/api/v1/sessions/{sessionId}/workspace/manifest") {
        return {
          data: { sessionId: "sess-1", files: [], truncated: false },
          error: undefined,
        };
      }
      if (path === "/api/v1/sessions/{sessionId}/reviews") {
        return { data: { reviewerHandleId: "", reviews: [] } };
      }
      if (path === "/api/v1/projects/{id}") {
        return {
          data: {
            status: "ok",
            project: {
              id: "ws-1",
              kind: "git",
              name: "my-app",
              path: "/repo",
              repo: "my-app",
              defaultBranch: "main",
              config: {},
            },
          },
        };
      }
      return { data: undefined };
    });

    renderWithQuery(
      <SessionInspector
        session={sessionWithProvider([pr(3, "open")], "claude-code")}
      />,
    );
    await openReviewsSection();

    const trigger = await screen.findByRole("button", {
      name: /Select reviewer agent/,
    });
    expect(trigger).toHaveTextContent("Claude Code");
    expect(trigger).not.toHaveTextContent("claude-code");
  });

  it("opens Harness from the reviewer menu for its unavailable selection", async () => {
    const responder = commonGetsResponder();
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/agents/readiness") {
        return {
          data: {
            agents: [
              agentReadiness("claude-code", "Claude Code"),
              agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
            ],
          },
        };
      }
      return responder(path);
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();
    await userEvent.click(await screen.findByRole("button", { name: /Select reviewer agent/ }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Manage agents…" }));
    await waitFor(() => expect(useUiStore.getState().settingsModal).not.toBeNull());

    expect(useUiStore.getState().settingsModal).toEqual({
      scope: "global",
      section: "harness",
      focusAgentId: "codex",
    });
  });

  it("configures session auto-review and disables manual controls", async () => {
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/agents/readiness") {
        const agents = ["claude-code", "codex", "kiro"].map((id) => agentReadiness(id));
        return { data: { agents } };
      }
      if (path === "/api/v1/sessions/{sessionId}/reviews") {
        return {
          data: {
            reviewerHandleId: "",
            reviews: [reviewState(3, "needs_review")],
          },
        };
      }
      if (path === "/api/v1/projects/{id}") {
        return {
          data: {
            status: "ok",
            project: {
              id: "ws-1",
              kind: "git",
              name: "my-app",
              path: "/repo",
              repo: "my-app",
              defaultBranch: "main",
              config: { reviewers: [{ harness: "codex" }] },
            },
          },
        };
      }
      return { data: undefined };
    });

    renderWithQuery(
      <SessionInspector
        session={session([pr(3, "open")], { autoReviewEnabled: true })}
      />,
    );
    await openReviewsSection();

    expect(
      screen.getByRole("button", { name: "Review latest commit" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Select reviewer agent" }),
    ).toBeDisabled();
    const toggle = screen.getByRole("switch", { name: "Auto review" });
    expect(toggle).toBeChecked();
    await userEvent.click(toggle);
    await waitFor(() =>
      expect(putMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/auto-review",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { enabled: false },
        },
      ),
    );
    expect(
      screen.queryByRole("button", { name: "Re-run review" }),
    ).not.toBeInTheDocument();
  });

  it("enables auto-review for the current session", async () => {
    mockCommonGets([], "", [reviewState(3, "needs_review")]);
    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    const toggle = screen.getByRole("switch", { name: "Auto review" });
    expect(toggle).not.toBeChecked();
    await userEvent.click(toggle);
    await waitFor(() =>
      expect(putMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/auto-review",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { enabled: true },
        },
      ),
    );
  });

  it.each(["chat", "tui"])("archives a %s reviewer and refreshes its conversation without changing review history", async (mode) => {
    const reviews = [reviewState(3, "up_to_date")];
    const common = commonGetsResponder([], "", reviews);
    getMock.mockImplementation(async (path: string) => path === "/api/v1/sessions/{sessionId}/reviews"
      ? { data: { reviewerHandleId: mode === "tui" ? "terminal-pane" : "", reviewerSurface: { mode, reviewId: "review-1", harness: "codex" }, reviews } }
      : common(path));
    postMock.mockResolvedValue({ data: { reviewerHandleId: "", reviews } });
    const { queryClient } = renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    await openReviewsSection();
    const stop = await screen.findByRole("button", { name: "Archive reviewer" });
    expect(stop).toBeEnabled();
    expect(stop.textContent).toBe("");
    await userEvent.hover(stop);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Archive reviewer");
    await userEvent.unhover(stop);
    await userEvent.click(stop);
    await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/kill", { params: { path: { sessionId: "sess-1" } } }));
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["reviewer-conversation", "review-1"] }));
    expect(queryClient.getQueryData<{ reviews: unknown[] }>(["session-reviews", "sess-1"])?.reviews).toEqual(reviews);
    expect(putMock).not.toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/auto-review", expect.anything());
  });

  it("shows a disabled Archive reviewer control before any reviewer exists", async () => {
    mockCommonGets([], "", []);
    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();
    expect(screen.getByRole("button", { name: "Archive reviewer" })).toBeDisabled();
  });

  it("removes the archived Chat surface while the reviews refresh is pending", async () => {
    const reviews = [reviewState(3, "up_to_date")];
    const reviewerSurface = { mode: "chat", reviewId: "review-1", harness: "codex" };
    const response = { data: { reviewerHandleId: "", reviewerSurface, reviews } };
    const common = commonGetsResponder([], "", reviews);
    let stopped = false;
    let finishRefresh!: (value: typeof response) => void;
    const refresh = new Promise<typeof response>((resolve) => { finishRefresh = resolve; });
    getMock.mockImplementation(async (path: string) => path === "/api/v1/sessions/{sessionId}/reviews"
      ? stopped ? refresh : response
      : common(path));
    postMock.mockImplementation(async () => { stopped = true; return { data: { reviewerHandleId: "", reviews } }; });
    const { queryClient } = renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();
    await userEvent.click(await screen.findByRole("button", { name: "Archive reviewer" }));
    await waitFor(() => expect(queryClient.getQueryState(["session-reviews", "sess-1"])?.fetchStatus).toBe("fetching"));
    await waitFor(() => expect(queryClient.getQueryData<{ reviewerSurface: unknown }>(["session-reviews", "sess-1"])?.reviewerSurface).toBeUndefined());
    expect(stopped).toBe(true);
    finishRefresh(response);
  });

  it("shows reviewing status and cancel action while auto-review is running", async () => {
    const runningReview = {
      ...approvedReview,
      status: "running",
      verdict: "",
      body: "",
    };
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/agents/readiness") {
        const agents = ["claude-code", "codex", "kiro"].map((id) => agentReadiness(id));
        return { data: { agents } };
      }
      if (path === "/api/v1/sessions/{sessionId}/reviews") {
        return {
          data: {
            reviewerHandleId: "reviewer-pane",
            reviewerActivityState: "active",
            reviews: [
              { ...reviewState(3, "running"), latestRun: runningReview },
            ],
          },
        };
      }
      if (path === "/api/v1/projects/{id}") {
        return {
          data: {
            status: "ok",
            project: {
              id: "ws-1",
              kind: "git",
              name: "my-app",
              path: "/repo",
              repo: "my-app",
              defaultBranch: "main",
              config: { reviewers: [{ harness: "codex" }] },
            },
          },
        };
      }
      return { data: undefined };
    });

    renderWithQuery(
      <SessionInspector
        session={session([pr(3, "open")], { autoReviewEnabled: true })}
      />,
    );
    await openReviewsSection();

    expect(
      screen.getByRole("button", { name: "Stop review" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Archive reviewer" }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: "Re-run review" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Run review" }),
    ).not.toBeInTheDocument();
  });

  it("hides review summary sections when no review data exists", async () => {
    mockCommonGets([], "", []);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(
      await screen.findByRole("button", { name: "Run review" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Tallpine code reviews")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Reviews on the pull request"),
    ).not.toBeInTheDocument();
  });

  it("hides Tallpine code reviews until a review run has been triggered", async () => {
    mockCommonGets([], "", [reviewState(3, "needs_review", "abc123")]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(
      await screen.findByRole("button", { name: "Review latest commit" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Tallpine code reviews")).not.toBeInTheDocument();
    expect(screen.queryByText("Reviewable change 3")).not.toBeInTheDocument();
  });

  it("shows Tallpine code reviews for verdict-only review states", async () => {
    mockCommonGets([], "reviewer-pane", [
      reviewState(3, "changes_requested", "abc123"),
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(await screen.findByText("Reviewable change 3")).toBeInTheDocument();
    expect(screen.getByText("Changes requested")).toBeInTheDocument();
  });

  it("hides agent review PR rows while a triggered review is still running without a verdict", async () => {
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
    await screen.findByText("Review in progress · Codex");

    expect(screen.queryByText("Reviewable change 3")).not.toBeInTheDocument();
    expect(screen.queryByText("Review summary")).not.toBeInTheDocument();
  });

  it("shows eligible and up-to-date open PR review rows", async () => {
    mockCommonGets([approvedReview], "reviewer-pane", [
      reviewState(3, "needs_review", "abc123"),
      reviewState(4, "up_to_date", "def456"),
      reviewState(5, "ineligible", "ghi789"),
    ]);

    renderWithQuery(
      <SessionInspector
        session={session([pr(3, "open"), pr(4, "open"), pr(5, "draft")])}
      />,
    );
    await openReviewsSection();

    expect(
      screen.getByRole("button", { name: /Select reviewer agent/ }),
    ).toHaveTextContent("Codex");
    expect(screen.queryByText("Reviewable change 3")).not.toBeInTheDocument();
    expect(await screen.findByText("Reviewable change 4")).toBeInTheDocument();
    expect(
      within(
        screen
          .getByText("Reviewable change 4")
          .closest("[data-testid='review-pr-row']") as HTMLElement,
      ).getAllByText("Approved"),
    ).not.toHaveLength(0);
    expect(screen.queryByText("Reviewable change 5")).not.toBeInTheDocument();
    expect(screen.getAllByText("Approved")).not.toHaveLength(0);
    expect(
      screen.getByRole("button", { name: "Review latest commit" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Open terminal" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Run" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Re-run" }),
    ).not.toBeInTheDocument();
  });

  // A review body is a multi-paragraph write-up. Rendered whole it buries the
  // verdict and every earlier pass below it, which is the opposite of reading
  // the history in one place.
  it("clamps a long review summary and expands it in place", async () => {
    const longBody = Array.from(
      { length: 12 },
      (_, i) => `Finding ${i + 1}: something worth reading.`,
    ).join("\n");
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "up_to_date", "abc123"),
        latestRun: { ...approvedReview, body: longBody },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    const summary = await screen.findByTestId("review-run-summary");
    expect(summary).toHaveClass("line-clamp-4");
    setRenderedOverflow(summary, true);

    await userEvent.click(await screen.findByRole("button", { name: "Show more" }));
    expect(screen.getByTestId("review-run-summary")).not.toHaveClass(
      "line-clamp-4",
    );

    await userEvent.click(screen.getByRole("button", { name: "Show less" }));
    expect(screen.getByTestId("review-run-summary")).toHaveClass(
      "line-clamp-4",
    );
  });

  // Reviews an agent asked for must be distinguishable from a person's click.
  it("labels an agent-requested review run", async () => {
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "up_to_date", "abc123"),
        latestRun: { ...approvedReview, triggerSource: "agent" },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(await screen.findByText(/Requested by agent/)).toBeInTheDocument();
  });

  // Several reviewers can run on one worker at once; each must be reachable.
  it("offers to open each of several live reviewers", async () => {
    const base = commonGetsResponder([], "claude-pane", [
      { ...reviewState(3, "running"), latestRun: { ...approvedReview, status: "running", verdict: "", body: "" } },
    ]);
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/sessions/{sessionId}/reviews") {
        return {
          data: {
            reviewerHandleId: "claude-pane",
            reviews: [{ ...reviewState(3, "running"), latestRun: { ...approvedReview, status: "running", verdict: "", body: "" } }],
            activeReviewers: [
              { mode: "tui", reviewId: "review-claude", harness: "claude-code", handleId: "claude-pane" },
              { mode: "tui", reviewId: "review-codex", harness: "codex", handleId: "codex-pane" },
            ],
          },
        };
      }
      return base(path);
    });
    const onOpenReviewerTerminal = vi.fn();

    renderWithQuery(
      <SessionInspector onOpenReviewerTerminal={onOpenReviewerTerminal} session={session([pr(3, "open")])} />,
    );
    await openReviewsSection();

    const openButtons = await screen.findAllByRole("button", { name: "Open" });
    expect(openButtons).toHaveLength(2);
    await userEvent.click(openButtons[1]!);
    expect(onOpenReviewerTerminal).toHaveBeenCalledWith({ handleId: "codex-pane", harness: "codex" });
  });

  it("does not list reviewers when only one is live", async () => {
    const base = commonGetsResponder([], "claude-pane", [reviewState(3, "up_to_date", "abc123")]);
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/sessions/{sessionId}/reviews") {
        return {
          data: {
            reviewerHandleId: "claude-pane",
            reviews: [reviewState(3, "up_to_date", "abc123")],
            reviewerSurface: { mode: "tui", reviewId: "review-claude", harness: "claude-code", handleId: "claude-pane" },
            activeReviewers: [{ mode: "tui", reviewId: "review-claude", harness: "claude-code", handleId: "claude-pane" }],
          },
        };
      }
      return base(path);
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    await screen.findByTestId("review-run-summary");
    expect(screen.queryByRole("button", { name: "Open" })).not.toBeInTheDocument();
  });

  // An agent can ask a reviewer other than the selected one. The selected
  // reviewer stays selected; the working one must still be reachable.
  it("offers to open a working reviewer that is not the selected one", async () => {
    const base = commonGetsResponder([], "", [
      { ...reviewState(3, "running"), latestRun: { ...approvedReview, status: "running", verdict: "", body: "" } },
    ]);
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/sessions/{sessionId}/reviews") {
        return {
          data: {
            reviewerHandleId: "",
            reviews: [{ ...reviewState(3, "running"), latestRun: { ...approvedReview, status: "running", verdict: "", body: "" } }],
            reviewerSurface: { mode: "tui", reviewId: "review-claude", harness: "claude-code" },
            activeReviewers: [{ mode: "tui", reviewId: "review-codex", harness: "codex", handleId: "codex-pane" }],
          },
        };
      }
      return base(path);
    });
    const onOpenReviewerTerminal = vi.fn();

    renderWithQuery(
      <SessionInspector onOpenReviewerTerminal={onOpenReviewerTerminal} session={session([pr(3, "open")])} />,
    );
    await openReviewsSection();

    await userEvent.click(await screen.findByRole("button", { name: "Open" }));
    expect(onOpenReviewerTerminal).toHaveBeenCalledWith({ handleId: "codex-pane", harness: "codex" });
  });

  // Nothing to hide, so offering to expand would be noise.
  it("does not offer to expand a short review summary", async () => {
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "up_to_date", "abc123"),
        latestRun: { ...approvedReview, body: "Looks good." },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    const summary = await screen.findByTestId("review-run-summary");
    setRenderedOverflow(summary, false);
    expect(summary).toHaveClass("line-clamp-4");
    expect(
      screen.queryByRole("button", { name: "Show more" }),
    ).not.toBeInTheDocument();
  });

  it("renders Tallpine review summaries as Markdown", async () => {
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "up_to_date", "abc123"),
        latestRun: {
          ...approvedReview,
          body: "Fix **auth validation**.\n\n- Add tests",
        },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    const summary = await screen.findByTestId("review-run-summary");
    expect(within(summary).getByText("auth validation").tagName).toBe("STRONG");
    expect(within(summary).getByText("Add tests").tagName).toBe("LI");
    expect(summary).not.toHaveTextContent("**auth validation**");
  });

  it("does not show a View on PR CTA for review summaries", async () => {
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "up_to_date", "abc123"),
        latestRun: { ...approvedReview, githubReviewId: "98765" },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(await screen.findByTestId("review-run-summary")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /View on PR/ })).not.toBeInTheDocument();
  });
});
