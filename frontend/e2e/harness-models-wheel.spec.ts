import { expect, test } from "@playwright/test";
import type { TallpineBridge } from "../src/preload";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

const MODELS = Array.from({ length: 20 }, (_, index) => ({ id: `model-${index + 1}`, label: `Model ${index + 1}`, efforts: [] }));

test("renderer: wheel over a long harness model list still scrolls the settings page (B572) @T0", async ({ page }) => {
	await page.setViewportSize({ width: 1100, height: 420 });
	await installFakeAgent(page);
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname.startsWith("/api/v1/agents/readiness")) await route.fulfill({ json: { agents: [agentReadiness("kiro", "Kiro")] } });
		else if (pathname === "/api/v1/agents/installers") await route.fulfill({ json: { agents: [] } });
		else if (pathname === "/api/v1/agents/install-jobs") await route.fulfill({ json: { jobs: [] } });
		else if (pathname === "/api/v1/agents/auth-plans") await route.fulfill({ json: { plans: [] } });
		else if (pathname === "/api/v1/agents/kiro/models" || pathname === "/api/v1/agents/kiro/models/refresh") await route.fulfill({ json: { agentId: "kiro", models: MODELS } });
		else if (pathname === "/api/v1/projects/clone/check") await route.fulfill({ json: { remoteUrl: "https://example.com/example.git", path: "/repos/example", exists: false, canUseExisting: false } });
		else if (pathname === "/api/v1/projects/clone/prepare") await route.fulfill({ json: { path: "/repos/example", remoteUrl: "https://example.com/example.git", preparationId: "prep-models" } });
		else if (pathname === "/api/v1/projects/clone/cleanup") await route.fulfill({ status: 204 });
		else if (pathname === "/api/v1/imports/validate") await route.fulfill({ json: { importKind: "project", isValid: true, blockingErrors: [], nextStep: "continue", root: { repoPath: "/repos/example", isRepo: true, hasCommit: true, hasOrigin: true, isEmptyFolder: false, needsGitInit: false, requiredActions: [], blockingErrors: [] } } });
		else await route.fulfill({ json: { healthy: true, chatNotAttached: [], rules: [], warnings: [] } });
	});
	await page.goto("/#/");
	await page.evaluate(() => {
		(window as unknown as { tallpine: TallpineBridge }).tallpine.app.chooseDirectory = async () => "/repos";
	});
	await page.getByRole("button", { name: "Clone a repo", exact: true }).click();
	await page.getByRole("textbox", { name: "Repository URL" }).fill("https://example.com/example.git");
	await page.getByRole("textbox", { name: "Clone into" }).fill("/repos");
	await page.getByRole("button", { name: "Continue", exact: true }).click();
	await page.getByRole("combobox", { name: "Worker agent", exact: true }).click();
	await page.getByRole("option", { name: /Manage agents/ }).click();
	await page.getByRole("button", { name: /Kiro/ }).first().click();
	await page.getByRole("tab", { name: "Models" }).click();
	const list = page.getByRole("list", { name: "Available models" });
	await expect(list).toBeVisible();
	const scroller = page.locator("[data-testid=settings-page] .settings-thin-scrollbar").first();
	const box = (await list.boundingBox())!;
	const viewport = page.viewportSize()!;
	// Real wheel events; the list scrolls under a pointer parked mid-window.
	await page.mouse.move(box.x + box.width / 2, viewport.height / 2);
	await expect.poll(async () => {
		await page.mouse.wheel(0, 400);
		return scroller.evaluate((e) => e.scrollHeight - e.clientHeight - e.scrollTop);
	}, { timeout: 10_000 }).toBeLessThanOrEqual(1);
	const pager = await page.getByText("1–10 of 20").boundingBox();
	expect(pager!.y + pager!.height).toBeLessThanOrEqual(viewport.height);
});
