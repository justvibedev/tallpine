import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

/** Opens the three-way Auto review menu and picks a choice. */
export async function chooseAutoReview(choice: string) {
	await userEvent.click(await screen.findByLabelText("Auto review PRs"));
	await userEvent.click(await screen.findByRole("menuitem", { name: choice }));
}
