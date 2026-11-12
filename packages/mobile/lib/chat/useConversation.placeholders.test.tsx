import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { ChatImage } from "./types";
import { PLACEHOLDER_BLOCK_HEADER } from "@tallpine/shared/chat/image-placeholders";

const { request, storage, stored } = vi.hoisted(() => {
 const stored = new Map<string, string>();
 return { request: vi.fn(), stored, storage: {
  getItem: vi.fn(async (key: string) => stored.get(key) ?? null),
  setItem: vi.fn(async (key: string, value: string) => { stored.set(key, value); }),
  removeItem: vi.fn(async (key: string) => { stored.delete(key); }),
 } };
});
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: () => "qa-host", authHeaders: () => ({}), httpBase: () => "http://qa-host" }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: storage }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
import { useMobileConversation, type MobileConversation } from "./useConversation";

const config = {} as ServerConfig;
const images: ChatImage[] = [{ mimeType: "image/png", data: "aGVsbG8=" }, { mimeType: "image/png", data: "aGk=" }];
let renderer: ReactTestRenderer | undefined;
let conversation: MobileConversation;
let supportsExcerpts: boolean | undefined;
function Harness() { conversation = useMobileConversation(config, "qa-session"); return null; }

beforeEach(() => {
 vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
 stored.clear(); vi.clearAllMocks(); supportsExcerpts = undefined;
 request.mockImplementation(async (_cfg, path, init) => ({ json: async () => path.endsWith("/attachments")
  ? { paths: [".tallpine/attachments/attachment-qa1.png", ".tallpine/attachments/attachment-qa2.png"] }
  : init?.method === "POST" ? { turnId: "qa-turn", duplicate: false }
  : { controller: "ready", supportsExcerpts, capabilities: ["images"], messages: [] } }));
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

// The phone composer leaves [IMGn] in the text; the hook stages the images and
// sends the agent the text, a separator and the block mapping each placeholder.
it("sends the agent the owner's placeholders, then the block mapping each to its staged path", async () => {
 await act(async () => { renderer = create(<Harness />); });
 request.mockClear();
 await act(async () => { await conversation.send("after [IMG2], before [IMG1]", images); });
 const posted = request.mock.calls.find(call => call[1].endsWith("/messages"));
 expect(JSON.parse(posted![2].body).text).toBe(
  `after [IMG2], before [IMG1]\n\n---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] .tallpine/attachments/attachment-qa1.png\n- [IMG2] .tallpine/attachments/attachment-qa2.png`,
 );
});
