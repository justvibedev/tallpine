import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionCopyContextItems } from "./SessionCopyMenuItems";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "./ui/context-menu";
import { sessionLink } from "../lib/session-links";

const copyLinkText = vi.fn();
vi.mock("../lib/copy-link", () => ({ copyLinkText: (text: string) => copyLinkText(text) }));

describe("SessionCopyContextItems", () => {
	beforeEach(() => copyLinkText.mockClear());
	it("copies the session id and the in-app link", () => {
		render(
			<ContextMenu>
				<ContextMenuTrigger>row</ContextMenuTrigger>
				<ContextMenuContent>
					<SessionCopyContextItems projectId="p 1" sessionId="s-1" />
				</ContextMenuContent>
			</ContextMenu>,
		);
		fireEvent.contextMenu(screen.getByText("row"));
		fireEvent.click(screen.getByText("Copy session ID"));
		expect(copyLinkText).toHaveBeenCalledWith("s-1");
		fireEvent.contextMenu(screen.getByText("row"));
		fireEvent.click(screen.getByText("Copy session link"));
		expect(copyLinkText).toHaveBeenCalledWith("tallpine://sessions/p%201/s-1");
		expect(sessionLink("p 1", "s-1")).toBe("tallpine://sessions/p%201/s-1");
	});
});
