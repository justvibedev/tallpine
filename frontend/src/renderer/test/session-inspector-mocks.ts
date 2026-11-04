// Shared mock wiring for the SessionInspector.*.test.tsx suites. A test file
// installs it with four vi.mock factories that import this module lazily (the
// factories are hoisted, so they cannot close over file-level values). Keep this
// module free of component imports: the factories await it, so a cycle through
// the mocked modules would deadlock. mockCommonGets and installInspectorLifecycle
// live in session-inspector-fixtures.tsx for that reason.
//
//   vi.mock("../lib/api-client", () => import("../test/session-inspector-mocks").then((m) => m.apiClientModule()));
import { vi } from "vitest";

export const getMock = vi.fn();
export const navigateMock = vi.fn();
export const patchMock = vi.fn();
export const putMock = vi.fn();
export const postMock = vi.fn();

export function postCallsFor(path: string) {
  return postMock.mock.calls.filter(([calledPath]) => calledPath === path);
}

export const routerModule = () => ({ useNavigate: () => navigateMock });

export const previewModeModule = () => ({ usesPreviewWorkspaceData: false });

export const apiClientModule = () => ({
  apiClient: { GET: getMock, PATCH: patchMock, POST: postMock, PUT: putMock },
  getApiBaseUrl: () => "http://127.0.0.1:3101",
  hasTrustedApiBaseUrl: () => false,
  subscribeApiBaseUrl: () => () => {},
  apiErrorMessage: (error: unknown, fallback = "Request failed") => {
    if (error instanceof Error) return error.message;
    if (typeof error === "object" && error !== null && "message" in error) {
      return String((error as { message: unknown }).message);
    }
    return fallback;
  },
});

export const hostClientsModule = (original: object) => ({
  ...original,
  clientForSessionHost: () => ({ GET: getMock, POST: postMock, PATCH: patchMock, PUT: putMock }),
  clientForHost: () => ({ GET: getMock, POST: postMock, PATCH: patchMock, PUT: putMock }),
});
