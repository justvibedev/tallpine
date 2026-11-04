import { afterEach, beforeEach, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { getMock, navigateMock, patchMock, postMock, putMock } from "./session-inspector-mocks";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState, type ReactNode } from "react";
import { SessionInspector } from "../components/SessionInspector";
import { TooltipProvider } from "../components/ui/tooltip";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import type {
  PRState,
  PullRequestFacts,
  SessionArtifact,
  WorkspaceSession,
  WorkspaceSummary,
} from "../types/workspace";

export function setRenderedOverflow(element: HTMLElement, overflowing: boolean) {
  Object.defineProperties(element, {
    clientHeight: { configurable: true, value: 64 },
    scrollHeight: { configurable: true, value: overflowing ? 96 : 64 },
  });
  fireEvent(window, new Event("resize"));
}

export const pr = (
  n: number,
  state: PRState,
  overrides: Partial<PullRequestFacts> = {},
): PullRequestFacts => ({
  url: `https://example.com/pr/${n}`,
  number: n,
  state,
  ci: "passing",
  review: "approved",
  mergeability: "mergeable",
  reviewComments: false,
  updatedAt: "2026-06-15T00:00:00Z",
  ...overrides,
});

export const session = (
  prs: PullRequestFacts[],
  overrides: Partial<WorkspaceSession> = {},
): WorkspaceSession => ({
  id: "sess-1",
  workspaceId: "ws-1",
  workspaceName: "my-app",
  title: "do the thing",
  provider: "claude-code",
  kind: "worker",
  branch: "feat/ns",
  status: "review_pending",
  updatedAt: "2026-06-15T00:00:00Z",
  autoInjectReview: true,
  autoInjectCI: true,
  prs,
  ...overrides,
});

export const sessionWithProvider = (
  prs: PullRequestFacts[],
  provider: WorkspaceSession["provider"],
): WorkspaceSession => ({
  ...session(prs),
  provider,
});

export const prSummary = (
  number: number,
  state: SessionPRSummary["state"],
  overrides: Partial<SessionPRSummary> = {},
): SessionPRSummary => {
  const url = `https://github.com/acme/repo/pull/${number}`;
  return {
    url: `https://api.github.com/repos/acme/repo/pulls/${number}`,
    htmlUrl: url,
    number,
    title: `PR ${number}`,
    state,
    provider: "github",
    repo: "acme/repo",
    author: "ada",
    sourceBranch: `feat/${number}`,
    targetBranch: "main",
    headSha: `sha-${number}`,
    additions: 4,
    deletions: 1,
    changedFiles: 2,
    ci: { autoInjectCI: true, state: "passing", failingChecks: [] },
    review: {
      decision: "none",
      hasUnresolvedHumanComments: false,
      unresolvedBy: [],
    },
    mergeability: {
      state: "mergeable",
      reasons: [],
      prUrl: url,
      conflictFiles: [],
    },
    updatedAt: "2026-06-15T12:00:00Z",
    ...overrides,
  };
};

export function seedPRSummaries(client: QueryClient, prs: SessionPRSummary[]) {
  client.setQueryData(sessionScmSummaryQueryKey("sess-1"), { prs, linkedPrs: [] });
}

/** The session view owns the top-bar slot the switcher renders into; stand one up beside the inspector. */
export function InspectorWithSwitcher(props: React.ComponentProps<typeof SessionInspector>) {
  const [host, setHost] = useState<HTMLDivElement | null>(null);
  return (
    <>
      <div data-testid="switcher-host" ref={setHost} />
      <SessionInspector onTogglePanel={() => undefined} switcherHost={host} {...props} />
    </>
  );
}

export const switcherLabels = () =>
  within(screen.getByTestId("panel-switcher")).getAllByRole("button").map((button) => button.getAttribute("aria-label"));

export function renderWithQuery(
  children: ReactNode,
  workspaces?: WorkspaceSummary[],
  seed?: (client: QueryClient) => void,
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  if (workspaces) client.setQueryData(workspaceQueryKey, workspaces);
  seed?.(client);
  return {
    ...render(
      <QueryClientProvider client={client}>
        <TooltipProvider>{children}</TooltipProvider>
      </QueryClientProvider>,
    ),
    queryClient: client,
  };
}

export function commonGetsResponder(
  _unusedRuns: unknown[] = [],
  reviewerHandleId = "",
  reviews: unknown[] = [],
  reviewerActivityState?: string,
) {
  return async (path: string) => {
    if (path === "/api/v1/agents/readiness") {
      const agents = ["claude-code", "codex", "kiro"].map((id) => agentReadiness(id));
      return { data: { agents } };
    }
    if (path === "/api/v1/agents/{agent}/models") {
      return {
        data: {
          agentId: "unknown",
          selectionMode: "text",
          models: [],
          allowCustom: false,
          source: "manual",
          fetchedAt: "2026-08-30T00:00:00Z",
          stale: false,
        },
        error: undefined,
      };
    }
    if (path === "/api/v1/sessions/{sessionId}/workspace/manifest") {
      return {
        data: { sessionId: "sess-1", files: [], truncated: false },
        error: undefined,
      };
    }
    if (path === "/api/v1/sessions/{sessionId}/reviews") {
      return { data: { reviewerHandleId, reviewerActivityState, reviews } };
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
  };
}


export const approvedReview = {
  id: "run-1",
  reviewId: "review-1",
  sessionId: "sess-1",
  harness: "codex",
  status: "complete",
  verdict: "approved",
  body: "Looks good.",
  prUrl: "https://example.com/pr/3",
  targetSha: "abc123",
  createdAt: "2026-06-16T10:06:00Z",
  autoInjectReview: true,
};

export const failedReview = {
  ...approvedReview,
  id: "run-failed",
  status: "failed",
  verdict: "",
  body: "reviewer crashed",
};

export const reviewState = (n: number, status: string, targetSha = `sha-${n}`) => ({
  prUrl: `https://example.com/pr/${n}`,
  prNumber: n,
  title: `Reviewable change ${n}`,
  targetSha,
  status,
  latestRun:
    status === "up_to_date"
      ? { ...approvedReview, prUrl: `https://example.com/pr/${n}`, targetSha }
      : undefined,
});


export const artifact = (
  overrides: Partial<SessionArtifact> & { path: string },
): SessionArtifact => ({
  kind: "file",
  name: overrides.path,
  size: 128,
  updatedAt: "2026-06-15T00:00:00Z",
  ...overrides,
});

export function mockCommonGets(
  _unusedRuns: unknown[] = [],
  reviewerHandleId = "",
  reviews: unknown[] = [],
  reviewerActivityState?: string,
) {
  getMock.mockImplementation(commonGetsResponder(_unusedRuns, reviewerHandleId, reviews, reviewerActivityState));
}

/** Registers the shared beforeEach/afterEach hooks for the calling test file. */
export function installInspectorLifecycle() {
  beforeEach(() => {
    getMock.mockReset();
    navigateMock.mockReset();
    patchMock.mockReset();
    postMock.mockReset();
    useUiStore.setState({ inspectorSessions: {}, settingsModal: null });
    putMock.mockReset();
    mockCommonGets();
    patchMock.mockResolvedValue({ data: { ok: true }, error: undefined, response: { status: 200 } });
    postMock.mockResolvedValue({ data: { ok: true, sessionId: "sess-1" }, error: undefined });
    putMock.mockResolvedValue({ data: { session: {} }, error: undefined, response: { status: 200 } });
  });
  afterEach(() => {
    vi.useRealTimers();
  });
}
