import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { ArtifactStorageRow } from "./ArtifactStorageRow";

const { navigate, closeSettings } = vi.hoisted(() => ({ navigate: vi.fn(), closeSettings: vi.fn() }));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
vi.mock("../../stores/ui-store", () => ({
	useUiStore: (select: (state: { closeSettings: () => void }) => unknown) => select({ closeSettings }),
}));

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	navigate.mockReset();
	closeSettings.mockReset();
});

describe("ArtifactStorageRow", () => {
	it("only links to the Artifacts tab", async () => {
		render(<ArtifactStorageRow />);
		await userEvent.setup().click(screen.getByRole("button", { name: "Open Artifacts" }));
		expect(closeSettings).toHaveBeenCalledTimes(1);
		expect(navigate).toHaveBeenCalledWith({ to: "/artifacts" });
	});
});
