import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import { pr, session, renderWithQuery, failedReview, reviewState, mockCommonGets, installInspectorLifecycle } from "../test/session-inspector-fixtures";

vi.mock("@tanstack/react-router", () => import("../test/session-inspector-mocks").then((m) => m.routerModule()));
vi.mock("../lib/preview-mode", () => import("../test/session-inspector-mocks").then((m) => m.previewModeModule()));
vi.mock("../lib/api-client", () => import("../test/session-inspector-mocks").then((m) => m.apiClientModule()));
vi.mock("../lib/host-clients", async (importOriginal) =>
  (await import("../test/session-inspector-mocks")).hostClientsModule(await importOriginal<typeof import("../lib/host-clients")>()),
);

installInspectorLifecycle();

describe("SessionInspector failed review notice", () => {
  it("surfaces the stored reason of a failed manual review", async () => {
    mockCommonGets([], "", [
      {
        ...reviewState(3, "needs_review", "abc123"),
        latestRun: {
          ...failedReview,
          triggerSource: "manual",
          body: "launch reviewer: reviewer replace stale pane: no such session",
        },
      },
    ]);

    renderWithQuery(<SessionInspector session={session([pr(3, "open")])} />);
    await userEvent.click(screen.getByRole("tab", { name: "Reviews" }));

    expect(await screen.findByRole("status")).toHaveTextContent(
      "Review Failed: launch reviewer: reviewer replace stale pane: no such session",
    );
  });
});
