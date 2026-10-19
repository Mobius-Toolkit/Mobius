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

const card = (page: Page, id: number) => page.getByRole("main").locator(`[data-action="${id}"]`);
const row = (page: Page, tool: string) =>
  page.getByRole("main").getByRole("listitem").filter({ hasText: tool });

async function openChat(page: Page) {
  await routeCards(page, createWorkstream, messageLead);
  await routeInboxItems(page, approvalItems);
  await page.goto("/chat");
  await expect(page.getByRole("heading", { name: "Waits for you (2)" })).toBeVisible();
}

test("the Owner approves a row with a note", async ({ page }) => {
  await openChat(page);
  const bodies = routeDecision(page, createWorkstream, "approve", done);
  const approve = row(page, "create_workstream").getByRole("button", { name: "Approve" });

  await expect(card(page, createWorkstream.id).getByText("Waits for the approval")).toBeVisible();
  await expect(card(page, createWorkstream.id).getByRole("button")).toHaveCount(1);
  await expect(
    card(page, createWorkstream.id).getByRole("button", { name: "Show all" }),
  ).toBeVisible();
  await approve.click();

  await expect(approve).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByText("Approve create_workstream · Gift cards")).toBeVisible();
  await expect(page.getByPlaceholder("Add a note for the Coordinator (optional)")).toBeVisible();
  await expect(page.getByRole("button", { name: "Attach images" })).toBeHidden();
  await page.getByRole("textbox", { name: "Note for the Coordinator" }).fill("Go ahead.");
  await page.getByRole("button", { name: "Send" }).click();

  await expect(row(page, "create_workstream")).toBeHidden();
  await expect(page.getByRole("heading", { name: "Waits for you (1)" })).toBeVisible();
  const created = card(page, createWorkstream.id);
  await expect(created.locator('[data-slot="badge"]', { hasText: "Done" })).toBeVisible();
  await expect(created.getByText("Created the Workstream #790.")).toBeVisible();
  await expect(created.getByText("Go ahead.")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Message to the Coordinator" })).toHaveValue("");
  await expect(page.getByRole("button", { name: "Attach images" })).toBeVisible();
  expect(bodies).toEqual([{ message: "Go ahead." }]);
});

test("the Owner clears a choice with a tap on the selected button", async ({ page }) => {
  await openChat(page);
  const approve = row(page, "create_workstream").getByRole("button", { name: "Approve" });
  const reject = row(page, "message_lead").getByRole("button", { name: "Reject" });

  await approve.click();
  await reject.click();
  await expect(approve).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByText("Reject message_lead · Plans in cents")).toBeVisible();
  await reject.click();
  await expect(page.getByText("Reject message_lead · Plans in cents")).toBeHidden();
  await expect(page.getByRole("textbox", { name: "Message to the Coordinator" })).toBeVisible();
});

test("the Owner rejects a row with a note", async ({ page }) => {
  await openChat(page);
  const bodies = routeDecision(page, createWorkstream, "reject", rejected);

  await row(page, "create_workstream").getByRole("button", { name: "Reject" }).click();
  await page.getByRole("textbox", { name: "Note for the Coordinator" }).fill(rejected.message);
  await page.getByRole("button", { name: "Send" }).click();

  const created = card(page, createWorkstream.id);
  await expect(created.getByText("Rejected", { exact: true })).toBeVisible();
  await expect(created.getByText(rejected.message)).toBeVisible();
  await expect(row(page, "create_workstream")).toBeHidden();
  expect(bodies).toEqual([{ message: rejected.message }]);
});

test("a failed action keeps its row and its choice", async ({ page }) => {
  await openChat(page);
  routeDecision(page, createWorkstream, "approve", {
    ...createWorkstream,
    error: "403 API rate limit exceeded",
  });

  await row(page, "create_workstream").getByRole("button", { name: "Approve" }).click();
  await page.getByRole("button", { name: "Send" }).click();

  await expect(row(page, "create_workstream").getByRole("alert")).toHaveText(
    "403 API rate limit exceeded",
  );
  await expect(card(page, createWorkstream.id).getByText("Failed")).toBeVisible();
  await expect(page.getByText("Approve create_workstream · Gift cards")).toBeVisible();
});

test("a chat message goes out as a message while no choice is selected", async ({ page }) => {
  await openChat(page);
  const decisions: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/actions/")) {
      decisions.push(request.url());
    }
  });
  const sent = page.waitForRequest("**/api/chat/messages");
  await page.route("/api/chat/messages", (route) => route.fulfill({ status: 204 }));

  await page.getByRole("textbox", { name: "Message to the Coordinator" }).fill("Hello");
  await page.getByRole("button", { name: "Send" }).click();
  await sent;

  await expect(page.getByRole("heading", { name: "Waits for you (2)" })).toBeVisible();
  expect(decisions).toEqual([]);
});

test("Show opens the full card", async ({ page }) => {
  await openChat(page);
  const created = card(page, createWorkstream.id);

  await row(page, "create_workstream").getByRole("button", { name: "Show" }).click();
  await expect(created.getByRole("button", { name: "Show less" })).toBeVisible();
  await expect(created.getByText("Let customers buy a gift card")).toBeInViewport();
});

test("the Inbox approves an item with one tap", async ({ page }) => {
  await routeInboxItems(page, approvalItems);
  const bodies = routeDecision(page, messageLead, "approve", {
    ...messageLead,
    state: "done",
    result: "Sent the message to the Lead of #12.",
  });

  await page.goto("/inbox");
  const main = page.getByRole("main");
  await expect(row(page, "create_workstream")).toBeVisible();
  await expect(row(page, "message_lead")).toBeVisible();

  await row(page, "message_lead").getByRole("button", { name: "Approve" }).click();

  await expect(row(page, "message_lead")).toBeHidden();
  await expect(row(page, "create_workstream")).toBeVisible();
  await expect(main.getByRole("tab", { name: "To do 2" })).toBeVisible();
  expect(bodies).toEqual([{ message: "" }]);
});

test("the Inbox keeps an item and shows the error when the action fails", async ({ page }) => {
  await routeInboxItems(page, approvalItems);
  routeDecision(page, messageLead, "approve", {
    ...messageLead,
    error: "403 API rate limit exceeded",
  });

  await page.goto("/inbox");
  const item = row(page, "message_lead");
  await item.getByRole("button", { name: "Approve" }).click();

  await expect(item.getByRole("alert")).toHaveText("403 API rate limit exceeded");
  await expect(item.getByRole("button", { name: "Approve" })).toBeVisible();
  await expect(page.getByRole("main").getByRole("tab", { name: "To do 3" })).toBeVisible();
});

test("Open goes to the chat of the agent", async ({ page }) => {
  await routeInboxItems(page, approvalItems);
  await page.goto("/inbox");

  await row(page, "message_lead").getByRole("link", { name: "Open" }).click();

  await expect(page).toHaveURL("/chat");
});

test("the phone shows the cards, the tray and the composer with no horizontal scroll", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openChat(page);
  await row(page, "create_workstream").getByRole("button", { name: "Approve" }).click();
  await page.getByRole("textbox", { name: "Note for the Coordinator" }).fill("Go ahead.");
  await row(page, "create_workstream").getByRole("button", { name: "Show" }).click();

  await expect(
    card(page, createWorkstream.id).getByText("A Playwright test buys a gift card"),
  ).toBeVisible();
  const fits = (selector: string) =>
    page
      .locator(selector)
      .first()
      .evaluate((el) => el.scrollWidth <= el.clientWidth);
  expect(await fits("html")).toBe(true);
  expect(await fits(`[data-action="${createWorkstream.id}"]`)).toBe(true);
  expect(await fits("main div.overflow-y-auto")).toBe(true);
  expect(await fits("main ul")).toBe(true);
  expect(await fits("main form")).toBe(true);
});
