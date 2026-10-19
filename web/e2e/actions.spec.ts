import { expect, test, type Page } from "@playwright/test";
import {
  approvalItems,
  createWorkstream,
  done,
  messageLead,
  rejected,
  routeCards,
  routeDecision,
} from "./actions.js";
import { routeInboxItems } from "./stops.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
  await page.route("/api/chat/seen", (route) => route.fulfill({ status: 204 }));
});

const card = (page: Page) =>
  page.getByRole("main").locator(`[data-action="${createWorkstream.id}"]`);

test("the Owner approves a card with a message", async ({ page }) => {
  await routeCards(page, createWorkstream);
  const bodies = routeDecision(page, createWorkstream, "approve", done);

  await page.goto("/chat");
  await expect(card(page).getByText("Waits for the approval")).toBeVisible();
  await expect(card(page).getByText("create_workstream")).toBeVisible();
  await expect(card(page).getByText("Do not add a refund of a gift card.")).toBeVisible();

  await card(page).getByRole("button", { name: "Approve" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: "Your message" }).fill("Go ahead.");
  await dialog.getByRole("button", { name: "Approve" }).click();

  await expect(dialog).toBeHidden();
  await expect(card(page).locator('[data-slot="badge"]', { hasText: "Done" })).toBeVisible();
  await expect(card(page).getByText("Created the Workstream #790.")).toBeVisible();
  await expect(card(page).getByText("Go ahead.")).toBeVisible();
  await expect(card(page).getByRole("button", { name: "Approve" })).toBeHidden();
  expect(bodies).toEqual([{ message: "Go ahead." }]);
});

test("the Owner rejects a card with a message", async ({ page }) => {
  await routeCards(page, createWorkstream);
  const bodies = routeDecision(page, createWorkstream, "reject", rejected);

  await page.goto("/chat");
  await card(page).getByRole("button", { name: "Reject" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: "Your message" }).fill(rejected.message);
  await dialog.getByRole("button", { name: "Reject" }).click();

  await expect(dialog).toBeHidden();
  await expect(card(page).getByText("Rejected", { exact: true })).toBeVisible();
  await expect(card(page).getByText(rejected.message)).toBeVisible();
  expect(bodies).toEqual([{ message: rejected.message }]);
});

test("the Inbox shows the cards and an item leaves after the decision", async ({ page }) => {
  await routeInboxItems(page, approvalItems);
  const bodies = routeDecision(page, messageLead, "approve", {
    ...messageLead,
    state: "done",
    result: "Sent the message to the Lead of #12.",
  });

  await page.goto("/inbox");
  const main = page.getByRole("main");
  const rows = main.getByRole("listitem");
  const row = rows.filter({ hasText: "message_lead" });
  await expect(rows.filter({ hasText: "create_workstream" })).toBeVisible();
  await expect(row).toBeVisible();

  await row.getByRole("button", { name: "Approve" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Approve" }).click();

  await expect(row).toBeHidden();
  await expect(rows.filter({ hasText: "create_workstream" })).toBeVisible();
  expect(bodies).toEqual([{ message: "" }]);
});

test("the phone shows the full Brief with no horizontal scroll", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await routeCards(page, createWorkstream);

  await page.goto("/chat");
  await expect(card(page).getByText("A Playwright test buys a gift card")).toBeVisible();
  expect(await page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")).toBe(
    true,
  );
});
