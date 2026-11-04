import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	initialize: vi.fn(async (_path: string) => undefined),
	requestNewTask: vi.fn(),
}));

vi.mock("../lib/shell-context", () => ({
	useShell: () => ({ cloneProject: vi.fn(), createProject: vi.fn(), initializeProjectRepository: mocks.initialize }),
}));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: { requestNewTask: typeof mocks.requestNewTask }) => unknown) => select({ requestNewTask: mocks.requestNewTask }),
}));
vi.mock("./WelcomePanel", () => ({ WelcomePanel: ({ children }: { children: ReactNode }) => <>{children}</> }));
vi.mock("./GitHubOnboardingNotice", () => ({ GitHubOnboardingNotice: () => null }));
vi.mock("./CreateProjectFlow", () => ({
	CreateProjectFlow: ({ onInitializeProject, onCreateStandaloneAgent }: {
		onInitializeProject: (path: string) => Promise<void>;
		onCreateStandaloneAgent: () => void;
	}) => <>
		<button onClick={() => { void onInitializeProject("/tmp/project").catch(() => undefined); }}>Initialize project</button>
		<button onClick={onCreateStandaloneAgent}>Standalone agent</button>
	</>,
}));

import { BoardWelcome } from "./BoardEmptyStates";

describe("BoardWelcome", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.initialize.mockResolvedValue(undefined);
	});

	it("initializes a project through the shell", async () => {
		render(<BoardWelcome />);
		fireEvent.click(screen.getByRole("button", { name: "Initialize project" }));
		await waitFor(() => expect(mocks.initialize).toHaveBeenCalledWith("/tmp/project"));
	});

	it("opens a standalone agent task", () => {
		render(<BoardWelcome />);
		fireEvent.click(screen.getByRole("button", { name: "Standalone agent" }));
		expect(mocks.requestNewTask).toHaveBeenCalledWith("__standalone__");
	});
});
