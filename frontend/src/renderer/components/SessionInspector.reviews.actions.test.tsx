import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import { useUiStore } from "../stores/ui-store";
import { pr, session, prSummary, renderWithQuery, approvedReview, reviewState, mockCommonGets, installInspectorLifecycle } from "../test/session-inspector-fixtures";
import { getMock, patchMock, postMock } from "../test/session-inspector-mocks";

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
  it("opens a Tallpine review in Browser and sends its summary to the worker", async () => {
    const reviewUrl = "https://github.com/acme/repo/pull/3#pullrequestreview-98765";
    const onWorkerMessageSent = vi.fn();
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "up_to_date", "abc123"),
        latestRun: {
          ...approvedReview,
          body: "Please tighten validation and add a regression test.",
          githubReviewId: "98765",
          prUrl: "https://github.com/acme/repo/pull/3",
        },
      },
    ]);

    renderWithQuery(
      <SessionInspector
        onWorkerMessageSent={onWorkerMessageSent}
        session={session([pr(3, "open")])}
      />,
    );
    await openReviewsSection();

    await userEvent.click(await screen.findByRole("button", { name: "Review actions" }));
    await userEvent.click(screen.getByRole("button", { name: "Open in Tallpine Browser" }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", {
        params: { path: { sessionId: "sess-1" } },
        body: { url: reviewUrl },
      }),
    );
    expect(useUiStore.getState().inspectorSessions["sess-1"]?.view).toBe("browser");
    expect(useUiStore.getState().inspectorSessions["sess-1"]?.isOpen).toBe(true);

    await userEvent.click(screen.getByRole("button", { name: "Send to worker agent" }));
    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
        params: { path: { sessionId: "sess-1" } },
        body: {
          userAuthored: true,
          message: expect.stringContaining("Review summary:\nPlease tighten validation and add a regression test."),
        },
      }),
    );
    expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
      params: { path: { sessionId: "sess-1" } },
      body: { message: expect.stringContaining(`Review URL: ${reviewUrl}`), userAuthored: true },
    });
    expect(onWorkerMessageSent).toHaveBeenCalledOnce();
  });

  it("shows inline comments on their exact Tallpine review pass without duplicating them externally", async () => {
    const onOpenReviewFile = vi.fn();
    const currentRun = {
      ...approvedReview,
      body: "Current Tallpine review.",
      githubReviewId: "111",
      prUrl: "https://example.com/pr/3",
    };
    const previousRun = {
      ...approvedReview,
      id: "run-previous",
      body: "Earlier Tallpine review.",
      githubReviewId: "222",
      prUrl: "https://example.com/pr/3",
      createdAt: "2026-06-15T10:06:00Z",
    };
    mockCommonGets([], "reviewer-pane", [{
      ...reviewState(3, "up_to_date", "abc123"),
      latestRun: currentRun,
      previousRun,
    }]);
    const previousGet = getMock.getMockImplementation()!;
    getMock.mockImplementation(async (path: string, opts?: unknown) => {
      if (path === "/api/v1/sessions/{sessionId}/pr") {
        return {
          data: {
            prs: [prSummary(3, "open", {
              author: "codebanditssss",
              review: {
                decision: "changes_requested",
                hasUnresolvedHumanComments: true,
                reviews: [],
                unresolvedBy: [{
                  reviewerId: "codebanditssss",
                  count: 2,
                  links: [
                    { reviewId: "111", body: "Current-pass comment.", file: "src/current.ts", line: 11, url: "https://example.com/current", autoInjectReview: false },
                    { reviewId: "222", body: "Earlier-pass comment.", file: "src/earlier.ts", line: 22, url: "https://example.com/earlier", autoInjectReview: true },
                  ],
                }, {
                  reviewerId: "maya",
                  count: 1,
                  links: [
                    { body: "Legacy external comment.", file: "src/legacy.ts", line: 33, url: "https://example.com/legacy", autoInjectReview: true },
                  ],
                }],
                resolvedBy: [{
                  reviewerId: "codebanditssss",
                  count: 1,
                  links: [{ reviewId: "111", body: "Resolved current-pass comment.", file: "src/current.ts", line: 9, url: "https://example.com/resolved", autoInjectReview: true }],
                }],
              },
            })],
          },
        };
      }
      return previousGet(path, opts);
    });

    renderWithQuery(
      <SessionInspector
        onOpenReviewFile={onOpenReviewFile}
        session={session([pr(3, "open")])}
      />,
    );
    await openReviewsSection();

    expect(await screen.findByText("Current-pass comment.")).toBeInTheDocument();
    expect(screen.queryByText("Earlier-pass comment.")).not.toBeInTheDocument();
    expect(screen.getByText("Resolved comments · 1")).toBeInTheDocument();

    await userEvent.click(screen.getAllByRole("button", { name: "Comment actions" })[0]!);
    await userEvent.click(screen.getByRole("button", { name: "Send to worker agent" }));
    expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", expect.objectContaining({
      params: { path: { sessionId: "sess-1" } },
      body: { message: expect.stringContaining("Current-pass comment."), userAuthored: true },
    }));
    await userEvent.click(screen.getAllByRole("button", { name: "Comment actions" })[0]!);
    await userEvent.click(screen.getByRole("button", { name: "View in file" }));
    expect(onOpenReviewFile).toHaveBeenCalledWith({ path: "src/current.ts", line: 11 });
    await userEvent.click(screen.getByRole("button", { name: "Resolve comment" }));
    await waitFor(() => expect(postMock).toHaveBeenCalledWith(
      "/api/v1/sessions/{sessionId}/reviews/comments/resolve",
      expect.objectContaining({ params: { path: { sessionId: "sess-1" } } }),
    ));

    await userEvent.click(screen.getByRole("button", { name: /Load more.*1 earlier/i }));
    expect(screen.getByText("Earlier-pass comment.")).toBeInTheDocument();
    expect(screen.getAllByText("Current-pass comment.")).toHaveLength(1);

    await userEvent.click(screen.getByRole("button", { name: /maya.*Commented/i }));
    expect(screen.getByText("Legacy external comment.")).toBeInTheDocument();
    expect(screen.getAllByText("Current-pass comment.")).toHaveLength(1);
    expect(screen.getAllByText("Earlier-pass comment.")).toHaveLength(1);
  });

  it("passes an inline review location to the Files diff viewer", async () => {
    const onOpenReviewFile = vi.fn();
    const previous = getMock.getMockImplementation()!;
    getMock.mockImplementation(async (path: string, opts?: unknown) => {
      if (path === "/api/v1/sessions/{sessionId}/pr") {
        return {
          data: {
            prs: [
              prSummary(3, "open", {
                review: {
                  decision: "changes_requested",
                  hasUnresolvedHumanComments: true,
                  reviews: [],
                  unresolvedBy: [{
                    reviewerId: "maya",
                    count: 1,
                    links: [{
                      body: "Guard this optional value.",
                      file: "src/panel.tsx",
                      line: 42,
                      url: "https://example.com/comment-42",
                      autoInjectReview: false,
                    }],
                  }],
                },
              }),
            ],
          },
        };
      }
      return previous(path, opts);
    });

    renderWithQuery(
      <SessionInspector
        onOpenReviewFile={onOpenReviewFile}
        session={session([pr(3, "open")])}
      />,
    );
    await openReviewsSection();
    await userEvent.click(await screen.findByRole("button", { name: /maya.*Commented/i }));
    await userEvent.click(screen.getByRole("button", { name: "Comment actions" }));
    await userEvent.click(screen.getByRole("button", { name: "View in file" }));

    expect(onOpenReviewFile).toHaveBeenCalledWith({ path: "src/panel.tsx", line: 42 });
  });

  it.each([
    [
      "needs_review",
      "changes_requested",
      "Review needed",
      "Review latest commit",
    ],
    ["cancelled", "approved", "Review needed", "Review latest commit"],
    ["running", "approved", "Reviewing...", "Stop review"],
  ] as const)(
    "keeps the current Tallpine review state clear while the current head is %s",
    async (status, previousVerdict, runLabel, actionLabel) => {
      const current = {
        ...reviewState(
          3,
          status === "cancelled" ? "needs_review" : status,
          "sha-current",
        ),
        previousRun: {
          ...approvedReview,
          id: "run-previous",
          status: "delivered",
          verdict: previousVerdict,
          body: "Previous review summary with actionable detail.",
          githubReviewId: "98765",
          targetSha: "sha-previous",
        },
      };
      if (status === "running" || status === "cancelled") {
        current.latestRun = {
          ...approvedReview,
          id: "run-current",
          status,
          verdict: "",
          targetSha: "sha-current",
        };
      }
      mockCommonGets([], "reviewer-pane", [current]);

      renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
      await openReviewsSection();

      expect(await screen.findAllByText(runLabel)).not.toHaveLength(0);
      expect(
        screen.getByText("Previous review summary with actionable detail."),
      ).toBeInTheDocument();
      expect(screen.queryByText(/Previous:/)).not.toBeInTheDocument();
      if (status === "needs_review" || status === "cancelled") {
        expect(
          screen.getByText(
            status === "cancelled" ? "Approved" : "Changes requested",
          ),
        ).toBeInTheDocument();
        expect(screen.getByText("Earlier commit")).toBeInTheDocument();
        expect(
          screen.getByText("#3 · Latest commit has not been reviewed"),
        ).toBeInTheDocument();
      } else {
        expect(screen.queryByText("Changes requested")).not.toBeInTheDocument();
        expect(screen.queryByText("Earlier commit")).not.toBeInTheDocument();
      }
      expect(screen.queryByRole("link", { name: "View on PR" })).not.toBeInTheDocument();
      // A run in flight gets its own live strip naming the harness, not just a
      // word on the button.
      if (status === "running") {
        expect(
          screen.getByText("Review in progress · Codex"),
        ).toBeInTheDocument();
      } else {
        expect(
          screen.queryByText(/is reviewing this change/),
        ).not.toBeInTheDocument();
      }
      expect(
        screen.getByRole("button", { name: actionLabel }),
      ).toBeInTheDocument();
    },
  );

  it("shows PRs with unresolved comments but no decisive review", async () => {
    mockCommonGets([], "reviewer-pane", [
      reviewState(3, "up_to_date", "sha-1"),
    ]);
    const previous = getMock.getMockImplementation()!;
    getMock.mockImplementation(async (path: string, opts?: unknown) => {
      if (path === "/api/v1/sessions/{sessionId}/pr") {
        return {
          data: {
            prs: [
              {
                number: 3,
                title: "Reviewable change 3",
                url: "https://example.com/pr/3",
                htmlUrl: "https://example.com/pr/3",
                state: "open",
                author: "ada",
                ci: {
                  state: "passing",
                  failingChecks: [],
                  prUrl: "https://example.com/pr/3",
                },
                mergeability: {
                  state: "mergeable",
                  reasons: [],
                  prUrl: "https://example.com/pr/3",
                  conflictFiles: [],
                },
                review: {
                  decision: "changes_requested",
                  hasUnresolvedHumanComments: true,
                  reviews: [],
                  unresolvedBy: [
                    {
                      reviewerId: "maya",
                      count: 2,
                      links: [
                        { file: "a.ts", line: 3, autoInjectReview: true },
                        { file: "a.ts", line: 9, autoInjectReview: true },
                      ],
                    },
                  ],
                },
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
      await screen.findByRole("button", { name: "Re-run review" }),
    ).toBeInTheDocument();
    expect(
      (await screen.findAllByText("Reviewable change 3")).length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText(/2 unresolved comments/).length).toBeGreaterThanOrEqual(
      1,
    );
    expect(
      screen.queryByTestId("github-inline-comments"),
    ).not.toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /maya.*Commented/i }),
    );
    const comments = screen.getByTestId("github-inline-comments");
    expect(comments).toHaveTextContent("Open comments · 2");
    expect(comments).not.toHaveTextContent("maya");
    expect(
      within(comments).getAllByRole("status", { name: "Sent to worker agent" }),
    ).toHaveLength(2);
    expect(comments).toHaveTextContent("a.ts:3");
    expect(comments).toHaveTextContent("a.ts:9");
    // Tallpine's runs and the PR's own reviews share one section keyed by PR, so the
    // unresolved count rides the same row as the Tallpine verdict.
    expect(screen.getByText("Review summary")).toBeInTheDocument();
    expect(
      screen.queryByText("Reviews on the pull request"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Tallpine code reviews")).not.toBeInTheDocument();
    expect(
      screen.queryByText("No unresolved threads."),
    ).not.toBeInTheDocument();
  });

  it("renders PR review summaries as Markdown", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date("2026-06-19T11:00:00Z"));
    mockCommonGets([], "reviewer-pane", [
      reviewState(3, "up_to_date", "sha-1"),
    ]);
    const previous = getMock.getMockImplementation()!;
    getMock.mockImplementation(async (path: string, opts?: unknown) => {
      if (path === "/api/v1/sessions/{sessionId}/pr") {
        return {
          data: {
            prs: [
              {
                number: 3,
                title: "Reviewable change 3",
                url: "https://example.com/pr/3",
                htmlUrl: "https://example.com/pr/3",
                state: "open",
                author: "ada",
                ci: {
                  state: "passing",
                  failingChecks: [],
                  prUrl: "https://example.com/pr/3",
                },
                mergeability: {
                  state: "mergeable",
                  reasons: [],
                  prUrl: "https://example.com/pr/3",
                  conflictFiles: [],
                },
                review: {
                  decision: "approved",
                  hasUnresolvedHumanComments: false,
                  reviews: [
                    {
                      reviewerId: "maya",
                      verdict: "approved",
                      submittedAt: "2026-06-16T11:00:00Z",
                      body: "Looks **ready**.\n\n1. Ship it",
                      reviewUrl:
                        "https://example.com/pr/3#pullrequestreview-456",
                      autoInjectReview: true,
                    },
                    {
                      reviewerId: "ada",
                      verdict: "changes_requested",
                      submittedAt: "2026-06-16T12:00:00Z",
                      body: "Self review should stay hidden.",
                      reviewUrl:
                        "https://example.com/pr/3#pullrequestreview-789",
                      autoInjectReview: true,
                    },
                  ],
                  unresolvedBy: [
                    {
                      reviewerId: "ada",
                      count: 1,
                      links: [
                        {
                          body: "Self comment should stay hidden.",
                          file: "self.ts",
                          line: 1,
                          autoInjectReview: true,
                        },
                      ],
                    },
                  ],
                },
              },
            ],
          },
        };
      }
      return previous(path, opts);
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    const summary = await screen.findByTestId("github-review-summary");
    const externalReview = summary.closest("article") as HTMLElement;
    expect(screen.queryByRole("button", { name: /ada/i })).not.toBeInTheDocument();
    expect(screen.queryByText("Self review should stay hidden.")).not.toBeInTheDocument();
    expect(screen.queryByText("Self comment should stay hidden.")).not.toBeInTheDocument();
    expect(summary).toHaveClass("select-text");
    expect(within(summary).getByText("ready").tagName).toBe("STRONG");
    expect(within(summary).getByText("Ship it").tagName).toBe("LI");
    expect(summary).not.toHaveTextContent("**ready**");
    expect(
      within(externalReview).getByText("Reviewed 3d ago"),
    ).toBeInTheDocument();
    expect(
      within(externalReview).queryByRole("link", { name: "View on PR" }),
    ).not.toBeInTheDocument();
    expect(
      within(externalReview).queryByRole("button", { name: "Request to re-review PR" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("External reviews")).toBeInTheDocument();
  });

  it("requests PR re-review from the external review actions menu", async () => {
    const previous = getMock.getMockImplementation()!;
    getMock.mockImplementation(async (path: string, opts?: unknown) => {
      if (path === "/api/v1/sessions/{sessionId}/pr") {
        return {
          data: {
            prs: [
              prSummary(3, "open", {
                review: {
                  decision: "changes_requested",
                  hasUnresolvedHumanComments: false,
                  reviews: [
                    {
                      reviewerId: "maya",
                      verdict: "changes_requested",
                      submittedAt: "2026-06-16T11:00:00Z",
                      reviewUrl:
                        "https://example.com/pr/3#pullrequestreview-456",
                      body: "Please request another look after the fixes.",
                      autoInjectReview: false,
                    },
                  ],
                  unresolvedBy: [],
                },
              }),
            ],
          },
        };
      }
      return previous(path, opts);
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();
    expect(screen.getByText("Please request another look after the fixes.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Review actions" }));
    await userEvent.click(screen.getByRole("button", { name: "Request to re-review PR" }));
    await waitFor(() => expect(postMock).toHaveBeenCalledWith(
      "/api/v1/sessions/{sessionId}/reviews/rerequest",
      {
        params: { path: { sessionId: "sess-1" } },
        body: {
          pullRequestUrl: "https://api.github.com/repos/acme/repo/pulls/3",
          reviewerId: "maya",
        },
      },
    ));
    expect(
      screen.getByRole("button", { name: "Asked for re-review" }),
    ).toBeDisabled();
  });

  it("marks SCM reviews and individual comments using their stored injection decision", async () => {
    const previous = getMock.getMockImplementation()!;
    getMock.mockImplementation(async (path: string, opts?: unknown) => {
      if (path === "/api/v1/sessions/{sessionId}/pr") {
        return {
          data: {
            prs: [
              prSummary(3, "open", {
                review: {
                  decision: "changes_requested",
                  hasUnresolvedHumanComments: true,
                  reviews: [
                    {
                      reviewerId: "maya",
                      verdict: "changes_requested",
                      submittedAt: "2026-06-16T11:00:00Z",
                      autoInjectReview: true,
                    },
                  ],
                  unresolvedBy: [
                    {
                      reviewerId: "maya",
                      count: 2,
                      links: [
                        { file: "a.ts", line: 3, autoInjectReview: true },
                        {
                          file: "a.ts",
                          line: 9,
                          url: "https://example.com/comment-9",
                          autoInjectReview: false,
                        },
                      ],
                    },
                  ],
                },
              }),
            ],
          },
        };
      }
      return previous(path, opts);
    });

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(screen.queryByText("Not injected")).not.toBeInTheDocument();
    expect(screen.getByText("External reviews")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Send to worker agent" }),
    ).not.toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /maya.*Changes requested/i }),
    );
    await userEvent.click(
      screen.getAllByRole("button", { name: "Comment actions" })[1]!,
    );
    const sendButton = screen.getByRole("button", {
      name: "Send to worker agent",
    });
    expect(sendButton).toBeEnabled();
    await userEvent.click(sendButton);
    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/send",
        {
          params: { path: { sessionId: "sess-1" } },
          body: {
            userAuthored: true,
            message: expect.stringContaining("Location: a.ts:9"),
          },
        },
      ),
    );
    expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
      params: { path: { sessionId: "sess-1" } },
      body: {
        userAuthored: true,
        message: expect.stringContaining(
          "commit the fix, and push the branch to GitHub",
        ),
      },
    });
    expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
      params: { path: { sessionId: "sess-1" } },
      body: {
        userAuthored: true,
        message: expect.stringContaining("Reviewer: @maya"),
      },
    });
    expect(
      screen.queryByRole("button", { name: "Resolve comment" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getAllByRole("status", { name: "Sent to worker agent" }),
    ).toHaveLength(2);
  });

  it("marks a Tallpine review using its stored injection decision", async () => {
    mockCommonGets([], "reviewer-pane", [
      {
        ...reviewState(3, "up_to_date", "abc123"),
        latestRun: { ...approvedReview, autoInjectReview: false },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await openReviewsSection();

    expect(screen.queryByText("Not injected")).not.toBeInTheDocument();
    expect(screen.getByText("Agent reviews")).toBeInTheDocument();
  });

  it("persists the automatic review injection toggle", async () => {
    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);

    const toggle = screen.getByRole("switch", {
      name: "Automatically fix review comments",
    });
    expect(toggle).toBeChecked();
    await userEvent.click(toggle);

    await waitFor(() =>
      expect(patchMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/auto-inject-review",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { autoInjectReview: false },
        },
      ),
    );
  });
});
