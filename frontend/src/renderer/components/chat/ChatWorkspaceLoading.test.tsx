// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import { cleanupChatWorkspaceTest, resetChatWorkspaceMocks } from "./ChatWorkspace.test.setup";
import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixtureSettled } from "../../lib/chat-fixture";
import { renderChat as render } from "../../test/render-chat";

beforeEach(resetChatWorkspaceMocks);
afterEach(cleanupChatWorkspaceTest);

describe("ChatWorkspace first load", () => {
	it("shows neutral loading copy, not a connection failure, while the first snapshot loads", () => {
		render(<ChatWorkspace snapshot={{ ...chatFixtureSettled, controller: { state: "connecting" } }} loadingQuietly />);

		expect(screen.getByText("Loading conversation…")).toBeInTheDocument();
		expect(screen.queryByText("The controller is not connected")).not.toBeInTheDocument();
	});
});
