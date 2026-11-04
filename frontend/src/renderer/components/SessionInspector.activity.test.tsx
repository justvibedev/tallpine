import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import { sessionInterfaceTransitionQueryKey } from "../hooks/useSessionInterfaceTransition";
import { sessionInterfaceTransitionStatus } from "../test/interface-transition-fixtures";
import { pr, session, prSummary, seedPRSummaries, renderWithQuery, commonGetsResponder, installInspectorLifecycle } from "../test/session-inspector-fixtures";
import { getMock, postMock } from "../test/session-inspector-mocks";

vi.mock("@tanstack/react-router", () => import("../test/session-inspector-mocks").then((m) => m.routerModule()));
vi.mock("../lib/preview-mode", () => import("../test/session-inspector-mocks").then((m) => m.previewModeModule()));
vi.mock("../lib/api-client", () => import("../test/session-inspector-mocks").then((m) => m.apiClientModule()));
vi.mock("../lib/host-clients", async (importOriginal) =>
  (await import("../test/session-inspector-mocks")).hostClientsModule(await importOriginal<typeof import("../lib/host-clients")>()),
);

installInspectorLifecycle();

describe("SessionInspector Activity section", () => {
  const activitySectionElement = () =>
    screen
      .getByText("Activity")
      .closest("[data-testid='inspector-section']") as HTMLElement;
  const activitySection = () => within(activitySectionElement());

  it("offers a managed resume only for an exited, nonterminated agent", async () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "exited",
          activity: { state: "exited", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    await userEvent.click(
      activitySection().getByRole("button", { name: "Resume agent" }),
    );

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/resume-agent",
        {
          params: { path: { sessionId: "sess-1" } },
        },
      ),
    );
  });

  it("does not offer agent resume for a live or terminated session", () => {
    const live = renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "idle",
          activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    expect(
      screen.queryByRole("button", { name: "Resume agent" }),
    ).not.toBeInTheDocument();
    expect(
      activitySectionElement().querySelector(".mt-3.border-t.pt-3:not([data-testid=session-account-summary])"),
    ).not.toBeInTheDocument();

    live.unmount();
    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "terminated",
          isTerminated: true,
          activity: { state: "exited", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );
    expect(
      screen.queryByRole("button", { name: "Resume agent" }),
    ).not.toBeInTheDocument();
  });

  it("does not offer agent resume during an interface transition", () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "exited",
          activity: { state: "exited", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
      undefined,
      (client) => {
        client.setQueryData(
          sessionInterfaceTransitionQueryKey("sess-1"),
          sessionInterfaceTransitionStatus("sess-1"),
        );
      },
    );

    expect(
      screen.queryByRole("button", { name: "Resume agent" }),
    ).not.toBeInTheDocument();
    expect(
      activitySectionElement().querySelector(".mt-3.border-t.pt-3:not([data-testid=session-account-summary])"),
    ).not.toBeInTheDocument();
  });

  it("keeps resume failures visible beside the action", async () => {
    postMock.mockResolvedValueOnce({
      error: new Error("agent restart failed"),
      response: { status: 500 },
    });
    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "exited",
          activity: { state: "exited", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    await userEvent.click(
      activitySection().getByRole("button", { name: "Resume agent" }),
    );

    expect(
      await activitySection().findByText("agent restart failed"),
    ).toBeInTheDocument();
  });

  it.each([
    ["idle", "Idle"],
    ["active", "Working"],
    ["waiting_input", "Input Needed"],
    ["exited", "Exited"],
  ] as const)("renders %s from raw session activity", (state, label) => {
    renderWithQuery(
      <SessionInspector
        session={session([pr(7, "open")], {
          status: "review_pending",
          activity: { state, lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    expect(activitySection().getByText(label)).toBeInTheDocument();
  });

  it("renders unknown activity through the shared activity label", () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "working",
          activity: {
            state: "unknown",
            lastActivityAt: "2026-06-15T10:00:00Z",
          },
        })}
      />,
    );

    expect(activitySection().getByText("Unknown")).toBeInTheDocument();
    expect(
      activitySection().queryByText("Activity Unavailable"),
    ).not.toBeInTheDocument();
  });

  it("falls back to unknown when no activity has been reported", () => {
    renderWithQuery(
      <SessionInspector session={session([], { status: "working" })} />,
    );

    expect(activitySection().getByText("Unknown")).toBeInTheDocument();
  });

  it("keeps the last known activity visible when the daemon reports no signal", () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "no_signal",
          activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    const activityRow = activitySection()
      .getByText("Idle")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    expect(within(activityRow).getByText("No Signal")).toBeInTheDocument();
  });

  it("does not derive the Activity label from PR-oriented session status", () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "review_pending",
          activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    expect(activitySection().getByText("Idle")).toBeInTheDocument();
    expect(
      activitySection().queryByText("Input Needed"),
    ).not.toBeInTheDocument();
  });

  it.each([
    ["ci_failed", "CI Failed"],
    ["changes_requested", "Changes Requested"],
    ["commented", "Commented"],
  ] as const)(
    "renders %s as an SCM state in the current Activity row",
    (status, label) => {
      renderWithQuery(
        <SessionInspector
          session={session([], {
            status,
            activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
          })}
        />,
      );

      const activityRow = activitySection()
        .getByText("Idle")
        .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
      expect(within(activityRow).getByText(label)).toBeInTheDocument();
    },
  );

  it("keeps an unresolved, non-blocking review comment visible as Commented feedback while the agent is working (#5765)", () => {
    renderWithQuery(
      <SessionInspector
        session={session(
          [pr(5765, "open", { review: "review_required", reviewComments: true })],
          {
            status: "working",
            activity: { state: "active", lastActivityAt: "2026-06-15T10:00:00Z" },
          },
        )}
      />,
    );

    const activityRow = activitySection()
      .getByText("Working")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    expect(within(activityRow).getByText("Commented")).toBeInTheDocument();
    expect(within(activityRow).queryByText("Changes Requested")).not.toBeInTheDocument();
  });

  it("does not show Commented for a stale unresolved comment on a merged PR", () => {
    renderWithQuery(
      <SessionInspector
        session={session(
          [pr(5765, "merged", { review: "review_required", reviewComments: true })],
          {
            status: "idle",
            activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
          },
        )}
      />,
    );

    const activityRow = activitySection()
      .getByText("Idle")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    expect(within(activityRow).queryByText("Commented")).not.toBeInTheDocument();
  });

  it("ignores stale failing CI from a merged PR when the open PR is passing", () => {
    renderWithQuery(
      <SessionInspector
        session={session(
          [pr(5753, "open", { ci: "passing" }), pr(5754, "merged", { ci: "failing" })],
          {
            status: "working",
            activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
          },
        )}
      />,
    );

    const activityRow = activitySection()
      .getByText("Idle")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    expect(within(activityRow).queryByText("CI Failed")).not.toBeInTheDocument();
  });

  it("renders PR conflicts as an SCM state in the current Activity row", () => {
    renderWithQuery(
      <SessionInspector
        session={session([pr(7, "open", { mergeability: "conflicting" })], {
          status: "working",
          activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    const activityRow = activitySection()
      .getByText("Idle")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    expect(within(activityRow).getByText("Conflict")).toBeInTheDocument();
  });

  it("timestamps the live Activity state so it participates in chronological ordering", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-06-15T12:00:00Z"));

    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "working",
          updatedAt: "2026-06-15T11:55:00Z",
          activity: { state: "active", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    const activityRow = activitySection()
      .getByText("Working")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    expect(within(activityRow).getByText("2h ago")).toBeInTheDocument();
  });

  it("renders each Activity event as one row with its time on the right", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-06-15T12:00:00Z"));
    renderWithQuery(
      <SessionInspector
        session={session([pr(7, "open")], {
          status: "working",
          createdAt: "2026-06-15T09:00:00Z",
          activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    for (const label of [/Created workspace/, "Idle"]) {
      const row = activitySection()
        .getByText(label)
        .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
      expect(row).toHaveClass("flex", "items-center");
      expect(row.firstElementChild).toHaveClass("rounded-full");
      expect(row.lastElementChild).toHaveClass("font-mono");
    }
  });

  it("lists the branch's newest commits in Activity", async () => {
    const respond = commonGetsResponder();
    getMock.mockImplementation(async (path: string) =>
      path === "/api/v1/sessions/{sessionId}/workspace/history"
        ? {
          data: {
            sessionId: "sess-1",
            commits: Array.from({ length: 7 }, (_, index) => ({
              sha: `c${index}`,
              subject: `commit ${index}`,
              author: "ada",
              timestamp: `2026-06-15T10:0${index}:00Z`,
              files: [],
            })),
          },
          error: undefined,
        }
        : respond(path),
    );
    renderWithQuery(<SessionInspector session={session([])} />);

    expect(await activitySection().findByText("commit 0")).toBeInTheDocument();
    expect(activitySection().getAllByText("Committed")).toHaveLength(5);
    expect(activitySection().queryByText("commit 5")).not.toBeInTheDocument();
  });

  it("uses the timeline node as the single live activity indicator", () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          status: "working",
          activity: { state: "active", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    const activityRow = activitySection()
      .getByText("Working")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    const marker = activityRow.querySelector(
      "span[aria-hidden='true'].rounded-full",
    ) as HTMLElement;
    expect(marker).toHaveClass("animate-status-pulse");
    expect(
      within(activityRow).getByText("Working").querySelector(".rounded-full"),
    ).not.toBeInTheDocument();
  });

  it("aligns summary section headings on one shared inset", () => {
    renderWithQuery(
      <SessionInspector
        session={session([pr(7, "open")], {
          status: "working",
          activity: { state: "active", lastActivityAt: "2026-06-15T10:00:00Z" },
        })}
      />,
    );

    for (const title of ["Pull request", "Session controls", "Activity"]) {
      expect(
        screen.getByText(title).closest('[data-testid="inspector-section"]'),
      ).toBeInTheDocument();
    }
  });

	it("keeps execution context out of the summary", () => {
		renderWithQuery(<SessionInspector session={session([], { branch: "feature/session" })} />);

		expect(screen.queryByTestId("execution-context")).not.toBeInTheDocument();
	});

  it("keeps workspace, PR, and SCM context rows in the Activity timeline", () => {
    renderWithQuery(
      <SessionInspector
        session={session(
          [pr(7, "open", { ci: "failing", review: "changes_requested" })],
          {
            status: "ci_failed",
            activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
          },
        )}
      />,
    );

    expect(
      activitySection().getByText(/Created workspace/),
    ).toBeInTheDocument();
    expect(activitySection().getByText("Opened")).toBeInTheDocument();
    expect(activitySection().getByText("PR #7")).toBeInTheDocument();
    const activityRow = activitySection()
      .getByText("Idle")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    expect(within(activityRow).getByText("CI Failed")).toBeInTheDocument();
    expect(
      within(activityRow).getByText("Changes Requested"),
    ).toBeInTheDocument();
  });

  it("links and timestamps draft, opened, and merged PR milestones from backend lifecycle times", async () => {
    const minutesAgo = (minutes: number) =>
      new Date(Date.now() - minutes * 60 * 1000).toISOString();
    const summaries = [
      prSummary(8, "draft", {
        createdAt: minutesAgo(120),
        stateChangedAt: minutesAgo(120),
      }),
      prSummary(7, "open", {
        createdAt: minutesAgo(60),
        stateChangedAt: minutesAgo(15),
      }),
      prSummary(6, "merged", {
        createdAt: minutesAgo(180),
        stateChangedAt: minutesAgo(30),
      }),
    ];
    getMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/sessions/{sessionId}/pr") {
        return {
          data: { sessionId: "sess-1", prs: summaries },
          error: undefined,
        };
      }
      return { data: { reviewerHandleId: "", reviews: [] }, error: undefined };
    });

    renderWithQuery(
      <SessionInspector
        session={session(
          [
            pr(8, "draft", {
              url: `https://api.github.com/repos/acme/repo/pulls/8`,
            }),
            pr(7, "open", {
              url: `https://api.github.com/repos/acme/repo/pulls/7`,
            }),
            pr(6, "merged", {
              url: `https://api.github.com/repos/acme/repo/pulls/6`,
            }),
          ],
          {
            status: "merged",
            activity: { state: "idle", lastActivityAt: "2026-06-15T11:50:00Z" },
          },
        )}
      />,
    );

    await waitFor(() => {
      expect(screen.getByRole("link", { name: "Draft PR #8" })).toHaveAttribute(
        "href",
        "https://github.com/acme/repo/pull/8",
      );
    });
    const draftLink = screen.getByRole("link", { name: "Draft PR #8" });
    expect(
      within(
        draftLink.closest(
          "[data-testid='inspector-timeline-event']",
        ) as HTMLElement,
      ).getByText("2h ago"),
    ).toBeInTheDocument();

    const openLink = screen.getByRole("link", { name: "Opened PR #7" });
    expect(
      within(
        openLink.closest(
          "[data-testid='inspector-timeline-event']",
        ) as HTMLElement,
      ).getByText("1h ago"),
    ).toBeInTheDocument();

    const mergedOpenedLink = screen.getByRole("link", { name: "Opened PR #6" });
    expect(
      within(
        mergedOpenedLink.closest(
          "[data-testid='inspector-timeline-event']",
        ) as HTMLElement,
      ).getByText("3h ago"),
    ).toBeInTheDocument();

    const mergedLink = screen.getByRole("link", { name: "Merged PR #6" });
    expect(
      within(
        mergedLink.closest(
          "[data-testid='inspector-timeline-event']",
        ) as HTMLElement,
      ).getByText("30m ago"),
    ).toBeInTheDocument();
    const doneRow = screen
      .getByText("Done")
      .closest("[data-testid='inspector-timeline-event']") as HTMLElement;
    expect(within(doneRow).getByText("30m ago")).toBeInTheDocument();
  });

  it("orders Activity timeline rows by timestamp with the latest event on top", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-06-15T12:00:00Z"));
    const summaries = [
      prSummary(42, "draft", {
        createdAt: "2026-06-15T08:00:00Z",
        stateChangedAt: "2026-06-15T08:00:00Z",
      }),
      prSummary(41, "open", {
        createdAt: "2026-06-15T11:30:00Z",
        stateChangedAt: "2026-06-15T11:30:00Z",
      }),
      prSummary(40, "merged", {
        createdAt: "2026-06-15T09:15:00Z",
        stateChangedAt: "2026-06-15T10:45:00Z",
      }),
    ];

    renderWithQuery(
      <SessionInspector
        session={session(
          [
            pr(42, "draft", {
              url: `https://api.github.com/repos/acme/repo/pulls/42`,
            }),
            pr(41, "open", {
              url: `https://api.github.com/repos/acme/repo/pulls/41`,
            }),
            pr(40, "merged", {
              url: `https://api.github.com/repos/acme/repo/pulls/40`,
            }),
          ],
          {
            status: "merged",
            createdAt: "2026-06-15T09:00:00Z",
            updatedAt: "2026-06-15T11:55:00Z",
            activity: { state: "idle", lastActivityAt: "2026-06-15T10:00:00Z" },
          },
        )}
      />,
      undefined,
      (client) => seedPRSummaries(client, summaries),
    );

    const section = screen
      .getByText("Activity")
      .closest("[data-testid='inspector-section']") as HTMLElement;
    const rows = Array.from(
      section.querySelectorAll("[data-testid='inspector-timeline-event']"),
      (row) => row.textContent?.replace(/\s+/g, " ").trim(),
    );
    expect(rows).toEqual([
      "Opened PR #4130m ago",
      "Merged PR #401h ago",
      "Done1h ago",
      "Idle2h ago",
      "Opened PR #402h ago",
      "Created workspace3h ago",
      "Draft PR #424h ago",
    ]);

  });
});
