import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionStatusPill } from "./ShellTopbar";

const session = (pausedUntil?: string) => ({ activity: { state: "active" }, pausedUntil }) as unknown as WorkspaceSession;

describe("SessionStatusPill usage-limit wait", () => {
	it("shows the wait and the reset time instead of the activity", () => {
		render(<SessionStatusPill session={session("2026-11-02T06:20:00Z")} />);
		expect(screen.getByText(/Waiting for usage reset until .*\d/)).toBeInTheDocument();
	});

	it("shows the plain activity when not paused", () => {
		render(<SessionStatusPill session={session()} />);
		expect(screen.queryByText(/Waiting for usage reset/)).not.toBeInTheDocument();
	});
});
