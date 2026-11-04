import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { pr, session, renderWithQuery, artifact, installInspectorLifecycle } from "../test/session-inspector-fixtures";
import { getMock, navigateMock, patchMock, postMock } from "../test/session-inspector-mocks";

vi.mock("@tanstack/react-router", () => import("../test/session-inspector-mocks").then((m) => m.routerModule()));
vi.mock("../lib/preview-mode", () => import("../test/session-inspector-mocks").then((m) => m.previewModeModule()));
vi.mock("../lib/api-client", () => import("../test/session-inspector-mocks").then((m) => m.apiClientModule()));
vi.mock("../lib/host-clients", async (importOriginal) =>
  (await import("../test/session-inspector-mocks")).hostClientsModule(await importOriginal<typeof import("../lib/host-clients")>()),
);

installInspectorLifecycle();


describe("SessionInspector Artifacts section", () => {
  it("shows an artifact list, not the empty PR state, when the session output is artifacts", () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          outputType: "artifact",
          artifactFiles: [
            artifact({ path: "notes.md", name: "notes.md", kind: "markdown" }),
            artifact({ path: "report.html", name: "report.html", kind: "html" }),
          ],
        })}
      />,
    );

    expect(screen.getByText("Artifacts (2)")).toBeInTheDocument();
    expect(screen.getByText("notes.md")).toBeInTheDocument();
    expect(screen.getByText("report.html")).toBeInTheDocument();
    expect(
      screen.queryByText("No pull request opened yet."),
    ).not.toBeInTheDocument();
  });

  it("does not show artifacts for a plain PR session with no artifact files", () => {
    renderWithQuery(<SessionInspector session={session([pr(7, "open")], { outputType: "pr" })} />);

    expect(screen.getByText("Pull request")).toBeInTheDocument();
    expect(screen.queryByText(/Artifacts? /)).not.toBeInTheDocument();
  });

  it("shows separate pull request and artifact sections when a session has both (pr_artifact)", () => {
    renderWithQuery(
      <SessionInspector
        session={session([pr(7, "open")], {
          outputType: "pr_artifact",
          artifactFiles: [artifact({ path: "notes.md" })],
        })}
      />,
    );

    expect(screen.getByText("Pull request")).toBeInTheDocument();
    expect(screen.getByText("Artifact")).toBeInTheDocument();
    expect(screen.getAllByText("PR #7").length).toBeGreaterThan(0);
    expect(screen.getByText("notes.md")).toBeInTheDocument();
  });

  it("opens an html artifact through the Browser preview flow", async () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          outputType: "artifact",
          artifactFiles: [
            artifact({
              path: "report.html",
              name: "report.html",
              kind: "html",
              previewUrl: "http://sess-1.localhost:3101/report.html",
            }),
          ],
        })}
      />,
    );

    fireEvent.click(screen.getByText("report.html"));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", {
        params: { path: { sessionId: "sess-1" } },
        body: { url: "http://sess-1.localhost:3101/report.html" },
      }),
    );
  });

  it("opens an html artifact through the Browser preview flow even after the session terminates", async () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          outputType: "artifact",
          isTerminated: true,
          status: "terminated",
          artifactFiles: [
            artifact({
              path: "report.html",
              name: "report.html",
              kind: "html",
              previewUrl: "http://sess-1.localhost:3101/report.html",
            }),
          ],
        })}
      />,
    );

    fireEvent.click(screen.getByText("report.html"));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", {
        params: { path: { sessionId: "sess-1" } },
        body: { url: "http://sess-1.localhost:3101/report.html" },
      }),
    );
  });

  it("opens a markdown/file artifact through the artifact viewer flow", () => {
    const onOpenArtifact = vi.fn();
    renderWithQuery(
      <SessionInspector
        onOpenArtifact={onOpenArtifact}
        session={session([], {
          outputType: "artifact",
          artifactFiles: [artifact({ path: "notes.md", name: "notes.md", kind: "markdown" })],
        })}
      />,
    );

    fireEvent.click(screen.getByText("notes.md"));

    expect(onOpenArtifact).toHaveBeenCalledWith({ path: "notes.md" });
    expect(postMock).not.toHaveBeenCalledWith(
      "/api/v1/sessions/{sessionId}/preview",
      expect.anything(),
    );
  });

  it("opens the Artifacts view from its own tab", async () => {
    const onViewChange = vi.fn();
    renderWithQuery(
      <SessionInspector
        onViewChange={onViewChange}
        session={session([], {
          artifactFiles: [artifact({ path: "notes.md", name: "notes.md", kind: "markdown" })],
        })}
      />,
    );

    expect(screen.queryByRole("button", { name: "More" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "true");

    await userEvent.click(screen.getByRole("tab", { name: "Artifacts" }));

    expect(onViewChange).toHaveBeenCalledWith("artifacts");
    expect(await screen.findByRole("button", { name: /notes\.md/ })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "false");
    expect(screen.getByRole("tab", { name: "Artifacts" })).toHaveAttribute("aria-selected", "true");
  });

  it("keeps the Artifacts tab for a requested artifact the session does not list yet", () => {
    const onViewChange = vi.fn();
    renderWithQuery(
      <SessionInspector
        artifactRequest={{ key: 1, path: "fresh.md" }}
        onViewChange={onViewChange}
        session={session([], { artifactFiles: [] })}
        view="artifacts"
      />,
    );

    expect(screen.getByRole("tab", { name: "Artifacts" })).toHaveAttribute("aria-selected", "true");
    expect(onViewChange).not.toHaveBeenCalledWith("summary");
  });

  it("keeps a requested Artifacts view while the session is loading", () => {
    const onViewChange = vi.fn();
    renderWithQuery(<SessionInspector onViewChange={onViewChange} view="artifacts" />);

    expect(onViewChange).not.toHaveBeenCalled();
  });

  it("hides the Artifacts tab when the session has no artifacts", () => {
    renderWithQuery(<SessionInspector session={session([], { artifactFiles: [] })} view="artifacts" />);

    expect(screen.queryByRole("tab", { name: "Artifacts" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "true");
  });

  it("does not render a feedback button on artifact rows", () => {
    renderWithQuery(
      <SessionInspector
        onOpenArtifact={vi.fn()}
        session={session([], {
          outputType: "artifact",
          artifactFiles: [artifact({ path: "report.html", name: "report.html", kind: "html", previewUrl: "http://sess-1.localhost:3101/report.html" })],
        })}
      />,
    );

    expect(screen.queryByRole("button", { name: "Add feedback: report.html" })).not.toBeInTheDocument();
  });
});

describe("SessionInspector usage", () => {
	it("does not show usage or cost", async () => {
		renderWithQuery(<SessionInspector session={session([])} />);
		expect(await screen.findByText("Branch")).toBeInTheDocument();
		expect(screen.queryByText("Usage & cost")).not.toBeInTheDocument();
		expect(getMock).not.toHaveBeenCalledWith("/api/v1/usage/sessions/{sessionId}", expect.anything());
	});
});

describe("SessionInspector completion controls", () => {
  it("persists the terminate-on-merge preference", async () => {
    renderWithQuery(<SessionInspector session={session([pr(7, "open")])} />);

    await userEvent.click(
      screen.getByRole("switch", {
        name: "Archive session when pull requests merge",
      }),
    );

    await waitFor(() =>
      expect(patchMock).toHaveBeenCalledWith(
        "/api/v1/sessions/{sessionId}/merge-policy",
        {
          params: { path: { sessionId: "sess-1" } },
          body: { terminateOnPrMerge: true },
        },
      ),
    );
  });

  it("terminates a live merged session and returns to its orchestrator immediately", async () => {
    postMock.mockReturnValue(new Promise(() => {}));
    const worker = session([pr(7, "merged")], { status: "merged" });
    const orchestrator = session([], {
      id: "orch-1",
      kind: "orchestrator",
      title: "orchestrator",
    });
    renderWithQuery(<SessionInspector session={worker} />, [
      {
        id: "ws-1",
        name: "my-app",
        path: "/repo",
        sessions: [worker, orchestrator],
      },
    ]);

    expect(
      screen.queryByRole("switch", {
        name: "Archive session when pull requests merge",
      }),
    ).not.toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Archive session" }),
    );
    expect(
      screen.getByRole("dialog", { name: "Are you sure you want to archive this session?" }),
    ).toBeInTheDocument();
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Confirm, archive session",
      }),
    );

    expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/kill", {
      params: { path: { sessionId: "sess-1" } },
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(navigateMock).toHaveBeenCalledWith({
      to: "/projects/$projectId/sessions/$sessionId",
      params: { projectId: "ws-1", sessionId: "orch-1" },
    });
  });

  it("shows only direct termination controls for ad hoc sessions", async () => {
    renderWithQuery(
      <SessionInspector
        session={session([], {
          workspaceId: STANDALONE_WORKSPACE_ID,
          workspaceName: "Scratchpad",
          status: "idle",
        })}
      />,
    );

    expect(screen.getByText("Session controls")).toBeInTheDocument();
    expect(
      screen.queryByRole("switch", { name: "Automatically fix CI failures" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("switch", { name: "Automatically fix review comments" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("switch", {
        name: "Archive session when pull requests merge",
      }),
    ).not.toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("button", { name: "Archive session" }),
    );
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Confirm, archive session",
      }),
    );

    expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/kill", {
      params: { path: { sessionId: "sess-1" } },
    });
    expect(navigateMock).toHaveBeenCalledWith({ to: "/" });
  });

  it("keeps the confirmation dismissed after a termination failure", async () => {
    postMock.mockResolvedValueOnce({
      error: new Error("runtime teardown failed"),
      response: { status: 500 },
    });
    renderWithQuery(
      <SessionInspector
        session={session([pr(7, "merged")], { status: "merged" })}
      />,
    );

    await userEvent.click(
      screen.getByRole("button", { name: "Archive session" }),
    );
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Confirm, archive session",
      }),
    );

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(navigateMock).toHaveBeenCalledWith({
      to: "/projects/$projectId",
      params: { projectId: "ws-1" },
      search: { view: "board" },
    });
  });

  it("hides completion controls after the session is terminated", () => {
    renderWithQuery(
      <SessionInspector
        session={session([pr(7, "merged")], {
          status: "merged",
          isTerminated: true,
        })}
      />,
    );

    expect(screen.queryByText("Completion")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Archive session" }),
    ).not.toBeInTheDocument();
  });

  it("does not show completion controls for orchestrator sessions", () => {
    renderWithQuery(
      <SessionInspector session={session([], { kind: "orchestrator" })} />,
    );

    expect(screen.queryByText("Completion")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("switch", {
        name: "Archive session when pull requests merge",
      }),
    ).not.toBeInTheDocument();
  });
});
