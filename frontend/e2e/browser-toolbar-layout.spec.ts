import { expect, test, type Locator, type Page } from "@playwright/test";
import { openInspector, selectPanelView } from "./support/open-inspector";

// The Browser panel's chrome is one toolbar row under the top bar; the panel
// switcher lives in the top bar, so nothing may overlap at any panel width.

type Box = { x: number; y: number; width: number; height: number };

const overlaps = (a: Box, b: Box) =>
	a.x < b.x + b.width - 0.5 && b.x < a.x + a.width - 0.5 && a.y < b.y + b.height - 0.5 && b.y < a.y + a.height - 0.5;

async function boxes(locators: Locator[]) {
	const result: Box[] = [];
	for (const locator of locators) {
		const box = await locator.boundingBox();
		if (box) result.push(box);
	}
	return result;
}

async function expectNoOverlap(page: Page) {
	const toolbar = page.getByTestId("browser-tab-row");
	const controls = [
		page.getByRole("button", { name: "Back" }),
		page.getByRole("button", { name: "Forward" }),
		page.getByRole("button", { name: "Reload" }),
		page.getByTestId("browser-address-bar"),
		page.getByRole("button", { name: "Open new tab" }),
		page.getByRole("button", { name: "Browser controls" }),
		...(await page.getByTestId("panel-switcher").getByRole("button").all()),
		page.getByTestId("sidebar-toggle"),
	];
	const measured = await boxes(controls);
	expect(measured.length).toBe(controls.length);
	for (let i = 0; i < measured.length; i++) {
		for (let j = i + 1; j < measured.length; j++) {
			expect(overlaps(measured[i], measured[j]), `controls ${i} and ${j} overlap`).toBe(false);
		}
	}
	// The field is control height (28px), not the taller inspector-tabs height.
	const field = await page.getByTestId("browser-address-bar").boundingBox();
	expect(field?.height).toBeLessThanOrEqual(28.5);
	// One row: the toolbar's controls share its vertical band, and the switcher sits above it.
	const row = await toolbar.boundingBox();
	const switcher = await page.getByTestId("panel-switcher").boundingBox();
	if (!row || !switcher) throw new Error("toolbar or switcher not visible");
	expect(switcher.y + switcher.height).toBeLessThanOrEqual(row.y + 0.5);
}

test("@P0 browser toolbar is one row and clear of the top-bar switcher at every panel width", async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 720 });
	await page.goto("/#/projects/tallpine-demo/sessions/demo-working");
	await openInspector(page);
	await selectPanelView(page, "browser");
	await expect(page.getByTestId("browser-address-bar")).toBeVisible();
	// One tab: no tab strip.
	await expect(page.getByTestId("browser-tabs-row")).toHaveCount(0);

	for (const width of [1440, 1100, 960]) {
		await page.setViewportSize({ width, height: 720 });
		await expectNoOverlap(page);
	}
});
