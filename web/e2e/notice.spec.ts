import { expect, test, type Locator, type Page } from "@playwright/test";
import { leadActionItem } from "./stops.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const link = "https://github.com/owner/shop";
const shop = "/workstreams/owner/shop/12";

const base = {
  dismissedAt: null,
  questions: [],
  organization: "owner",
  repository: "owner/shop",
  workstreamRepository: "owner/shop",
  workstream: 12,
  time: "2026-09-28T09:30:00Z",
  title: "",
  reason: "",
  link: "",
  pullRequest: 0,
  pullRequestUrl: "",
  reportUrl: "",
};

const question = {
  ...base,
  id: 1,
  issue: 45,
  kind: "question",
  title: "Pick the plan limits",
  reason: "The Lead asks a question",
  text: "Which seat limit?",
  link: `${link}/issues/45`,
};

const readyItem = {
  ...base,
  id: 4,
  issue: 30,
  kind: "ready for review",
  title: "Name the plan tiers",
  reason: "The pull request waits for your approval",
  text: "The pull request waits.",
  link: `${link}/issues/30`,
  pullRequest: 44,
  pullRequestUrl: `${link}/pull/44`,
  merging: false,
  mergeRefusal: "",
};

const routeInbox = (page: Page, list: object[]) =>
  page.route("**/api/inbox", (route) => route.fulfill({ json: { data: list } }));

const notice = (page: Page, text: string) => page.getByRole("status").filter({ hasText: text });

async function routeWorkstreamsClosed(page: Page) {
  await page.route("**/api/workstreams", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data: { repository: string; number: number; allTasksClosed: boolean }[];
    };
    for (const workstream of body.data) {
      if (workstream.repository === "owner/shop" && workstream.number === 12) {
        workstream.allTasksClosed = true;
      }
    }
    await route.fulfill({ response, json: body });
  });
}

test("a merge shows its notice for 4 seconds", async ({ page }) => {
  await page.clock.install();
  await routeInbox(page, [readyItem]);
  await page.route("**/api/inbox/4/merge", (route) =>
    route.fulfill({ json: { data: { merging: true } } }),
  );
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Merge" }).click();
  const merged = notice(page, "Started the merge of PR #44");
  await expect(merged).toBeVisible();
  await page.clock.fastForward(3500);
  await expect(merged).toBeVisible();
  await page.clock.fastForward(1000);
  await expect(merged).toBeHidden();
});

test("a refused merge shows its error and no notice", async ({ page }) => {
  await routeInbox(page, [readyItem]);
  await page.route("**/api/inbox/4/merge", (route) =>
    route.fulfill({ status: 409, json: { error: "The task of #30 is not ready for review." } }),
  );
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Merge" }).click();
  await expect(page.getByText("The task of #30 is not ready for review.")).toBeVisible();
  await expect(notice(page, "Started the merge")).toHaveCount(0);
});

test("the answer of a Lead question shows its notice", async ({ page }) => {
  await routeInbox(page, [question]);
  await page.route("**/api/inbox/1/answer", (route) =>
    route.fulfill({ json: { data: { url: `${link}/issues/45#issuecomment-1` } } }),
  );
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Answer" }).click();
  const panel = page.getByRole("dialog");
  await panel.getByRole("textbox", { name: "Your answer" }).fill("Ten seats.");
  await panel.getByRole("button", { name: "Send" }).click();

  await expect(panel).toHaveCount(0);
  await expect(notice(page, "Sent the answer to the Lead of #12")).toBeVisible();
});

test("the approval of an action shows its notice", async ({ page }) => {
  await routeInbox(page, [leadActionItem]);
  await page.route("**/api/actions/78/approve", (route) =>
    route.fulfill({
      json: { data: { ...leadActionItem.action, state: "done", result: "Changed the Brief." } },
    }),
  );
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Answer" }).click();
  const panel = page.getByRole("dialog");
  await panel.getByRole("button", { name: "Approve" }).click();

  await expect(panel).toHaveCount(0);
  await expect(notice(page, `Approved: ${leadActionItem.title}`)).toBeVisible();
});

test("a failed approval keeps the panel with its error and shows no notice", async ({ page }) => {
  await routeInbox(page, [leadActionItem]);
  await page.route("**/api/actions/78/approve", (route) =>
    route.fulfill({ json: { data: { ...leadActionItem.action, error: "GitHub refused it." } } }),
  );
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Answer" }).click();
  const panel = page.getByRole("dialog");
  await panel.getByRole("button", { name: "Approve" }).click();

  await expect(panel.getByRole("alert")).toHaveText("GitHub refused it.");
  await expect(notice(page, "Approved")).toHaveCount(0);
});

test("a new level of the Autopilot shows its notice", async ({ page }) => {
  await page.route("**/api/workstreams/owner/shop/12/autopilot", (route) =>
    route.fulfill({ status: 204 }),
  );
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(shop);

  await page.getByRole("button", { name: "Autopilot: Off" }).click();
  await page.getByRole("menuitemradio", { name: /^Merge only/ }).click();

  await expect(notice(page, "Autopilot of #12: Merge only")).toBeVisible();
});

test("the close of a Workstream shows its notice on the next page", async ({ page }) => {
  await routeWorkstreamsClosed(page);
  await page.route("**/api/workstreams/owner/shop/12/complete", (route) =>
    route.fulfill({ status: 204 }),
  );
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(shop);

  await page.getByRole("main").getByRole("button", { name: "Close Workstream" }).click();
  await expect(notice(page, "Closed the Workstream #12")).toBeVisible();
  await page.locator('nav a[href="/workstreams"]').filter({ visible: true }).click();
  await expect(page).toHaveURL("/workstreams");
  await expect(notice(page, "Closed the Workstream #12")).toBeVisible();
});

test("a new notice replaces the open notice", async ({ page }) => {
  await page.route("**/api/workstreams/owner/shop/12/autopilot", (route) =>
    route.fulfill({ status: 204 }),
  );
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(shop);

  await page.getByRole("button", { name: "Autopilot: Off" }).click();
  await page.getByRole("menuitemradio", { name: /^Merge only/ }).click();
  await expect(notice(page, "Autopilot of #12: Merge only")).toBeVisible();
  await page.getByRole("button", { name: "Autopilot: Merge only" }).click();
  await page.getByRole("menuitemradio", { name: /^Start tasks/ }).click();

  await expect(notice(page, "Autopilot of #12: Start tasks")).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Autopilot of" })).toHaveCount(1);
});

const overlaps = async (a: Locator, b: Locator) => {
  const first = (await a.boundingBox())!;
  const second = (await b.boundingBox())!;
  return (
    first.x < second.x + second.width &&
    second.x < first.x + first.width &&
    first.y < second.y + second.height &&
    second.y < first.y + first.height
  );
};

test("on a phone the notice does not cover the bottom bar or the message field", async ({
  page,
}) => {
  await page.route("**/api/workstreams/owner/shop/12/autopilot", (route) =>
    route.fulfill({ status: 204 }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(shop);

  await page.getByRole("button", { name: "Autopilot: Off" }).click();
  await page
    .getByRole("dialog", { name: "Autopilot" })
    .getByRole("radio", { name: /^Start tasks/ })
    .click();
  const shown = notice(page, "Autopilot of #12: Start tasks");
  await expect(shown).toBeVisible();

  const bottomBar = page
    .locator("nav")
    .filter({ has: page.getByRole("link", { name: "Inbox" }) })
    .last();
  expect(await overlaps(shown, bottomBar)).toBe(false);
  expect(await overlaps(shown, page.getByLabel("Message to the Lead"))).toBe(false);
  const box = (await shown.boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(390);
});
