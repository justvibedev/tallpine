import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { NotificationDTO } from "../../lib/notifications";
import { InboxList } from "./InboxList";

const { navigateMock } = vi.hoisted(() => ({ navigateMock: vi.fn() }));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));
vi.mock("../NotificationCenter", () => ({
	NotificationCenter: ({ onSelect }: { onSelect: (notification: NotificationDTO) => void }): ReactNode => (
		<button onClick={() => onSelect({ id: "n7" } as NotificationDTO)} type="button">pick</button>
	),
}));

describe("InboxList", () => {
	it("navigates to /inbox with the picked notification as the item", async () => {
		render(<InboxList />);
		await userEvent.click(screen.getByRole("button", { name: "pick" }));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/inbox", search: { item: "n7" } });
	});
});
