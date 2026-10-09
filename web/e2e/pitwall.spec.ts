import { expect, test } from "@playwright/test";

// Full chain: Go server + built frontend + WebSocket + Monte Carlo + live race.
test("strategy lab converges, then the live race runs", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/?seed=E2E-1");
  await expect(page.locator(".event .name")).not.toHaveText(/Chargement/, { timeout: 15_000 });
  await expect(page.locator(".led.open")).toBeVisible();

  // two strategies, run the comparison
  await page.locator(".strat-compact").first().fill("M-25-H");
  await page.getByRole("button", { name: "Simuler" }).click();
  const counter = page.locator(".counter .big");
  await expect(counter).not.toHaveText("0", { timeout: 15_000 });
  await expect(page.locator(".counter .sub")).toContainText("terminé", { timeout: 30_000 });
  await expect(page.locator(".cmp tbody tr")).toHaveCount(2);
  await expect(page.locator(".verdict")).toContainText("Recommandation");

  // an invalid plan is refused client-side with a clear message
  await page.locator(".strat-compact").first().fill("M-0-H");
  await expect(page.locator(".err").first()).toContainText("tour 1");

  // live race
  await page.locator(".strat-compact").first().fill("M-25-H");
  await page.getByRole("button", { name: /Course avec/ }).click();
  await expect(page.locator(".tab[aria-selected=true]")).toContainText("Live Race");
  await expect(page.locator(".tower-row .last").first()).toHaveText(/\d:\d\d\.\d{3}/, { timeout: 20_000 });
  await page.screenshot({ path: "test-results/live.png" });
  expect(errors).toEqual([]);
});

test("same seed, same race: the URL is shareable", async ({ page }) => {
  await page.goto("/?seed=SHARE-7&cars=12");
  await expect(page.locator(".event .name")).not.toHaveText(/Chargement/, { timeout: 15_000 });
  const name1 = await page.locator(".event .name").textContent();
  await page.reload();
  await expect(page.locator(".event .name")).toHaveText(name1 ?? "", { timeout: 15_000 });
  await expect(page.locator(".tower-row")).toHaveCount(12);
});

test("garbage seed in the URL falls back safely", async ({ page }) => {
  await page.goto("/?seed=%3Cscript%3Ealert(1)%3C/script%3E&cars=9999");
  await expect(page.locator(".event .name")).not.toHaveText(/Chargement/, { timeout: 15_000 });
  await expect(page.locator(".tower-row")).toHaveCount(10);
});
