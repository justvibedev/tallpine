import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { StorageCompactionRows } from "./StorageCompactionRows";

const { locks } = vi.hoisted(() => ({ locks: { keys: new Set<string>() } }));

vi.mock("../../hooks/useManagedLocks", () => ({ useManagedLocks: () => locks.keys }));
vi.mock("../../hooks/useSettings", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useSettings")>()),
	useSettings: () => ({ settings: { storageCompaction: true, providerEventRetentionDays: 14 }, isLoading: false, error: undefined }),
	useUpdateStorageCompaction: () => ({ update: vi.fn(), saving: false, error: undefined }),
}));

describe("StorageCompactionRows managed lock", () => {
	beforeEach(() => locks.keys.clear());

	it("makes the retention window read-only with the organization note when locked", () => {
		locks.keys.add("app.providerEventRetentionDays");
		render(<StorageCompactionRows />);
		expect(screen.getByTestId("agent-policy-lock")).toHaveTextContent("Managed by your organization");
		expect(screen.getByRole("button", { name: "Keep agent events of finished sessions" })).toBeDisabled();
	});

	it("leaves it editable when nothing is managed", () => {
		render(<StorageCompactionRows />);
		expect(screen.queryByTestId("agent-policy-lock")).toBeNull();
		expect(screen.getByRole("button", { name: "Keep agent events of finished sessions" })).toBeEnabled();
	});
});
