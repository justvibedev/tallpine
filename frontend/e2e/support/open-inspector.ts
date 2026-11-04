import { expect, type Page } from "@playwright/test";

/** Opening a session leaves the inspector closed; open it for specs that exercise the rail. */
export async function openInspector(page: Page) {
	await page.getByTestId("sidebar-toggle").click();
	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	return inspector;
}

/** Browser and Plan switch from the top bar; clicking the active one would close the panel, so click only a different view. */
export async function selectPanelView(page: Page, id: "browser" | "plan") {
	const icon = page.getByTestId(`panel-switcher-${id}`);
	if ((await icon.getAttribute("aria-pressed")) !== "true") await icon.click();
	await expect(icon).toHaveAttribute("aria-pressed", "true");
}
