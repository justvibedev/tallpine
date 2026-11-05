import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { subscribeBrowserOverlaySync } from "../../lib/browser-overlay-sync";
import {
	OptionMenu,
	OptionMenuContent,
	OptionMenuItem,
	OptionMenuSub,
	OptionMenuSubContent,
	OptionMenuSubTrigger,
	OptionMenuTrigger,
} from "./option-menu";

it("keeps option menus and submenus above the native browser until dismissed", async () => {
	const user = userEvent.setup();
	const setOverlayOpen = vi.spyOn(window.tallpine!.browser, "setOverlayOpen");
	const unsubscribe = subscribeBrowserOverlaySync();
	const onSelect = vi.fn();
	try {
		render(
			<OptionMenu>
				<OptionMenuTrigger>Turn settings</OptionMenuTrigger>
				<OptionMenuContent>
					<OptionMenuSub>
						<OptionMenuSubTrigger>Model</OptionMenuSubTrigger>
						<OptionMenuSubContent>
							<OptionMenuItem onSelect={onSelect}>Opus</OptionMenuItem>
						</OptionMenuSubContent>
					</OptionMenuSub>
				</OptionMenuContent>
			</OptionMenu>,
		);
		await user.click(screen.getByRole("button", { name: "Turn settings" }));
		await waitFor(() => expect(setOverlayOpen).toHaveBeenLastCalledWith(true));
		await user.keyboard("{ArrowDown}{ArrowRight}");
		const choice = await screen.findByRole("menuitem", { name: "Opus" });
		expect(choice.closest('[role="menu"]')).toHaveAttribute("data-browser-native-overlay", "true");
		expect(setOverlayOpen).not.toHaveBeenCalledWith(false);
		await user.click(choice);
		expect(onSelect).toHaveBeenCalledOnce();
		await waitFor(() => expect(setOverlayOpen).toHaveBeenLastCalledWith(false));
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
	} finally {
		unsubscribe();
		setOverlayOpen.mockRestore();
	}
});

it.each(["pointer", "keyboard"])("closes only the opted-in submenu after a %s selection", async (input) => {
	const user = userEvent.setup();
	const onSelect = vi.fn();
	render(
		<OptionMenu>
			<OptionMenuTrigger>Turn settings</OptionMenuTrigger>
			<OptionMenuContent>
				<OptionMenuSub closeOnSelect>
					<OptionMenuSubTrigger>Model</OptionMenuSubTrigger>
					<OptionMenuSubContent>
						<OptionMenuItem onSelect={onSelect}>Opus</OptionMenuItem>
						<OptionMenuItem onSelect={(event) => event.preventDefault()}>Other models</OptionMenuItem>
					</OptionMenuSubContent>
				</OptionMenuSub>
			</OptionMenuContent>
		</OptionMenu>,
	);
	await user.click(screen.getByRole("button", { name: "Turn settings" }));
	await user.keyboard("{ArrowDown}{ArrowRight}");
	fireEvent.click(await screen.findByRole("menuitem", { name: "Other models" }));
	expect(screen.getAllByRole("menu")).toHaveLength(2);
	const choice = screen.getByRole("menuitem", { name: "Opus" });
	if (input === "keyboard") {
		choice.focus();
		await user.keyboard("{Enter}");
	} else {
		fireEvent.click(choice);
	}
	expect(onSelect).toHaveBeenCalledOnce();
	expect(screen.getAllByRole("menu")).toHaveLength(1);
	expect(screen.queryByRole("menuitem", { name: "Opus" })).not.toBeInTheDocument();
	expect(screen.getByRole("menuitem", { name: "Model" })).toHaveFocus();
});
