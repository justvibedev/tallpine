import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useProjectRemoval } from "./useProjectRemoval";

function setup(activeProjectId: string | null) {
	const goHome = vi.fn();
	const onRemoveProject = vi.fn().mockResolvedValue(undefined);
	const { result } = renderHook(() =>
		useProjectRemoval({ workspace: { id: "p1" } as never, selection: { activeProjectId, goHome } as never, onRemoveProject }),
	);
	return { goHome, onRemoveProject, result };
}

describe("useProjectRemoval focus", () => {
	it("keeps focus when removing a non-focused project", async () => {
		const { goHome, onRemoveProject, result } = setup("p2");
		await act(async () => { await result.current.handleConfirmRemove(); });
		expect(onRemoveProject).toHaveBeenCalledWith("p1");
		expect(goHome).not.toHaveBeenCalled();
	});
	it("goes home when removing the focused project", async () => {
		const { goHome, result } = setup("p1");
		await act(async () => { await result.current.handleConfirmRemove(); });
		expect(goHome).toHaveBeenCalledTimes(1);
	});
});
