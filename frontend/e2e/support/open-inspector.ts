import { expect, type Page } from "@playwright/test";

/** Opening a session leaves the inspector closed; open it for specs that exercise the rail. */
export async function openInspector(page: Page) {
	await page.getByTestId("sidebar-toggle").click();
	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	return inspector;
}

/** Browser is a tab in the open panel; Plan switches from the top bar, where clicking the active one would close the panel. */
export async function selectPanelView(page: Page, id: "browser" | "plan") {
	if (id === "browser") {
		const toggle = page.getByTestId("sidebar-toggle");
		if ((await toggle.getAttribute("aria-pressed")) !== "true") await toggle.click();
		const tab = page.locator("#inspector").getByRole("tab", { name: "Browser" });
		await tab.click();
		await expect(tab).toHaveAttribute("aria-selected", "true");
		return;
	}
	const icon = page.getByTestId(`panel-switcher-${id}`);
	if ((await icon.getAttribute("aria-pressed")) !== "true") await icon.click();
	await expect(icon).toHaveAttribute("aria-pressed", "true");
}
