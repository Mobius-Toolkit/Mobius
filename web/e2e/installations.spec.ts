import { expect, test } from "@playwright/test";
import { logIn } from "./login.js";

const desktop = { width: 1280, height: 800 };
const phone = { width: 390, height: 844 };

test("a change of the installations shows with no reload", async ({ page }) => {
  await logIn(page);
  const main = page.getByRole("main");
  const switcher = page.getByRole("button", { name: "owner" }).filter({ visible: true });
  const organization = page.getByRole("menuitemradio", { name: "acme" });
  const slow = { timeout: 30_000 };
  await expect(main.getByText("owner/shop")).toBeVisible();
  await page.evaluate("window.sameLoad = true");

  await page.request.post("/e2e/repositories/acme/orchard");

  await expect(main.getByText("acme/orchard")).toBeVisible(slow);
  await switcher.click();
  await expect(organization).toBeVisible();
  await page.keyboard.press("Escape");

  await page.request.delete("/e2e/repositories/acme/orchard");

  await expect(main.getByText("acme/orchard")).toBeHidden(slow);
  await switcher.click();
  await expect(page.getByRole("menuitemradio", { name: "plants" })).toBeVisible();
  await expect(organization).toBeHidden();
  expect(await page.evaluate("window.sameLoad")).toBe(true);
});

test("the App name follows the account until the user edits it", async ({ page }) => {
  await logIn(page);
  const account = page.getByLabel("Account or organization");
  const name = page.getByLabel("App name");

  await expect(name).toHaveValue("Mobius");

  await account.fill("acme");

  await expect(name).toHaveValue("Mobius acme");

  const long = "a".repeat(39);
  await account.fill(long);

  await expect(name).toHaveValue(`Mobius ${long}`.slice(0, 34));

  await name.fill("My App");
  await account.fill("plants");

  await expect(name).toHaveValue("My App");
});

// The menu zooms in while it opens, so only offsetWidth and offsetHeight give its real size.
const switchButton = (selector: string) =>
  `document.querySelector('${selector} button[aria-haspopup="menu"]')`;

test("the organization switch of a desktop fills the side bar and has the arrow at the right edge", async ({
  page,
}) => {
  await page.setViewportSize(desktop);
  await logIn(page);
  await page.goto("/chat");
  await expect(page.locator("nav").first().getByRole("button", { name: "owner" })).toBeVisible();
  const button = switchButton("nav");

  const [buttonWidth, boxWidth, edgeGap] = (await page.evaluate(`(() => {
    const button = ${button};
    const box = button.parentElement;
    const style = getComputedStyle(box);
    const arrow = button.querySelector("svg").getBoundingClientRect();
    return [
      button.offsetWidth,
      box.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight),
      button.getBoundingClientRect().right - arrow.right,
    ];
  })()`)) as number[];

  expect(buttonWidth).toBe(boxWidth);
  expect(edgeGap).toBeLessThan(16);
});

test("the menu of the organization switch of a desktop is wide and shows each name on one line", async ({
  page,
}) => {
  await page.setViewportSize(desktop);
  await logIn(page);
  await page.goto("/chat");
  const button = page.locator("nav").first().getByRole("button", { name: "owner" });
  await expect(button).toBeVisible();
  await page.request.post("/e2e/repositories/Mobius-Toolkit/garden");
  try {
    await button.click();
    await expect(page.getByRole("menuitemradio", { name: "Mobius-Toolkit" })).toBeVisible({
      timeout: 30_000,
    });

    const [menuWidth, heights] = (await page.evaluate(`[
      document.querySelector('[role="menu"]').offsetWidth,
      [...document.querySelectorAll('[role="menuitemradio"]')].map((item) => item.offsetHeight),
    ]`)) as [number, number[]];

    expect(menuWidth).toBeGreaterThanOrEqual(224);
    expect(heights.length).toBeGreaterThanOrEqual(3);
    expect(new Set(heights).size).toBe(1);
  } finally {
    await page.request.delete("/e2e/repositories/Mobius-Toolkit/garden");
  }
});

test("the organization switch of a phone keeps the width of its button and its menu", async ({
  page,
}) => {
  await page.setViewportSize(phone);
  await logIn(page);
  await page.goto("/chat");
  const button = page.locator("#content").getByRole("button", { name: "owner" });
  await expect(button).toBeVisible();
  expect(await page.evaluate(`${switchButton("#content")}.offsetWidth`)).toBe(358);

  await button.click();
  await expect(page.getByRole("menu")).toBeVisible();
  expect(await page.evaluate(`document.querySelector('[role="menu"]').offsetWidth`)).toBe(358);
});

for (const [device, viewport] of [
  ["desktop", desktop],
  ["phone", phone],
] as const) {
  test(`a tap on an organization closes the organization menu of a ${device}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await logIn(page);
    await page.goto("/chat");
    const switcher = (name: string) =>
      page.locator('button[aria-haspopup="menu"]').filter({ visible: true, hasText: name });
    const menu = page.getByRole("menu");
    const choose = async (current: string, chosen: string) => {
      await switcher(current).click();
      await page.getByRole("menuitemradio", { name: chosen }).click();
      await expect(menu).toBeHidden();
      await expect(switcher(chosen)).toBeVisible();
    };
    await expect(switcher("owner")).toBeVisible();

    try {
      await choose("owner", "plants");
      await choose("plants", "plants");
    } finally {
      await choose("plants", "owner");
    }
  });
}

test("each row of the organization menu of a phone is at least 44 px high", async ({ page }) => {
  await page.setViewportSize(phone);
  await logIn(page);
  await page.goto("/chat");
  await page.locator("#content").getByRole("button", { name: "owner" }).click();
  await expect(page.getByRole("menuitemradio", { name: "plants" })).toBeVisible();

  const heights = (await page.evaluate(
    `[...document.querySelectorAll('[role="menuitemradio"]')].map((item) => item.offsetHeight)`,
  )) as number[];

  expect(heights.length).toBeGreaterThanOrEqual(2);
  expect(Math.min(...heights)).toBeGreaterThanOrEqual(44);
});
