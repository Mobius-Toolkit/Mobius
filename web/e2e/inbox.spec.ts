import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const link = "https://github.com/owner/shop";

const base = {
  dismissedAt: null,
  organization: "owner",
  repository: "owner/shop",
  workstream: 12,
  time: "2026-09-28T09:30:00Z",
  title: "",
  reason: "",
  link: "",
  pullRequest: 0,
  pullRequestUrl: "",
  reportUrl: "",
};

const items = [
  {
    ...base,
    id: 1,
    issue: 45,
    kind: "question",
    title: "Pick the plan limits",
    reason: "The Lead asks a question",
    text: "Which seat limit?",
    link: `${link}/issues/45`,
  },
  {
    ...base,
    id: 2,
    issue: 42,
    kind: "stopped",
    title: "Let customers change plans",
    reason: "The session hung three times",
    text: "Mobius stopped the session at 2026-09-28 09:30 UTC.",
    link: `${link}/issues/42`,
  },
  {
    ...base,
    id: 3,
    issue: 41,
    kind: "lost",
    title: "Add plan model",
    reason: "Mobius lost the task",
    text: "Mobius lost the task.",
    link: `${link}/issues/41`,
  },
  {
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
    reportUrl: `${link}/pull/44#issuecomment-1`,
  },
  {
    ...base,
    id: 5,
    issue: 12,
    kind: "Lead failed",
    reason: "The Lead did not get your message",
    text: "Start the work.",
  },
  {
    ...base,
    id: 6,
    issue: 46,
    kind: "full disk",
    reason: "The local check waits for free disk",
    text: "The disk is full.",
  },
];

async function routeInbox(page: Page, list: object[]) {
  await page.route("**/api/inbox", (route) => route.fulfill({ json: { data: list } }));
}

test("each item shows on one line with the action of its kind, oldest first", async ({ page }) => {
  await routeInbox(page, items.toReversed());
  await page.goto("/inbox");
  const rows = page.getByRole("main").getByRole("listitem");

  await expect(rows).toHaveCount(6);
  await expect(rows.nth(0)).toContainText("#45Pick the plan limits");
  await expect(rows.nth(3)).toContainText("PR #44Name the plan tiers");
  await expect(rows.nth(5)).toContainText("#46");
  const heights = await rows.evaluateAll((list) => list.map((row) => row.clientHeight));
  expect(new Set(heights).size).toBe(1);

  const action = (row: number, name: string) => rows.nth(row).getByRole("link", { name });
  await expect(action(0, "Answer")).toHaveAttribute("href", `${link}/issues/45`);
  await expect(rows.nth(1).getByRole("button", { name: "Resume" })).toBeVisible();
  await expect(rows.nth(2).getByRole("button", { name: "Start again" })).toBeVisible();
  await expect(action(3, "Open PR")).toHaveAttribute("href", `${link}/pull/44`);
  for (const row of [4, 5]) {
    await expect(rows.nth(row).getByRole("button")).toHaveText(["Details", "Dismiss"]);
  }
  await expect(rows.getByRole("button", { name: "Dismiss" })).toHaveCount(6);
  await expect(page.getByRole("main").getByRole("tab", { name: "To do 6" })).toBeVisible();
});

test("Details is closed by default and shows the full text after a click", async ({ page }) => {
  await routeInbox(page, items);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem").nth(3);
  const details = row.getByRole("button", { name: "Details" });

  await expect(row.getByText("The pull request waits.")).toHaveCount(0);
  await expect(details).toHaveAttribute("aria-expanded", "false");
  await details.click();
  await expect(details).toHaveAttribute("aria-expanded", "true");
  await expect(row.getByText("The pull request waits.")).toBeVisible();
  await expect(row.getByText(/^Integrate loyalty plans · Sep 28, 09:30\sAM$/)).toBeVisible();
  await expect(row.getByRole("link", { name: "Approval report" })).toHaveAttribute(
    "href",
    `${link}/pull/44#issuecomment-1`,
  );
  await details.click();
  await expect(row.getByText("The pull request waits.")).toHaveCount(0);
});

test("Resume and Start again resume the issue", async ({ page }) => {
  const resumed: string[] = [];
  await routeInbox(page, items);
  await page.route("**/api/repositories/*/*/issues/*/resume", (route) => {
    resumed.push(new URL(route.request().url()).pathname);
    return route.fulfill({ status: 204 });
  });
  await page.goto("/inbox");
  const main = page.getByRole("main");

  await main.getByRole("button", { name: "Resume" }).click();
  await main.getByRole("button", { name: "Start again" }).click();

  await expect
    .poll(() => resumed)
    .toEqual([
      "/api/repositories/owner/shop/issues/42/resume",
      "/api/repositories/owner/shop/issues/41/resume",
    ]);
});
