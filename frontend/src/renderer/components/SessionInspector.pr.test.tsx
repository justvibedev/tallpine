import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { pr, session, prSummary, seedPRSummaries, renderWithQuery, commonGetsResponder, installInspectorLifecycle } from "../test/session-inspector-fixtures";
import { getMock, patchMock, postMock } from "../test/session-inspector-mocks";

vi.mock("@tanstack/react-router", () => import("../test/session-inspector-mocks").then((m) => m.routerModule()));
vi.mock("../lib/preview-mode", () => import("../test/session-inspector-mocks").then((m) => m.previewModeModule()));
vi.mock("../lib/api-client", () => import("../test/session-inspector-mocks").then((m) => m.apiClientModule()));
vi.mock("../lib/host-clients", async (importOriginal) =>
  (await import("../test/session-inspector-mocks")).hostClientsModule(await importOriginal<typeof import("../lib/host-clients")>()),
);

installInspectorLifecycle();

describe("SessionInspector PR section", () => {
  // Scope assertions to the PR section so the card order is explicit.
  const prSection = (title: string) =>
    within(
      screen
        .getByText(title)
        .closest("[data-testid='inspector-section']") as HTMLElement,
    );

  it("shows a reported external PR from one SCM request without tracked actions", async () => {
    const respond = commonGetsResponder();
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/projects/{id}") {
        return { data: { status: "ok", project: { id: "ws-1", repo: "https://gitlab.com/my-app/backend", config: {} } } };
      }
      return path === "/api/v1/sessions/{sessionId}/pr"
        ? { data: { prs: [], linkedPrs: [{
          url: "https://gitlab.com/release/notes/-/merge_requests/9",
          provider: "gitlab",
          host: "gitlab.com",
          repo: "release/notes",
          number: 9,
        }] } }
        : respond(path);
    });
    renderWithQuery(<SessionInspector session={session([])} />, undefined, (client) => {
      client.setQueryData(["project", "ws-1"], { repo: "https://gitlab.com/my-app/backend", config: {} });
    });

    await screen.findByRole("link", { name: /gitlab.com\/release\/notes MR #9/ });

    const section = prSection("Pull request");
    expect(section.getByRole("link", { name: /gitlab.com\/release\/notes MR #9/ })).toHaveAttribute("href", "https://gitlab.com/release/notes/-/merge_requests/9");
    expect(section.getByText("Reported by worker")).toBeInTheDocument();
    expect(section.getByText("External repository")).toBeInTheDocument();
    expect(section.getByRole("button", { name: /Linked for reference.*Tallpine does not track checks/ })).toBeInTheDocument();
    expect(section.getByRole("button", { name: "Copy link" })).toBeInTheDocument();
    expect(section.queryByRole("button", { name: /^Merge PR #/ })).not.toBeInTheDocument();
    expect(section.queryByText("No pull request opened yet.")).not.toBeInTheDocument();
    expect(getMock.mock.calls.filter(([path]) => path === "/api/v1/sessions/{sessionId}/pr")).toHaveLength(1);
  });

  it("does not label a reported PR from the project repository as external", async () => {
    const respond = commonGetsResponder();
    getMock.mockImplementation(async (path: string) =>
      path === "/api/v1/sessions/{sessionId}/pr"
        ? { data: { prs: [], linkedPrs: [{
          url: "https://github.com/acme/repo/pull/9",
          provider: "github",
          host: "github.com",
          repo: "acme/repo",
          number: 9,
        }] } }
        : respond(path),
    );
    renderWithQuery(<SessionInspector session={session([])} />, undefined, (client) => {
      client.setQueryData(["project", "ws-1"], { repo: "git@github.com:acme/repo.git", config: {} });
    });

    await screen.findByRole("link", { name: /github.com\/acme\/repo PR #9/ });
    expect(prSection("Pull request").queryByText("External repository")).not.toBeInTheDocument();
  });

  it("renders one card per PR, ordered actionable-first, when a session owns a stack", () => {
    renderWithQuery(
      <SessionInspector
        session={session([pr(40, "merged"), pr(41, "open"), pr(42, "draft")])}
      />,
    );

    expect(screen.getByText("Pull requests (3)")).toBeInTheDocument();
    const cards = prSection("Pull requests (3)")
      .getAllByText(/^PR #\d+$/)
      .map((el) => el.textContent);
    // open (41), draft (42), merged (40)
    expect(cards).toEqual(["PR #41", "PR #42", "PR #40"]);
  });

  it("uses the singular heading and shows enriched facts for a single PR", () => {
    renderWithQuery(
      <SessionInspector session={session([pr(7, "open")])} />,
      undefined,
      (client) => {
        seedPRSummaries(client, [
          prSummary(7, "open", {
            review: {
              decision: "approved",
              hasUnresolvedHumanComments: false,
              unresolvedBy: [],
            },
          }),
        ]);
      },
    );

    expect(screen.getByText("Pull request")).toBeInTheDocument();
    expect(screen.queryByText(/Pull requests \(/)).not.toBeInTheDocument();
    expect(prSection("Pull request").getByText("PR #7")).toBeInTheDocument();
    expect(
      prSection("Pull request").getByText("Mergeable"),
    ).toBeInTheDocument();
    expect(prSection("Pull request").getByText("PR approved")).toBeInTheDocument();
    expect(
      prSection("Pull request").getByText("Checks passing"),
    ).toBeInTheDocument();
    expect(
      prSection("Pull request").getByRole("link", { name: "PR 7" }),
    ).toHaveClass("text-sm");
    expect(
      prSection("Pull request").getByRole("link", { name: "PR 7" }),
    ).toHaveAttribute("href", "https://github.com/acme/repo/pull/7");
    expect(prSection("Pull request").getByText("open")).toHaveClass(
      "text-[9px]",
      "leading-none",
    );
    expect(
      prSection("Pull request").getByRole("button", { name: "Merge PR #7" }),
    ).toBeInTheDocument();
  });

  it("merges a ready pull request directly through the daemon", async () => {
    postMock.mockImplementation(async (path: string) => path === "/api/v1/prs/{id}/merge" ? {
      data: {
        ok: true,
        prNumber: 7,
        method: "squash",
        state: "merged",
        stateChangedAt: "2026-10-19T11:06:22Z",
      },
      error: undefined,
    } : {
      data: { ok: true, sessionId: "sess-1" },
      error: undefined,
    });
    const readyPR = prSummary(7, "open", {
      url: "https://example.com/pr/7",
      headSha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      review: {
        decision: "approved",
        hasUnresolvedHumanComments: false,
        unresolvedBy: [],
      },
    });
    const respond = commonGetsResponder();
    getMock.mockImplementation(async (path: string) => path === "/api/v1/sessions/{sessionId}/pr"
      ? { data: { prs: [readyPR], linkedPrs: [] } }
      : respond(path));
    const { queryClient } = renderWithQuery(
      <SessionInspector session={session([pr(7, "open")])} />,
      undefined,
      (client) => {
        seedPRSummaries(client, [readyPR]);
      },
    );

    const mergeButton = screen.getByRole("button", { name: "Merge PR #7" });
    expect(mergeButton).toBeEnabled();
    fireEvent.click(mergeButton);

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith("/api/v1/prs/{id}/merge", {
        params: { path: { id: "7" } },
        body: {
          prUrl: "https://example.com/pr/7",
          expectedHeadSha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        },
      }),
    );
    await waitFor(() =>
      expect(queryClient.getQueryData<{ prs: SessionPRSummary[] }>(sessionScmSummaryQueryKey("sess-1"))?.prs[0]?.state).toBe("merged"),
    );
    await waitFor(() =>
      expect(prSection("Pull request").getByText("merged")).toBeInTheDocument(),
    );
    expect(prSection("Pull request").queryByRole("button", { name: "Merge PR #7" })).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });


  it("does not offer Merge when the pull request is not ready", () => {
    renderWithQuery(
      <SessionInspector
        session={session([
          pr(7, "open", {
            ci: "failing",
            mergeability: "blocked",
          }),
        ])}
      />,
    );

    expect(
      screen.queryByRole("button", { name: "Merge PR #7" }),
    ).not.toBeInTheDocument();
  });

  it.each(["unknown", "blocked", "unstable"] as const)(
    "does not offer Merge when provider mergeability is %s",
    (mergeability) => {
      renderWithQuery(
        <SessionInspector session={session([pr(7, "open")])} />,
        undefined,
        (client) => {
          seedPRSummaries(client, [
            prSummary(7, "open", {
              ci: { autoInjectCI: true, state: "passing", failingChecks: [] },
              review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] },
              mergeability: { state: mergeability, reasons: [], prUrl: "https://example.com/pr/7" },
            }),
          ]);
        },
      );
      expect(screen.queryByRole("button", { name: "Merge PR #7" })).not.toBeInTheDocument();
    },
  );

  it("does not offer Merge without a current head SHA", () => {
    renderWithQuery(
      <SessionInspector session={session([pr(7, "open")])} />,
      undefined,
      (client) => {
        seedPRSummaries(client, [
          prSummary(7, "open", {
            headSha: "",
            ci: { autoInjectCI: true, state: "passing", failingChecks: [] },
            review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] },
            mergeability: { state: "mergeable", reasons: [], prUrl: "https://example.com/pr/7" },
          }),
        ]);
      },
    );
    expect(screen.queryByRole("button", { name: "Merge PR #7" })).not.toBeInTheDocument();
  });

  it("offers Merge when the provider requires no review", () => {
    renderWithQuery(
      <SessionInspector session={session([pr(7, "open", { review: "none" })])} />,
      undefined,
      (client) => {
        seedPRSummaries(client, [prSummary(7, "open")]);
      },
    );

    expect(screen.getByRole("button", { name: "Merge PR #7" })).toBeEnabled();
    expect(prSection("Pull request").getByText("No review required")).toBeInTheDocument();
    expect(prSection("Pull request").queryByText("Review pending")).not.toBeInTheDocument();
  });

  it.each(["review_required", "changes_requested"] as const)(
    "does not offer Merge when the review decision is %s",
    (decision) => {
      renderWithQuery(
        <SessionInspector session={session([pr(7, "open")])} />,
        undefined,
        (client) => {
          seedPRSummaries(client, [
            prSummary(7, "open", {
              review: { decision, hasUnresolvedHumanComments: false, unresolvedBy: [] },
            }),
          ]);
        },
      );
      expect(screen.queryByRole("button", { name: "Merge PR #7" })).not.toBeInTheDocument();
    },
  );

  it("does not offer Merge while human review comments are unresolved", () => {
    renderWithQuery(
      <SessionInspector session={session([pr(7, "open")])} />,
      undefined,
      (client) => {
        seedPRSummaries(client, [
          prSummary(7, "open", {
            review: { decision: "none", hasUnresolvedHumanComments: true, unresolvedBy: [] },
          }),
        ]);
      },
    );
    expect(screen.queryByRole("button", { name: "Merge PR #7" })).not.toBeInTheDocument();
  });

  it("uses the state chip as the single merged-state indicator", () => {
    renderWithQuery(
      <SessionInspector
        session={session([pr(7, "merged")], { status: "merged" })}
      />,
    );

    const card = prSection("Pull request")
      .getByText("PR #7")
      .closest("article") as HTMLElement;
    expect(within(card).getByText("merged", { exact: true })).toHaveClass(
      "border-border-strong",
      "bg-overlay",
      "text-success",
    );
    expect(
      within(card).queryByText("Pull request merged"),
    ).not.toBeInTheDocument();
  });

  it("omits PR cards and PR policies until a PR is known, even for PR output", () => {
    renderWithQuery(<SessionInspector session={session([], { outputType: "pr" })} />);
    expect(screen.queryByText(/PR #\d+/)).not.toBeInTheDocument();
    expect(screen.queryByText("No pull request opened yet.")).not.toBeInTheDocument();
    expect(screen.queryByText("Session controls")).not.toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Automatically fix CI failures" })).not.toBeInTheDocument();
  });

  it("titles the branch block Branch without a PR and Pull request with one", () => {
    const { unmount } = renderWithQuery(<SessionInspector session={session([], { outputType: "none" })} />);
    expect(screen.getByRole("button", { name: "Branch" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.queryByText("Pull request")).not.toBeInTheDocument();
    unmount();

    renderWithQuery(<SessionInspector session={session([pr(7, "open")], { outputType: "none" })} />);
    expect(screen.getByRole("button", { name: "Pull request" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Branch" })).not.toBeInTheDocument();
  });

  it("hides the section entirely when the session has no known output type", () => {
    renderWithQuery(<SessionInspector session={session([], { outputType: undefined })} />);
    expect(
      screen.queryByText("No pull request opened yet."),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Pull request")).not.toBeInTheDocument();
  });

  it("hides the section entirely when the session's output type is explicitly none", () => {
    renderWithQuery(<SessionInspector session={session([], { outputType: "none" })} />);
    expect(
      screen.queryByText("No pull request opened yet."),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Pull request")).not.toBeInTheDocument();
  });

  it("keeps durable session policies in Summary and operational review controls in Reviews", async () => {
    renderWithQuery(<SessionInspector session={session([pr(7, "open")])} />);

    expect(screen.getByText("Session controls")).toBeInTheDocument();

    const policyRow = (name: string) =>
      screen
        .getByRole("switch", { name })
        .closest("[data-slot='inspector-policy-row']") as HTMLElement;
    const ciRow = policyRow("Automatically fix CI failures");
    const reviewRow = policyRow("Automatically fix review comments");
    const terminateRow = policyRow(
      "Archive session when pull requests merge",
    );
    const prCard = prSection("Pull request")
      .getByText("PR #7")
      .closest("article") as HTMLElement;
    const appearsBefore = (first: HTMLElement, second: HTMLElement) =>
      Boolean(
        first.compareDocumentPosition(second) &
        Node.DOCUMENT_POSITION_FOLLOWING,
      );

    expect(appearsBefore(prCard, ciRow)).toBe(true);
    expect(ciRow.className).toBe(terminateRow.className);
    expect(reviewRow.className).toBe(terminateRow.className);
    expect(ciRow.parentElement).not.toHaveClass(
      "rounded-lg",
      "border",
      "bg-surface",
    );


    for (const name of [
      "Automatically fix CI failures",
      "Automatically fix review comments",
      "Archive session when pull requests merge",
    ]) {
      const toggle = screen.getByRole("switch", { name });
      expect(toggle).toHaveClass("h-4", "w-8", "rounded-full");
      expect(toggle.querySelector("[data-slot='switch-thumb']")).toHaveClass(
        "size-3.5",
        "rounded-full",
      );
    }
    expect(
      screen.getByRole("button", {
        name: "Sends CI failures to the worker for this session's PRs.",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "When disabled, Tallpine keeps this session open after all pull requests merge.",
      }),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "Reviews" }));
    expect(
      await screen.findByRole("button", { name: "Run review" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("switch", { name: "Automatically fix review comments" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Auto review" })).toBeInTheDocument();

  });

  it("persists the CI injection policy", async () => {
    renderWithQuery(<SessionInspector session={session([pr(7, "open")])} />);

    const toggle = screen.getByRole("switch", {
      name: "Automatically fix CI failures",
    });
    expect(toggle).toBeChecked();
    await userEvent.click(toggle);

    expect(toggle).not.toBeChecked();
    await waitFor(() =>
      expect(patchMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/auto-inject-ci",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { autoInjectCI: false },
        },
      ),
    );
  });

  it("restores the CI injection toggle and shows the API error when saving fails", async () => {
    patchMock.mockResolvedValueOnce({
      error: new Error("CI policy unavailable"),
      response: { status: 500 },
    });
    renderWithQuery(<SessionInspector session={session([pr(7, "open")])} />);

    const toggle = screen.getByRole("switch", {
      name: "Automatically fix CI failures",
    });
    await userEvent.click(toggle);

    expect(
      await screen.findByText("CI policy unavailable"),
    ).toBeInTheDocument();
    expect(toggle).toBeChecked();
  });

  it("shows failing checks without an injection notice", () => {
    const failingPR = prSummary(7, "open", {
      ci: {
        autoInjectCI: false,
        state: "failing",
        failingChecks: [
          {
            name: "unit",
            status: "failed",
            conclusion: "failure",
            url: "https://ci.example/unit",
          },
        ],
      },
      mergeability: {
        state: "blocked",
        reasons: ["required checks failing"],
        prUrl: "https://example.com/pr/7",
      },
    });
    renderWithQuery(
      <SessionInspector session={session([pr(7, "open")])} />,
      undefined,
      (client) => {
        seedPRSummaries(client, [failingPR]);
      },
    );

    const card = prSection("Pull request")
      .getByText("PR #7")
      .closest("article") as HTMLElement;
    expect(within(card).getByText("Checks failing")).toBeInTheDocument();
    expect(within(card).queryByText("CI failures not injected")).not.toBeInTheDocument();
  });

  it("links each PR to its url", () => {
    renderWithQuery(
      <SessionInspector session={session([pr(41, "open"), pr(42, "draft")])} />,
    );
    const links = [
      prSection("Pull requests (2)").getByRole("link", { name: "Open PR #41" }),
      prSection("Pull requests (2)").getByRole("link", { name: "Open PR #42" }),
    ];
    expect(links[0]).toHaveClass(
      "text-settings-label",
      "hover:text-settings-label",
    );
    expect(links.map((a) => a.getAttribute("href"))).toEqual([
      "https://example.com/pr/41",
      "https://example.com/pr/42",
    ]);
  });
});
