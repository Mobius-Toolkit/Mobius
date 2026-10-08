import { expect, type Page, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("back and forward move between the pages", async ({ page }) => {
  const main = page.getByRole("main");
  const link = (path: string) => page.locator(`a[href="${path}"]`).filter({ visible: true });

  await page.goto("/workstreams");
  await expect(main.getByText("Integrate loyalty plans")).toBeVisible();
  await link("/inbox").click();
  await expect(page).toHaveURL("/inbox");
  await expect(main.getByText("Inbox", { exact: true })).toBeVisible();
  await link("/inbox/activity").click();
  await expect(page).toHaveURL("/inbox/activity");
  await expect(main.getByRole("tab", { name: "Activity" })).toHaveAttribute(
    "aria-selected",
    "true",
  );

  await page.goBack();
  await expect(page).toHaveURL("/inbox");
  await expect(main.getByText("Inbox", { exact: true })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL("/workstreams");
  await expect(main.getByText("Integrate loyalty plans")).toBeVisible();
  await page.goForward();
  await expect(page).toHaveURL("/inbox");
  await expect(main.getByText("Inbox", { exact: true })).toBeVisible();
});

test("the start page and an unknown path open the Triager chat", async ({ page }) => {
  for (const path of [
    "/",
    "/nowhere",
    "/activity",
    "/workstreams/new",
    "/workstreams/owner/shop/abc",
    "/workstreams/owner",
  ]) {
    await page.goto(path);
    await expect(page).toHaveURL("/chat");
    await expect(page.getByLabel("Message to the Triager")).toBeVisible();
  }
});

test("the tab bar of a phone shows the tabs in order, and the Chat tab shows the unread count", async ({
  page,
}) => {
  const tabs = page.getByRole("navigation").last().getByRole("link");

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/workstreams");
  await expect(tabs).toHaveText([/^Chat$/, /^Workstreams$/, /^Inbox\s*2$/, /^Settings$/]);

  await page.addInitScript("localStorage.setItem('organization', 'plants')");
  await page.goto("/workstreams");
  await expect(tabs).toHaveText([/^Chat\s*1$/, /^Workstreams$/, /^Inbox$/, /^Settings$/]);
});

test("the sidebar of a desktop shows Chat and Inbox above the Workstreams", async ({ page }) => {
  const links = page.getByRole("navigation").first().getByRole("link");

  await page.goto("/workstreams");
  await expect(links.nth(0)).toHaveText(/^Chat$/);
  await expect(links.nth(1)).toHaveText(/^Inbox\s*2$/);
  await expect(links.nth(2)).toHaveText(/^Workstreams$/);
  await expect(links.filter({ hasText: /^Activity$/ })).toHaveCount(0);
  await expect(links.filter({ hasText: /^New Workstream$/ })).toHaveCount(0);

  await page.addInitScript("localStorage.setItem('organization', 'plants')");
  await page.goto("/workstreams");
  await expect(links.nth(0)).toHaveText(/^Chat\s*1$/);
});

test("the Inbox page has a tab for each address, and a reload keeps the tab", async ({ page }) => {
  const main = page.getByRole("main");
  const todo = main.getByRole("tab", { name: /^To do/ });
  const activity = main.getByRole("tab", { name: "Activity" });

  await page.goto("/inbox");
  await expect(todo).toHaveText(/^To do\s*2$/);
  await expect(todo).toHaveAttribute("aria-selected", "true");
  await expect(main.getByText("#45 needs a decision")).toBeVisible();

  await activity.click();
  await expect(page).toHaveURL("/inbox/activity");
  await expect(activity).toHaveAttribute("aria-selected", "true");
  await expect(main.getByRole("button", { name: "Integrate loyalty plans" })).toBeVisible();
  await expect(main.getByText("#45 needs a decision")).toHaveCount(0);

  await page.reload();
  await expect(page).toHaveURL("/inbox/activity");
  await expect(activity).toHaveAttribute("aria-selected", "true");
  await expect(main.getByRole("button", { name: "Integrate loyalty plans" })).toBeVisible();

  await page.goBack();
  await expect(page).toHaveURL("/inbox");
  await expect(todo).toHaveAttribute("aria-selected", "true");
  await expect(main.getByText("#45 needs a decision")).toBeVisible();
});

test("a click on a link of the app opens its page with no page load", async ({ page }) => {
  const main = page.getByRole("main");
  const link = (path: string) => page.locator(`a[href="${path}"]`).filter({ visible: true });
  const loaded = () => page.evaluate("window.loaded === true");

  await page.goto("/workstreams/plants/garden/16");
  await expect(main.getByText("Note 12 of #16.")).toBeVisible();
  await page.evaluate("window.loaded = true");

  // The side bar of a desktop.
  await link("/inbox").click();
  await expect(page).toHaveURL("/inbox");
  await expect(main.getByText("Inbox", { exact: true })).toBeVisible();
  await link("/workstreams/plants/garden/16").click();
  await expect(page).toHaveURL("/workstreams/plants/garden/16");
  await expect(main.getByText("Note 12 of #16.")).toBeVisible();

  // The tabs and the Settings links of a phone.
  await page.setViewportSize({ width: 390, height: 844 });
  await link("/settings").click();
  await expect(page).toHaveURL("/settings");
  await link("/settings/checkup").click();
  await expect(page).toHaveURL("/settings/checkup");
  await expect(main.getByText("Checkup", { exact: true })).toBeVisible();
  await link("/workstreams").click();
  await expect(page).toHaveURL("/workstreams");
  await link("/workstreams/plants/garden/17").click();
  await expect(page).toHaveURL("/workstreams/plants/garden/17");
  await expect(main.getByText("Note 12 of #17.")).toBeVisible();
  expect(await loaded()).toBe(true);

  await page.goBack();
  await expect(page).toHaveURL("/workstreams");
  await page.goBack();
  await expect(page).toHaveURL("/settings/checkup");
  await expect(main.getByText("Checkup", { exact: true })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL("/settings");
  expect(await loaded()).toBe(true);
});

test("a link marks only its own page as the current page", async ({ page }) => {
  await page.goto("/chat");
  const current = page.locator('[aria-current="page"]');
  await expect(current.filter({ visible: true })).toHaveText(["Chat"]);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(current.filter({ visible: true })).toHaveText(["Chat"]);
  await page.goto("/settings/checkup");
  await expect(current.filter({ visible: true })).toHaveCount(0);
});

test("a click on another chat shows no state of the previous chat", async ({ page }) => {
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Lead");

  await page.goto("/workstreams/plants/garden/16");
  await expect(main.getByText("Note 12 of #16.")).toBeVisible();
  await input.fill("A draft for #16.");
  await page.locator('nav a[href="/workstreams/plants/garden/17"]').click();
  await expect(page).toHaveURL("/workstreams/plants/garden/17");
  await expect(main.getByText("Note 12 of #17.")).toBeVisible();
  await expect(main.getByText("Note 12 of #16.")).toHaveCount(0);
  await expect(input).toHaveValue("");
});

test("a long list of release changes scrolls inside the upgrade dialog on a phone", async ({
  page,
}) => {
  const changes = Array.from({ length: 60 }, (_, index) => `Change number ${index + 1}`);
  await page.route("/api/release/changes", (route) => route.fulfill({ json: { data: changes } }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/workstreams");
  await page.getByRole("button", { name: "Upgrade v0.1.4" }).filter({ visible: true }).click();

  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Change number 1", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Upgrade" })).toBeInViewport({ ratio: 1 });
  await expect(dialog.getByRole("button", { name: "Close" })).toBeInViewport({ ratio: 1 });
  await expect(dialog.getByText("Change number 60", { exact: true })).not.toBeInViewport();

  await dialog.getByText("Change number 60", { exact: true }).scrollIntoViewIfNeeded();
  await expect(dialog.getByText("Change number 60", { exact: true })).toBeInViewport({
    ratio: 1,
  });
  await expect(dialog.getByRole("button", { name: "Upgrade" })).toBeInViewport({ ratio: 1 });

  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
});

test("the upgrade dialog shows the newest release when it opens", async ({ page }) => {
  let version = "v0.1.4";
  await page.route("**/api/release", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { version: string } };
    body.data.version = version;
    await route.fulfill({ response, json: body });
  });
  await page.goto("/workstreams");
  const button = page.getByRole("button", { name: "Upgrade v0.1.4" }).filter({ visible: true });
  await expect(button).toBeVisible();

  version = "v0.1.5";
  await button.click();

  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Upgrade to v0.1.5" })).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
  await expect(
    page.getByRole("button", { name: "Upgrade v0.1.5" }).filter({ visible: true }),
  ).toBeVisible();
});

test("a new page starts at the top", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 200 });
  await page.goto("/workstreams");
  await expect(page.getByRole("main").getByText("Workstreams")).toBeVisible();
  await page.evaluate("window.scrollTo(0, document.body.scrollHeight)");
  expect(await page.evaluate("window.scrollY")).toBeGreaterThan(0);
  await page.getByRole("link", { name: "Inbox" }).filter({ visible: true }).click();
  await expect(page).toHaveURL("/inbox");
  expect(await page.evaluate("window.scrollY")).toBe(0);
});

const back = (page: Page) => page.getByRole("button", { name: "Back" });

test.describe("the back button of a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("goes back to the previous page of the app", async ({ page }) => {
    await page.goto("/workstreams");
    await page.locator('a[href="/workstreams/owner/shop/46"]').filter({ visible: true }).click();
    await expect(page).toHaveURL("/workstreams/owner/shop/46");
    await back(page).click();
    await expect(page).toHaveURL("/workstreams");
    await page.goForward();
    await expect(page).toHaveURL("/workstreams/owner/shop/46");
  });

  test("opens the parent page after a direct open", async ({ page }) => {
    const main = page.getByRole("main");

    for (const path of ["/workstreams/owner/shop/46"]) {
      await page.goto(path);
      await expect(back(page)).toBeVisible();
      await back(page).click();
      await expect(page).toHaveURL("/workstreams");
      await expect(main.getByText("Integrate loyalty plans")).toBeVisible();
      await page.goBack();
      await expect(page).not.toHaveURL(path);
    }
  });

  test("is not on a tab page", async ({ page }) => {
    for (const path of ["/chat", "/workstreams", "/inbox", "/inbox/activity", "/settings"]) {
      await page.goto(path);
      await expect(page.getByRole("navigation").last()).toBeVisible();
      await expect(back(page)).toHaveCount(0);
    }
  });
});

test("the desktop layout has no back button", async ({ page }) => {
  await page.goto("/workstreams/plants/garden/17");
  await expect(page.getByRole("main").getByText("Note 12 of #17.")).toBeVisible();
  await expect(back(page)).toBeHidden();
});

test.describe("the back button of a settings page on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("goes back to the Settings after a click on its link", async ({ page }) => {
    await page.goto("/settings");
    await page.getByRole("main").locator('a[href="/devices"]').click();
    await expect(page).toHaveURL("/devices");
    await back(page).click();
    await expect(page).toHaveURL("/settings");
    await page.goForward();
    await expect(page).toHaveURL("/devices");
  });

  test("opens the Settings after a direct open", async ({ page }) => {
    for (const path of ["/github", "/devices", "/settings/checkup", "/agents"]) {
      await page.goto(path);
      await back(page).click();
      await expect(page).toHaveURL("/settings");
      await expect(page.getByRole("main").locator('a[href="/devices"]')).toBeVisible();
    }
  });
});

test("a switch to another organization leaves the chat of the old organization", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const organization = (name: string) =>
    page.getByRole("button", { name }).filter({ visible: true });

  await page.goto("/workstreams/plants/garden/16");
  await expect(main.getByText("Note 12 of #16.")).toBeVisible();
  await organization("plants").click();
  await page.getByRole("menuitemradio", { name: "owner" }).click();
  await expect(page).toHaveURL("/workstreams");
  await expect(main.getByText("Integrate loyalty plans")).toBeVisible();
  await expect(main.getByText("Cut the roses")).toHaveCount(0);

  await page.goto("/inbox");
  await organization("owner").click();
  await page.getByRole("menuitemradio", { name: "plants" }).click();
  await expect(page).toHaveURL("/inbox");
});

test("a Workstream with Autopilot on shows the Autopilot icon", async ({ page }) => {
  const main = page.getByRole("main");
  const row = (title: string) => main.getByRole("link").filter({ hasText: title });

  await page.goto("/workstreams");
  await expect(row("Early renewals").getByRole("img", { name: "Autopilot" })).toBeVisible();
  await expect(row("Early renewals")).toContainText("#14");
  await expect(row("Integrate loyalty plans")).toContainText("#12");
  await expect(row("Integrate loyalty plans").getByRole("img", { name: "Autopilot" })).toHaveCount(
    0,
  );
});

test("a Workstream with an agent that works shows the dot, and a Workstream with no agent does not", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const row = (title: string) => main.getByRole("link").filter({ hasText: title });
  const dot = (title: string) => row(title).getByRole("img", { name: "Agent running" });

  await page.goto("/workstreams");
  await expect(dot("Integrate loyalty plans")).toBeVisible();
  await expect(row("Early renewals")).toBeVisible();
  await expect(dot("Early renewals")).toHaveCount(0);
});

test("a Workstream with only a paused agent shows no dot", async ({ page }) => {
  const main = page.getByRole("main");
  await page.route("**/api/agents", (route) =>
    route.fulfill({
      json: {
        data: {
          count: 1,
          max: 8,
          groups: [
            {
              name: "Implementer",
              count: 1,
              max: 3,
              agents: [
                {
                  agent: {
                    repository: "owner/shop",
                    workstream: 14,
                    queueReason: "paused until 2026-09-28 12:00 UTC",
                    working: false,
                  },
                },
              ],
            },
          ],
        },
      },
    }),
  );

  await page.goto("/workstreams");
  await expect(main.getByRole("link").filter({ hasText: "Early renewals" })).toBeVisible();
  await expect(main.getByRole("img", { name: "Agent running" })).toHaveCount(0);
});

test("a Workstream with a pull request that waits for the merge shows the merge icon", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const row = (title: string) => main.getByRole("link").filter({ hasText: title });

  await page.goto("/workstreams");
  await expect(row("Early renewals").getByRole("img", { name: "Ready to merge" })).toBeVisible();
  await expect(row("Integrate loyalty plans")).toContainText("#12");
  await expect(
    row("Integrate loyalty plans").getByRole("img", { name: "Ready to merge" }),
  ).toHaveCount(0);
});

test("the agents page shows the start time of an agent after the repository", async ({ page }) => {
  await page.goto("/agents");
  await expect(
    page
      .getByRole("main")
      .getByRole("button", { name: /Ticket #41 Add plan model/ })
      .getByText(/owner\/shop · Sep \d+, \d\d:\d\d [AP]M$/),
  ).toBeVisible();
});

test("the agents screen shows a status that changed while the page was hidden", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const ticket = main.getByRole("button", { name: /Ticket #41 Add plan model/ });
  const setVisibility = (state: "hidden" | "visible") =>
    page.evaluate(`(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => ${state === "hidden"} })
      document.dispatchEvent(new Event('visibilitychange'))
    })()`);

  await page.goto("/agents");
  await expect(ticket).toBeVisible();
  await expect(ticket.getByText("waits for the Owner")).toHaveCount(0);

  await setVisibility("hidden");
  const res = await page.request.put(
    "/e2e/agents/41/queue-reason?reason=waits%20for%20the%20Owner",
  );
  expect(res.ok()).toBe(true);
  await setVisibility("visible");
  await expect(ticket.getByText("waits for the Owner")).toBeVisible();
  const reset = await page.request.put("/e2e/agents/41/queue-reason?reason=");
  expect(reset.ok()).toBe(true);
});
