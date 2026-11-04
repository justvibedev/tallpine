import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import { sessionWorkspaceFilesQueryKey } from "../hooks/useSessionWorkspaceFiles";
import { useUiStore } from "../stores/ui-store";
import { pr, session, InspectorWithSwitcher, switcherLabels, renderWithQuery, reviewState, mockCommonGets, installInspectorLifecycle } from "../test/session-inspector-fixtures";
import { getMock } from "../test/session-inspector-mocks";

vi.mock("@tanstack/react-router", () => import("../test/session-inspector-mocks").then((m) => m.routerModule()));
vi.mock("../lib/preview-mode", () => import("../test/session-inspector-mocks").then((m) => m.previewModeModule()));
vi.mock("../lib/api-client", () => import("../test/session-inspector-mocks").then((m) => m.apiClientModule()));
vi.mock("../lib/host-clients", async (importOriginal) =>
  (await import("../test/session-inspector-mocks")).hostClientsModule(await importOriginal<typeof import("../lib/host-clients")>()),
);

installInspectorLifecycle();

describe("SessionInspector tabs", () => {
  it("gives an orchestrator only a Browser tab (plus Plan when available)", () => {
    renderWithQuery(<InspectorWithSwitcher browserOnly session={session([], { kind: "orchestrator" })} />);
    expect(switcherLabels()).toEqual(["Browser"]);
    // The orchestrator has no in-panel tab strip at all.
    expect(screen.queryAllByRole("tab")).toHaveLength(0);
    expect(screen.queryByRole("tab", { name: "Summary" })).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Files" })).not.toBeInTheDocument();
    expect(screen.getByRole("complementary", { name: "Session inspector" }).querySelector(".session-inspector__body--browser")).toBeInTheDocument();
  });

  it("closes an open panel from the active switcher icon and opens a closed one from it", async () => {
    const onTogglePanel = vi.fn();
    const view = renderWithQuery(<InspectorWithSwitcher session={session([])} onTogglePanel={onTogglePanel} view="browser" />);
    await userEvent.click(screen.getByRole("button", { name: "Browser" }));
    expect(onTogglePanel).toHaveBeenCalledTimes(1);
    view.unmount();

    const onToggleClosed = vi.fn();
    renderWithQuery(<InspectorWithSwitcher panelOpen={false} session={session([])} onTogglePanel={onToggleClosed} view="browser" />);
    await userEvent.click(screen.getByRole("button", { name: "Browser" }));
    expect(onToggleClosed).toHaveBeenCalledTimes(1);
  });

  it("has no Backlog tab: Backlog is a page", () => {
    renderWithQuery(<InspectorWithSwitcher session={session([])} />);
    expect(screen.queryByRole("button", { name: "Backlog" })).not.toBeInTheDocument();
  });

  it("keeps the same Browser-only inspector for a remote orchestrator", () => {
    renderWithQuery(<InspectorWithSwitcher browserOnly hostId="box-a" session={session([], { kind: "orchestrator", hostId: "box-a" })} />);
    expect(switcherLabels()).toEqual(["Browser"]);
    expect(screen.queryByRole("tab", { name: "Files" })).not.toBeInTheDocument();
    expect(screen.getByRole("complementary", { name: "Session inspector" }).querySelector(".session-inspector__body--browser")).toBeInTheDocument();
  });

  it("gives the Browser viewport the full inspector body without the default content gutter", async () => {
    renderWithQuery(<InspectorWithSwitcher session={session([])} />);

    await userEvent.click(screen.getByRole("button", { name: "Browser" }));

    const body = screen
      .getByRole("complementary", { name: "Session inspector" })
      .querySelector(".session-inspector__body--browser");
    expect(body).toHaveClass(
      "session-inspector__body--browser",
      "p-0",
      "overflow-hidden",
    );
    expect(body).not.toHaveClass(
      "p-3",
      "pb-4",
      "@max-[300px]/inspector:px-2.5",
    );
  });

  it("shows the active tab as icon + label and keeps the others square icon-only buttons", () => {
    renderWithQuery(<SessionInspector session={session([])} />);

    const summaryTab = screen.getByRole("tab", { name: "Summary" });
    expect(summaryTab).toHaveAttribute("aria-selected", "true");
    expect(screen.getByTestId("inspector-active-tab-label")).toHaveTextContent("Summary");
    expect(summaryTab).not.toHaveClass("size-control-md");

    const filesTab = screen.getByRole("tab", { name: "Files" });
    expect(filesTab).not.toHaveClass("flex-1");
    expect(filesTab).toHaveClass("size-control-md", "p-0", "shrink-0");
    expect(filesTab).toHaveAttribute("title", "Files");
    expect(screen.getAllByTestId("inspector-active-tab-label")).toHaveLength(1);
    // Below a narrow panel width the label gives way to the icon alone.
    expect(screen.getByTestId("inspector-active-tab-label")).toHaveClass("truncate", "@max-[300px]/inspector:hidden");
  });

  it("shows the glow only while real browser activity is unseen", () => {
    const currentSession = session([]);
    const view = renderWithQuery(<InspectorWithSwitcher session={currentSession} />);
    expect(
      screen.queryByTestId("panel-switcher-browser-dot"),
    ).not.toBeInTheDocument();
    view.unmount();

    useUiStore.getState().setBrowserUnseen(currentSession.id, true);
    renderWithQuery(<InspectorWithSwitcher session={currentSession} />);
    expect(screen.getByTestId("panel-switcher-browser-dot")).toBeInTheDocument();

    act(() =>
      useUiStore.getState().setInspectorView(currentSession.id, "browser"),
    );
    expect(
      screen.queryByTestId("panel-switcher-browser-dot"),
    ).not.toBeInTheDocument();
  });

  it("renders the supplied files view when the Files tab opens", async () => {
    const onOpenFiles = vi.fn();
    renderWithQuery(
      <SessionInspector
        filesView={<div>workspace file review</div>}
        onOpenFiles={onOpenFiles}
        session={session([])}
      />,
    );

    await userEvent.click(screen.getByRole("tab", { name: "Files" }));

    expect(onOpenFiles).toHaveBeenCalledTimes(1);
    expect(screen.getByText("workspace file review")).toBeInTheDocument();
  });

  it("warms the workspace files cache before the Files tab opens", async () => {
    renderWithQuery(<SessionInspector session={session([])} />);

    const filesTab = screen.getByRole("tab", { name: "Files" });
    expect(within(filesTab).getByText("Files")).toBeInTheDocument();
    expect(within(filesTab).getByTestId("files-viewer-icon")).toBeInTheDocument();
    await waitFor(() =>
      expect(getMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/workspace/manifest",
        {
          params: { path: { sessionId: "sess-1" } },
        },
      ),
    );
  });


  it("shows a live changed-file count on the Files tab once the shared cache is populated", () => {
    renderWithQuery(
      <SessionInspector session={session([])} />,
      undefined,
      (client) => {
        client.setQueryData(sessionWorkspaceFilesQueryKey("sess-1"), {
          sessionId: "sess-1",
          truncated: false,
          files: [
            {
              path: "src/App.tsx",
              status: "modified",
              additions: 2,
              deletions: 1,
              size: 120,
              binary: false,
            },
            {
              path: "README.md",
              status: "unmodified",
              additions: 0,
              deletions: 0,
              size: 80,
              binary: false,
            },
          ],
        });
      },
    );

    const filesTab = screen.getByRole("tab", { name: "Files" });
    expect(within(filesTab).getByText("1 File")).toBeInTheDocument();
    // The accessible name stays static so existing name-based tab queries keep resolving.
    expect(filesTab).toHaveAttribute("title", "Files");
  });

  it("distinguishes a checked-but-clean workspace (0 Files) from an unopened tab (Files)", () => {
    renderWithQuery(
      <SessionInspector session={session([])} />,
      undefined,
      (client) => {
        client.setQueryData(sessionWorkspaceFilesQueryKey("sess-1"), {
          sessionId: "sess-1",
          truncated: false,
          files: [
            {
              path: "README.md",
              status: "unmodified",
              additions: 0,
              deletions: 0,
              size: 80,
              binary: false,
            },
          ],
        });
      },
    );

    const filesTab = screen.getByRole("tab", { name: "Files" });
    expect(within(filesTab).getByText("0 Files")).toBeInTheDocument();
  });

  it("keeps collapsed inspector content hidden and inert", () => {
    renderWithQuery(
      <SessionInspector isInspectorVisible={false} session={session([])} />,
    );

    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /inspector panel/i }),
    ).not.toBeInTheDocument();
    expect(
      document.querySelector("[aria-hidden='true'][inert]"),
    ).toBeInTheDocument();
  });
});


describe("SessionInspector tabs", () => {
  it("exposes Reviews after Summary and keeps review content out of Summary", async () => {
    mockCommonGets([], "", [reviewState(1, "needs_review")]);
    renderWithQuery(<SessionInspector session={session([pr(1, "open")])} sideChatView={<div />} />);
    const tabs = screen.getAllByRole("tab").map((el) => el.textContent?.trim());
    // Browser moved to the top-bar switcher.
    expect(tabs).toEqual(["Summary", "Reviews", "Side chat", "Files"]);
    expect(screen.queryByText("Review controls")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "Reviews" }));

    expect(await screen.findByText("Review controls")).toBeInTheDocument();
    expect(screen.queryByText("Pull request")).not.toBeInTheDocument();
  });

  it("keeps the Reviews tab available for draft PRs", async () => {
    mockCommonGets([], "", [reviewState(1, "needs_review")]);
    renderWithQuery(<SessionInspector session={session([pr(1, "draft")])} />);

    await userEvent.click(screen.getByRole("tab", { name: "Reviews" }));

    expect(await screen.findByText("Review controls")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Review latest commit" })).not.toBeDisabled();
  });

  it("hides the Reviews tab when every PR is merged or closed", async () => {
    mockCommonGets([], "", [reviewState(1, "up_to_date"), reviewState(2, "up_to_date")]);
    renderWithQuery(
      <SessionInspector
        session={session([pr(1, "merged"), pr(2, "closed")])}
        view="reviews"
      />,
    );

    expect(screen.queryByRole("tab", { name: "Reviews" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Summary" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.queryByText("Review controls")).not.toBeInTheDocument();
    expect(screen.queryByText("View review details")).not.toBeInTheDocument();
  });

  it("collapses and expands a section from its heading", async () => {
    renderWithQuery(<SessionInspector session={session([])} />);
    const heading = screen.getByRole("button", { name: "Activity" });
    await userEvent.click(heading);
    expect(heading).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(heading);
    expect(heading).toHaveAttribute("aria-expanded", "true");
  });

  it("does not render the overview card in the summary", () => {
    renderWithQuery(
      <SessionInspector
        session={{ ...session([], { outputType: "pr" }), issueId: "github:acme/project-one#42" }}
      />,
    );

    expect(screen.queryByText("Overview")).not.toBeInTheDocument();
    expect(screen.queryByText("Issue")).not.toBeInTheDocument();
    expect(
      screen.queryByText("github:acme/project-one#42"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Branch")).not.toBeInTheDocument();
  });
});
