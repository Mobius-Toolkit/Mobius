import { expect, test, type Page } from "@playwright/test";
import { getSwitches, lines, turnAllOn, turnOff, type Kind } from "./switches.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test.afterEach(async ({ page }) => {
  await turnAllOn(page);
});

const line = (page: Page, kind: Kind) => page.getByText(lines[kind], { exact: true }).locator("..");

const notice = (page: Page, text: string) => page.getByRole("status").filter({ hasText: text });

const switchesRow = (page: Page) => page.getByRole("main").getByRole("link", { name: /^Switches/ });

const back = (page: Page) => page.getByRole("button", { name: "Back" }).filter({ visible: true });

// Fails the next request that turns the Start switch off. The switch stays on.
async function failStartSwitch(page: Page) {
  await page.route(
    "**/api/switches/start",
    (route) => route.fulfill({ status: 500, json: { error: "The switch did not change." } }),
    { times: 1 },
  );
  const toggle = page.getByRole("main").getByRole("switch", { name: "Start" });
  await expect(toggle).toBeChecked();
  const failed = page.waitForResponse(
    (res) => res.url().endsWith("/api/switches/start") && res.request().method() === "PUT",
  );
  await toggle.click();
  await failed;
  await expect(page.getByText("The switch did not change.")).toBeVisible();
}

for (const [kind, title] of [
  ["start", "Start"],
  ["merge", "Merge"],
  ["repairs", "Repairs"],
] as const) {
  test(`the ${title} switch on the Switches page turns the switch off, and Resume turns it on`, async ({
    page,
  }) => {
    await page.goto("/settings/switches");
    const toggle = page.getByRole("main").getByRole("switch", { name: title });
    await expect(toggle).toBeChecked();

    const off = page.waitForResponse(
      (res) => res.url().endsWith(`/api/switches/${kind}`) && res.request().method() === "PUT",
    );
    await toggle.click();
    await off;
    await expect(notice(page, `${title} is off`)).toBeVisible();
    await expect(toggle).not.toBeChecked();
    await expect(line(page, kind)).toBeVisible();
    await expect(line(page, kind).getByRole("button", { name: "0 held" })).toBeVisible();
    expect((await getSwitches(page)).data[kind]).toBe(false);

    const on = page.waitForResponse(
      (res) => res.url().endsWith(`/api/switches/${kind}`) && res.request().method() === "PUT",
    );
    await line(page, kind).getByRole("button", { name: "Resume", exact: true }).click();
    await on;
    await expect(notice(page, `${title} is on`)).toBeVisible();
    await expect(page.getByText(lines[kind], { exact: true })).toBeHidden();
    await expect(toggle).toBeChecked();
    expect((await getSwitches(page)).data[kind]).toBe(true);
  });
}

test("the held list shows the held issues of its switch and links to their Workstream", async ({
  page,
}) => {
  await turnOff(page, "start", [38, 39]);
  await turnOff(page, "merge", [42]);
  await page.goto("/settings");
  await expect(line(page, "start").getByRole("button", { name: "2 held" })).toBeVisible();
  await expect(line(page, "merge").getByRole("button", { name: "1 held" })).toBeVisible();

  await line(page, "start").getByRole("button", { name: "2 held" }).click();
  const sheet = page.getByRole("dialog", { name: lines.start });
  await expect(sheet.getByText("#38 Show the plan prices")).toBeVisible();
  await expect(sheet.getByText("#39 Send the plan receipts")).toBeVisible();
  await expect(sheet.getByText("Integrate loyalty plans")).toHaveCount(2);
  await expect(sheet.getByText("Let customers change plans")).toBeHidden();

  await sheet.getByRole("link", { name: /#38 Show the plan prices/ }).click();
  await expect(page).toHaveURL("/workstreams/owner/shop/12");
  await expect(sheet).toBeHidden();

  await line(page, "merge").getByRole("button", { name: "1 held" }).click();
  const merge = page.getByRole("dialog", { name: lines.merge });
  await expect(merge.getByText("#42 Let customers change plans")).toBeVisible();
  await expect(merge.getByText("#38")).toBeHidden();
});

test("the Settings page shows one line for each switch that is off", async ({ page }) => {
  await turnOff(page, "start");
  await turnOff(page, "repairs");
  await page.goto("/settings");

  await expect(line(page, "start")).toBeVisible();
  await expect(line(page, "repairs")).toBeVisible();
  await expect(page.getByText(lines.merge, { exact: true })).toBeHidden();
  await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(2);

  await line(page, "repairs").getByRole("button", { name: "Resume", exact: true }).click();
  await expect(line(page, "repairs")).toBeHidden();
  await expect(line(page, "start")).toBeVisible();
});

test("the line stays with the error text when Resume fails", async ({ page }) => {
  await turnOff(page, "start");
  await page.goto("/settings");
  await page.route(
    "**/api/switches/start",
    (route) => route.fulfill({ status: 500, json: { error: "The switch did not change." } }),
    { times: 1 },
  );

  const failed = page.waitForResponse(
    (res) => res.url().endsWith("/api/switches/start") && res.request().method() === "PUT",
  );
  await line(page, "start").getByRole("button", { name: "Resume", exact: true }).click();
  await failed;

  await expect(line(page, "start")).toBeVisible();
  await expect(page.getByText("The switch did not change.")).toBeVisible();
  await expect(notice(page, "Start is on")).toHaveCount(0);
  expect((await getSwitches(page)).data.start).toBe(false);
});

test("a new line of a switch shows no error text of an earlier failure", async ({ page }) => {
  await turnOff(page, "start");
  await page.goto("/settings");
  await page.route(
    "**/api/switches/start",
    (route) => route.fulfill({ status: 500, json: { error: "The switch did not change." } }),
    { times: 1 },
  );

  const failed = page.waitForResponse(
    (res) => res.url().endsWith("/api/switches/start") && res.request().method() === "PUT",
  );
  await line(page, "start").getByRole("button", { name: "Resume", exact: true }).click();
  await failed;
  await expect(page.getByText("The switch did not change.")).toBeVisible();

  await turnAllOn(page);
  await expect(page.getByText(lines.start, { exact: true })).toBeHidden();
  await turnOff(page, "start");

  await expect(line(page, "start")).toBeVisible();
  await expect(page.getByText("The switch did not change.")).toBeHidden();
});

test("a failed request after which the server has the new value leaves no error on a new line", async ({
  page,
}) => {
  await turnOff(page, "start");
  await page.goto("/settings");
  let answered = false;
  await page.route("**/api/switches/start", async (route) => {
    if (answered) {
      await route.continue();
      return;
    }
    answered = true;
    await route.fetch();
    await route.fulfill({ status: 500, json: { error: "The switch did not change." } });
  });

  const failed = page.waitForResponse(
    (res) => res.url().endsWith("/api/switches/start") && res.request().method() === "PUT",
  );
  await line(page, "start").getByRole("button", { name: "Resume", exact: true }).click();
  await failed;
  await expect(page.getByText(lines.start, { exact: true })).toBeHidden();

  await turnOff(page, "start");

  await expect(line(page, "start")).toBeVisible();
  await expect(page.getByText("The switch did not change.")).toBeHidden();
  await page.unrouteAll({ behavior: "wait" });
});

test("the Switches page shows the error of a switch under its row until the server gives a new value", async ({
  page,
}) => {
  await page.goto("/settings/switches");
  await page.route(
    "**/api/switches/start",
    (route) => route.fulfill({ status: 500, json: { error: "The switch did not change." } }),
    { times: 1 },
  );

  const failed = page.waitForResponse(
    (res) => res.url().endsWith("/api/switches/start") && res.request().method() === "PUT",
  );
  await page.getByRole("main").getByRole("switch", { name: "Start" }).click();
  await failed;

  await expect(page.getByText("The switch did not change.")).toBeVisible();
  await expect(notice(page, "Start is off")).toHaveCount(0);
  await expect(page.getByText(lines.start, { exact: true })).toBeHidden();

  await turnOff(page, "start");

  await expect(line(page, "start")).toBeVisible();
  await expect(page.getByText("The switch did not change.")).toBeHidden();
});

test("the Switches row of Settings shows All on when each switch is on", async ({ page }) => {
  await page.goto("/settings");

  await expect(switchesRow(page).getByText("All on", { exact: true })).toBeVisible();
  await expect(switchesRow(page).getByText("Error", { exact: true })).toHaveCount(0);
});

test("the Switches row of Settings shows the titles of the switches that are off", async ({
  page,
}) => {
  await turnOff(page, "start");
  await turnOff(page, "merge");
  await page.goto("/settings");

  await expect(switchesRow(page).getByText("Start off, Merge off", { exact: true })).toBeVisible();
});

test("the Switches row of Settings shows Error after a failed switch request", async ({ page }) => {
  await page.goto("/settings/switches");
  await failStartSwitch(page);
  await back(page).click();
  await expect(page).toHaveURL("/settings");

  await expect(switchesRow(page).getByText("All on", { exact: true })).toBeVisible();
  await expect(switchesRow(page).getByText("Error", { exact: true })).toBeVisible();
});

test("the Switches row of Settings shows no Error after the server gives a new value", async ({
  page,
}) => {
  await page.goto("/settings/switches");
  await failStartSwitch(page);
  await back(page).click();
  await expect(page).toHaveURL("/settings");
  await expect(switchesRow(page).getByText("Error", { exact: true })).toBeVisible();

  await turnOff(page, "start");

  await expect(switchesRow(page).getByText("Start off", { exact: true })).toBeVisible();
  await expect(switchesRow(page).getByText("Error", { exact: true })).toHaveCount(0);
});
