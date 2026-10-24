import { expect, test, type Locator, type Page } from "@playwright/test";
import {
  addHistory,
  addHistoryTable,
  ask,
  askDraft,
  askStore,
  briefTitle,
  cardOf,
  checksQuestion,
  createTitle,
  draftTea,
  draftText,
  draftQuestion,
  draftWorkstream,
  dropColumn,
  dropText,
  editBrief,
  editBriefRule,
  editBriefTable,
  historyText,
  historyTableText,
  keepPrices,
  keepText,
  messageLeadOfTwelve,
  leadTitle,
  leadToLeadTitle,
  ownerTexts,
  restoreBrief,
  sendToCoordinator,
  sendToLead,
  settle,
  stopUnknown,
  storeQuestion,
  teaText,
  teaTitle,
  triagerItem,
  unknownText,
  unknownTitle,
  waitText,
} from "./actions.js";
import { countSamples, fakeMicrophone } from "./microphone.js";
import { routeInboxItems } from "./stops.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test.afterEach(async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await settle(page);
});

// An open panel hides the main element from the role queries, so the card has no role query.
const row = (page: Page, title: string) =>
  page.getByRole("main").getByRole("listitem").filter({ hasText: title });
const panel = (page: Page) => page.getByRole("dialog");
const note = (page: Page) => panel(page).getByRole("textbox", { name: "Note for the Coordinator" });
const approve = (page: Page) => panel(page).getByRole("button", { name: "Approve" });
const reject = (page: Page) => panel(page).getByRole("button", { name: "Reject" });
const approved = (page: Page) =>
  page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      /\/api\/actions\/\d+\/approve$/.test(response.url()),
  );
const waiting = (page: Page, count: number) =>
  page.getByRole("heading", { name: `Waits for you (${count})` });
const option = (group: Locator, title: string) => group.getByRole("button", { name: title });
const agentTimeout = { timeout: 30_000 };
const badge = (card: Locator, name: string) =>
  card.locator('[data-slot="badge"]', { hasText: name });
const link = (page: Page, path: string) =>
  page.locator(`nav a[href="${path}"]`).filter({ visible: true });

const fits = (locator: Locator) =>
  locator.first().evaluate((el) => el.scrollWidth <= el.clientWidth);

async function openChat(page: Page) {
  await page.goto("/chat");
  await expect(page.getByRole("textbox", { name: "Message to the Coordinator" })).toBeVisible();
}

async function answer(page: Page, title: string) {
  await row(page, title).getByRole("button", { name: "Answer" }).click();
  await expect(panel(page)).toBeVisible();
}

test("the Owner approves an action with a note", async ({ page }) => {
  await openChat(page);
  const main = page.getByRole("main");

  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  const card = cardOf(page, keepText);
  await expect(card.getByText("Waits for the approval")).toBeVisible();
  await expect(row(page, leadTitle).getByRole("button")).toHaveText(["Answer", "Chat"]);
  await expect(main.getByRole("button", { name: "Approve" })).toHaveCount(0);
  await expect(main.getByRole("button", { name: "Reject" })).toHaveCount(0);

  await link(page, "/inbox").click();
  await expect(row(page, leadTitle).getByRole("button")).toHaveText(["Answer", "Chat"]);
  await link(page, "/chat").click();
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await answer(page, leadTitle);
  await expect(panel(page).getByText("keep the old column until the next release")).toBeVisible();
  await note(page).fill("Go ahead.");
  await approve(page).click();

  await expect(panel(page)).toBeHidden();
  await expect(waiting(page, 1)).toBeHidden();
  await expect(badge(card, "Done")).toBeVisible();
  await expect(card.getByText("Sent the message to the Lead of #14 Early renewals.")).toBeVisible();
  await expect(card.getByText("Go ahead.")).toBeVisible();

  await link(page, "/inbox").click();
  await expect(main.getByRole("tab", { name: /^To do/ })).toBeVisible();
  await expect(row(page, leadTitle)).toBeHidden();
});

test("the Owner rejects an action with a note", async ({ page }) => {
  await openChat(page);

  await ask(page, dropColumn);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, leadTitle);
  await note(page).fill("Wait for the migration of #12.");
  await reject(page).click();

  await expect(panel(page)).toBeHidden();
  const card = cardOf(page, dropText);
  await expect(card.getByText("Rejected", { exact: true })).toBeVisible();
  await expect(card.getByText("Wait for the migration of #12.")).toBeVisible();
  await expect(waiting(page, 1)).toBeHidden();

  await link(page, "/inbox").click();
  await expect(row(page, leadTitle)).toBeHidden();
});

async function expectChips(scope: Locator) {
  const found = scope.locator('[data-slot="badge"]').filter({ hasText: /^mobius:/ });
  await expect(found).toHaveText(["mobius:maintenance", "mobius:evergreen"]);
  await expect(found.nth(0)).toHaveCSS("background-color", "rgb(191, 218, 220)");
  await expect(found.nth(0)).toHaveCSS("color", "rgb(0, 0, 0)");
  await expect(found.nth(1)).toHaveCSS("background-color", "rgb(0, 82, 204)");
  await expect(found.nth(1)).toHaveCSS("color", "rgb(255, 255, 255)");
  await expect(scope.getByText("labels", { exact: true })).toHaveCount(0);
  await expect(scope.getByText(/\["mobius/)).toHaveCount(0);
}

test("the labels of an action show as GitHub labels in the card and in the panel", async ({
  page,
}) => {
  await openChat(page);

  await ask(page, draftWorkstream);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await expectChips(cardOf(page, draftText));
  await answer(page, createTitle);
  await expectChips(panel(page));
});

async function expectReasonAboveBrief(scope: Locator) {
  const reason = await scope.getByText("reason", { exact: true }).boundingBox();
  const brief = await scope.getByText("brief", { exact: true }).boundingBox();
  expect(reason?.y ?? 999).toBeLessThan(brief?.y ?? 0);
}

test("an empty list of labels shows no name and no chip in the card and in the panel", async ({
  page,
}) => {
  await openChat(page);

  await ask(page, draftTea);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  const card = cardOf(page, teaText);
  await expect(card.getByText("title", { exact: true })).toBeVisible();
  await expect(card.getByText("brief", { exact: true })).toBeVisible();
  await answer(page, teaTitle);
  for (const scope of [card, panel(page)]) {
    await expect(scope.getByText("labels", { exact: true })).toHaveCount(0);
    await expect(scope.locator('[data-slot="badge"]').filter({ hasText: /^mobius:/ })).toHaveCount(
      0,
    );
  }
});

test("the card and the panel show the arguments of an action in the order of its tool", async ({
  page,
}) => {
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await expectReasonAboveBrief(cardOf(page, "The Owner wants a longer time to renew."));
  await answer(page, briefTitle);
  await expectReasonAboveBrief(panel(page));
});

test("a second call of the tool replaces the action that waits", async ({ page }) => {
  await openChat(page);

  await ask(page, addHistory);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, leadTitle);

  await sendToCoordinator(page, addHistoryTable);

  await expect(panel(page)).toBeHidden();
  await expect(cardOf(page, historyText).getByText("Replaced", { exact: true })).toBeVisible();
  await expect(cardOf(page, historyTableText).getByText("Waits for the approval")).toBeVisible();
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await expect(row(page, leadTitle)).toHaveCount(1);
});

test("a failed action keeps the panel with its error and its note, and a second approve sends again", async ({
  page,
}) => {
  await openChat(page);
  const approvals: string[] = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/approve")) {
      approvals.push(request.postData() ?? "");
    }
  });

  await ask(page, stopUnknown);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, unknownTitle);
  await note(page).fill("Go ahead.");
  await approve(page).click();

  await expect(panel(page).getByRole("alert")).toHaveText(/#9999 is not an open Workstream/);
  await expect(note(page)).toHaveValue("Go ahead.");
  const card = cardOf(page, unknownText);
  await expect(badge(card, "Failed")).toBeVisible();
  await expect(card.getByText(/#9999 is not an open Workstream/)).toBeVisible();
  await expect(approve(page)).toBeEnabled();

  await approve(page).click();
  await expect.poll(() => approvals.length).toBe(2);
  await expect(panel(page).getByRole("alert")).toHaveText(/#9999 is not an open Workstream/);

  await reject(page).click();
  await expect(panel(page)).toBeHidden();
  await expect(card.getByText("Rejected", { exact: true })).toBeVisible();
  expect(approvals).toEqual([
    JSON.stringify({ message: "Go ahead." }),
    JSON.stringify({ message: "Go ahead." }),
  ]);
});

test("a decision with an empty note goes out one time and hides the row while the request runs", async ({
  page,
}) => {
  await openChat(page);
  const bodies: unknown[] = [];
  const gate: { open?: () => void } = {};
  const held = new Promise<void>((resolve) => {
    gate.open = resolve;
  });
  await page.route("**/api/actions/*/approve", async (route) => {
    bodies.push(route.request().postDataJSON());
    await held;
    await route.continue();
  });

  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, leadTitle);
  const answered = approved(page);
  await approve(page).click();
  await expect(panel(page)).toBeHidden();
  await expect(row(page, leadTitle)).toBeHidden();
  await expect.poll(() => bodies.length).toBe(1);
  gate.open?.();

  await expect(waiting(page, 1)).toBeHidden();
  expect((await answered).ok()).toBe(true);
  expect(bodies).toEqual([{ message: "" }]);
});

test("the panel has the microphone button and no attach button", async ({ page }) => {
  await page.addInitScript(fakeMicrophone);
  await openChat(page);
  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await answer(page, leadTitle);

  await expect(panel(page).getByRole("button", { name: "Start voice input" })).toBeVisible();
  await expect(panel(page).getByRole("button", { name: "Attach images" })).toHaveCount(0);
  await expect(panel(page).getByRole("button", { name: "Send" })).toHaveCount(0);
});

for (const [decision, message, button] of [
  ["Approve", keepPrices, approve],
  ["Reject", dropColumn, reject],
] as const) {
  test(`${decision} during a conversion keeps it, and the text goes to the field of the chat`, async ({
    page,
  }) => {
    await page.addInitScript(fakeMicrophone);
    await page.addInitScript(countSamples);
    let release!: () => void;
    const held = new Promise<void>((resolve) => (release = resolve));
    await page.route("**/api/voice/transcriptions", async (route) => {
      await held;
      await route.fulfill({ json: { data: { text: "red roses" } } });
    });
    await openChat(page);
    await ask(page, message);
    await expect(waiting(page, 1)).toBeVisible(agentTimeout);
    await answer(page, leadTitle);
    await panel(page).getByRole("button", { name: "Start voice input" }).click();
    await expect.poll(() => page.evaluate("samples")).toBeGreaterThan(0);
    await panel(page).getByRole("button", { name: "Stop voice input" }).click();
    await expect(
      panel(page).getByRole("button", { name: "Converting the voice input" }),
    ).toBeVisible();

    await button(page).click();
    await expect(panel(page)).toBeHidden();
    const input = page.getByRole("textbox", { name: "Message to the Coordinator" });
    await expect(input).toHaveValue("");
    release();
    await expect(input).toHaveValue("red roses");
  });
}

test("a chat message goes out as a message while the tray has rows", async ({ page }) => {
  await openChat(page);
  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
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

  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  expect(decisions).toEqual([]);
});

test("the Inbox approves an item in the panel", async ({ page }) => {
  await openChat(page);
  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await link(page, "/inbox").click();
  await expect(row(page, leadTitle)).toBeVisible();
  await expect(row(page, leadTitle)).not.toContainText("message_lead");
  await expect(row(page, leadTitle).getByRole("button")).toHaveText(["Answer", "Chat"]);

  await answer(page, leadTitle);
  const answered = approved(page);
  await approve(page).click();

  await expect(panel(page)).toBeHidden();
  await expect(row(page, leadTitle)).toBeHidden();
  expect((await answered).ok()).toBe(true);
});

test("the Inbox item of the Triager links to its issue", async ({ page }) => {
  await routeInboxItems(page, [triagerItem]);

  await page.goto("/inbox");

  await expect(
    row(page, "Message to the Lead of #12 Integrate loyalty plans").getByRole("link", {
      name: "#61",
    }),
  ).toHaveAttribute("href", "https://github.com/owner/shop/issues/61");
});

test("the phone shows the cards, the tray and the panel with no horizontal scroll", async ({
  page,
}) => {
  await page.addInitScript(fakeMicrophone);
  await page.setViewportSize({ width: 390, height: 844 });
  await openChat(page);
  await ask(page, draftWorkstream);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await ask(page, keepPrices);
  await expect(waiting(page, 2)).toBeVisible(agentTimeout);

  expect(await fits(page.locator("html"))).toBe(true);
  expect(await fits(cardOf(page, draftText))).toBe(true);
  expect(await fits(cardOf(page, keepText))).toBe(true);
  expect(await fits(page.locator("main div.overflow-y-auto"))).toBe(true);
  expect(await fits(page.locator("main ul"))).toBe(true);

  await answer(page, createTitle);
  await note(page).fill("Go ahead.");

  expect(await fits(page.locator("html"))).toBe(true);
  expect(await fits(page.getByRole("dialog"))).toBe(true);
  const microphone = await panel(page)
    .getByRole("button", { name: "Start voice input" })
    .boundingBox();
  const rejectBox = await reject(page).boundingBox();
  const approveBox = await approve(page).boundingBox();
  expect(Math.abs((microphone?.y ?? 0) - (rejectBox?.y ?? 99))).toBeLessThan(4);
  expect(Math.abs((approveBox?.y ?? 0) - (rejectBox?.y ?? 99))).toBeLessThan(4);
  expect((approveBox?.x ?? 999) + (approveBox?.width ?? 0)).toBeLessThanOrEqual(390);
});

test("the Owner answers a question with options", async ({ page }) => {
  await openChat(page);

  await ask(page, askStore);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await expect(row(page, storeQuestion).getByRole("button")).toHaveText(["Answer", "Chat"]);
  await answer(page, storeQuestion);

  const store = panel(page).getByRole("group", { name: storeQuestion });
  const checks = panel(page).getByRole("group", { name: checksQuestion });
  const send = panel(page).getByRole("button", { name: "Send" });
  const answerNote = panel(page).getByRole("textbox", { name: "Your answer" });
  await expect(panel(page).getByText("The shop needs a store for the plans.")).toBeVisible();
  const sender = panel(page).getByText(/ · /);
  const questionText = await panel(page)
    .getByText("The shop needs a store for the plans.")
    .boundingBox();
  const line = await sender.boundingBox();
  expect(line?.y ?? 999).toBeLessThan(questionText?.y ?? 0);
  await expect(store.getByText("Select one.", { exact: true })).toBeVisible();
  await expect(store.getByText("One file, no server.")).toBeVisible();
  await expect(checks.getByText("Select one or more.")).toBeVisible();
  await expect(checks.getByText("Runs the Go tests.")).toBeVisible();
  await expect(answerNote).toHaveAttribute("placeholder", "Add a note (optional)");
  await expect(send).toBeDisabled();

  await option(store, "Postgres").click();
  await expect(send).toBeEnabled();
  await option(store, "SQLite").click();
  await expect(option(store, "SQLite")).toHaveAttribute("aria-pressed", "true");
  await expect(option(store, "Postgres")).toHaveAttribute("aria-pressed", "false");
  await option(checks, "Lint").click();
  await option(checks, "Tests").click();
  await expect(option(checks, "Lint")).toHaveAttribute("aria-pressed", "true");
  await expect(option(checks, "Tests")).toHaveAttribute("aria-pressed", "true");
  await expect(option(checks, "Types")).toHaveAttribute("aria-pressed", "false");
  await answerNote.fill("Keep the old data.");
  await expect(panel(page)).toBeVisible();
  expect((await ownerTexts(page)).some((text) => text.includes(storeQuestion))).toBe(false);

  await send.click();
  await expect(panel(page)).toBeHidden();
  await expect(waiting(page, 1)).toBeHidden();
  await expect
    .poll(() => ownerTexts(page))
    .toContain("Which store?\n- SQLite\n\nWhich checks?\n- Lint\n- Tests\n\nKeep the old data.");
});

test("the Owner sends only the selected options", async ({ page }) => {
  await openChat(page);

  await ask(page, askStore);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, storeQuestion);
  await panel(page)
    .getByRole("group", { name: storeQuestion })
    .getByRole("button", { name: "SQLite" })
    .click();
  await panel(page).getByRole("button", { name: "Send" }).click();

  await expect(panel(page)).toBeHidden();
  await expect.poll(() => ownerTexts(page)).toContain("Which store?\n- SQLite");
});

test("a question with no questions shows no option and sends the typed text", async ({ page }) => {
  await openChat(page);

  await ask(page, askDraft);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, draftQuestion);

  await expect(panel(page).getByRole("group")).toHaveCount(0);
  await expect(panel(page).getByRole("textbox", { name: "Your answer" })).toHaveAttribute(
    "placeholder",
    "Write your answer",
  );
  await expect(panel(page).getByRole("button", { name: "Send" })).toBeDisabled();
  await panel(page).getByRole("textbox", { name: "Your answer" }).fill("Yes, draft it now.");
  await panel(page).getByRole("button", { name: "Send" }).click();

  await expect(panel(page)).toBeHidden();
  await expect(waiting(page, 1)).toBeHidden();
  await expect.poll(() => ownerTexts(page)).toContain("Yes, draft it now.");
});

async function openLeadChat(page: Page) {
  await page.goto("/workstreams/owner/shop/14");
  await expect(page.getByRole("main").getByLabel("Message to the Lead")).toBeVisible();
}

const removedText = (scope: Locator) => scope.locator("del");
const addedText = (scope: Locator) => scope.locator("ins");
const removedSwitch = (scope: Locator) => scope.getByRole("switch", { name: "Show removed text" });
const decoration = (mark: Locator) =>
  mark.first().evaluate((el) => getComputedStyle(el).textDecorationLine);

test("the panel of an edit_brief action shows the changes", async ({ page }) => {
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await expect(removedSwitch(dialog)).not.toBeChecked();
  await expect(removedText(dialog)).toHaveCount(0);
  await expect(dialog).not.toContainText("one month");
  await expect(dialog).not.toContainText("Send a mail after the renewal.");
  await expect(addedText(dialog)).toHaveText(["two months", "Do not remove the old renewal page."]);
  await expect(dialog.locator("strong", { hasText: "Goal" })).toBeVisible();
  for (const item of await dialog.locator("li").all()) {
    await expect(item).toHaveText(/\S/);
  }
  await expect(dialog.locator("strong")).toHaveText(["Goal", "Scope", "Limits"]);
  await expect(dialog.locator("pre")).toHaveCount(0);
  expect(await decoration(addedText(dialog))).toBe("none");
  expect(await fits(dialog)).toBe(true);
  await expect(dialog.getByRole("button", { name: "Hide removed text" })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Show removed text" })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Show the full Brief" })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Show the changes" })).toHaveCount(0);

  await dialog.getByText("Show removed text").click();
  await expect(removedSwitch(dialog)).toBeChecked();
  await expect(removedText(dialog)).toHaveText(["one month", "Send a mail after the renewal."]);
  await expect(dialog.locator("li", { has: page.locator("del") })).toHaveText(
    "Send a mail after the renewal.",
  );
  await expect(dialog.locator("li", { has: page.locator("ins") })).toHaveText(
    "Do not remove the old renewal page.",
  );
  expect(await decoration(removedText(dialog))).toBe("line-through");

  await removedSwitch(dialog).click();
  await expect(removedText(dialog)).toHaveCount(0);
});

test("the panel of an edit_brief action shows a change in inline code as a full old line and a full new line", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: {
        data: {
          brief:
            "**Goal**\nLet customers renew a plan with `renew_old` early.\n\n**Scope**\n- Add a button to the renewal page.",
        },
      },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await expect(removedText(dialog)).toHaveCount(0);
  await expect(addedText(dialog).first()).toHaveText(
    "Let customers renew a plan two months early.",
  );
  await expect(dialog).not.toContainText("==");
  await expect(dialog).not.toContainText("~~");

  await removedSwitch(dialog).click();
  await expect(removedText(dialog).first()).toHaveText(
    "Let customers renew a plan with renew_old early.",
  );
  await expect(addedText(dialog).first()).toHaveText(
    "Let customers renew a plan two months early.",
  );
  await expect(dialog).not.toContainText("==");
  await expect(dialog).not.toContainText("~~");
});

test("the panel of an edit_brief action shows no removed rule as Markdown", async ({ page }) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: {
        data: {
          brief:
            "**Goal**\nLet customers renew a plan two months early.\n---\n\n**Scope**\n- Add a button to the renewal page.\n\n**Limits**\n- Do not change the price of a plan.",
        },
      },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await expect(addedText(dialog)).toHaveText(["Do not remove the old renewal page."]);
  await expect(dialog.locator("hr")).toHaveCount(0);
  await expect(dialog.locator("h2", { hasText: "Let customers" })).toHaveCount(0);
});

test("the panel of an edit_brief action shows no removed fenced code as Markdown", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: {
        data: {
          brief:
            "**Goal**\nLet customers renew a plan two months early.\n\n```sh\n# install deps\npnpm install *.tgz\n```\n\n**Scope**\n- Add a button to the renewal page.\n\n**Limits**\n- Do not change the price of a plan.",
        },
      },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await expect(addedText(dialog)).toHaveText(["Do not remove the old renewal page."]);
  await expect(dialog.locator("h1")).toHaveCount(0);
  await expect(dialog).not.toContainText("install deps");
  await expect(dialog).not.toContainText("pnpm install");
});

test("the panel of an edit_brief action keeps the cells of a table row with the marks", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: {
        data: {
          brief:
            "**Goal**\nShow a table.\n\n| Plan | Months |\n| --- | --- |\n| Basic | one |\n| Old | four |",
        },
      },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBriefTable);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await removedSwitch(dialog).click();
  await expect(removedText(dialog)).toContainText(["Old", "four"]);
  await expect(addedText(dialog)).toContainText(["Extra", "three"]);
  const rows = dialog.locator("tr");
  await expect(rows).toHaveCount(4);
  for (const cells of await rows.all()) {
    await expect(cells.locator("th, td")).toHaveCount(2);
  }
  await expect(dialog).not.toContainText("==");
  await expect(dialog).not.toContainText("~~");
});

test("the panel of an edit_brief action keeps a removed block apart from the next block", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: {
        data: {
          brief:
            "**Goal**\nShow a rule.\n\nRemoved text.\n\n---\n\n| X | Y |\n| --- | --- |\n| 1 | 2 |\n\nKeep text.",
        },
      },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBriefRule);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await removedSwitch(dialog).click();
  await expect(removedText(dialog)).toHaveText(["Removed text.", "X", "Y", "1", "2"]);
  await expect(dialog.locator("h2", { hasText: "Removed text." })).toHaveCount(0);
  await expect(dialog.locator("hr")).toHaveCount(1);
  await expect(dialog.locator("p", { hasText: "Keep text." })).not.toContainText("Removed text.");
  const rows = dialog.locator("tr");
  await expect(rows).toHaveCount(2);
  for (const cells of await rows.all()) {
    await expect(cells.locator("th, td")).toHaveCount(2);
  }
  await expect(dialog).not.toContainText("==");
  await expect(dialog).not.toContainText("~~");
});

test("the panel of an edit_brief action keeps a removed last section apart from the list above it", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: {
        data: {
          brief:
            "**Goal**\nLet customers renew a plan two months early.\n\n**Scope**\n- Add a button to the renewal page.\n\n**Limits**\n- Do not change the price of a plan.\n- Do not remove the old renewal page.\n\n**Removed title**\n- Removed child.",
        },
      },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await removedSwitch(dialog).click();
  await expect(removedText(dialog)).toHaveText(["Removed title", "Removed child."]);
  await expect(dialog.locator("li", { hasText: "Removed title" })).toHaveCount(0);
  await expect(dialog.locator("li", { hasText: "Removed child." })).toHaveCount(1);
  await expect(
    dialog.locator("li", { hasText: "Do not remove the old renewal page." }),
  ).not.toContainText("Removed");
  await expect(dialog).not.toContainText("~~");
});

test("the panel of an edit_brief action keeps a removed paragraph apart from an added rule", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: { data: { brief: "**Goal**\nShow a rule.\n\nOld text.\n\nKeep text." } },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBriefRule);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await removedSwitch(dialog).click();
  await expect(removedText(dialog)).toHaveText("Old text.");
  await expect(dialog.locator("h2", { hasText: "Old text." })).toHaveCount(0);
  await expect(dialog.locator("hr")).toHaveCount(1);
  await expect(dialog).not.toContainText("==");
  await expect(dialog).not.toContainText("~~");
});

test("the panel of an edit_brief action keeps a rewritten line in the paragraph of its title", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: { data: { brief: "**Goal**\nLet customers renew early.\n\n---\n\nKeep text." } },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBriefRule);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await removedSwitch(dialog).click();
  const paragraph = dialog.locator("p", { has: page.locator("strong", { hasText: "Goal" }) });
  await expect(paragraph.locator("del")).toHaveText("Let customers renew early.");
  await expect(paragraph.locator("ins")).toHaveText("Show a rule.");
  await expect(dialog.locator("hr")).toHaveCount(1);
  await expect(dialog).not.toContainText("==");
  await expect(dialog).not.toContainText("~~");
});

test("the panel of an edit_brief action keeps a plain line apart from the list item that it replaces", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({
      json: { data: { brief: "**Goal**\nShow a rule.\n\n---\n\n- Keep text." } },
    }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBriefRule);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);

  const dialog = panel(page);
  await removedSwitch(dialog).click();
  await expect(dialog.locator("li del")).toHaveText("Keep text.");
  await expect(dialog.locator("ins")).toHaveText("Keep text.");
  await expect(dialog.locator("li ins")).toHaveCount(0);
  await expect(dialog).not.toContainText("==");
  await expect(dialog).not.toContainText("~~");
});

test("an edit_brief action that comes while the first read of the Inbox runs stays in the tray", async ({
  page,
}) => {
  const gate = { open: () => {} };
  const released = new Promise<void>((resolve) => (gate.open = resolve));
  let fetched = false;
  let first = true;
  await page.route("**/api/inbox", async (route) => {
    if (!first || !route.request().frame().url().includes("/workstreams/")) {
      return route.continue();
    }
    first = false;
    const response = await route.fetch();
    fetched = true;
    await released;
    await route.fulfill({ response });
  });
  await openLeadChat(page);
  await expect.poll(() => fetched).toBe(true);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  const answered = page.waitForResponse("**/api/inbox");
  gate.open();
  await (await answered).finished();
  await page.evaluate(
    "new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))",
  );
  await expect(waiting(page, 1)).toBeVisible();
});

test("the panel of an edit_brief action reads the Brief again each time it opens", async ({
  page,
}) => {
  let briefs = "**Goal**\nLet customers renew a plan three months early.";
  let reads = 0;
  await page.route("**/api/actions/*/brief", (route) => {
    reads++;
    return route.fulfill({ json: { data: { brief: briefs } } });
  });
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await answer(page, briefTitle);
  const dialog = panel(page);
  await removedSwitch(dialog).click();
  await expect(removedText(dialog)).toHaveText("three");
  await expect(addedText(dialog).first()).toHaveText("two");
  await expect(dialog.getByText("Send a mail after the renewal.")).toHaveCount(0);
  expect(reads).toBe(1);

  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  briefs = "**Goal**\nLet customers renew a plan one month early.";
  await answer(page, briefTitle);
  await removedSwitch(dialog).click();
  await expect(removedText(dialog)).toHaveText("one month");
  expect(reads).toBe(2);
});

test("the panel of an edit_brief action shows the full Brief when the read fails", async ({
  page,
}) => {
  await page.route("**/api/actions/*/brief", (route) =>
    route.fulfill({ status: 500, json: { error: "GitHub is down." } }),
  );
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await answer(page, briefTitle);

  const dialog = panel(page);
  await expect(dialog.getByText("Let customers renew a plan two months early.")).toBeVisible();
  await expect(removedText(dialog)).toHaveCount(0);
  await expect(addedText(dialog)).toHaveCount(0);
});

test("the card of a done edit_brief action shows the changes, and the full Brief with no old Brief", async ({
  page,
}) => {
  await openLeadChat(page);
  await sendToLead(page, editBrief);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, briefTitle);
  await approve(page).click();
  await expect(panel(page)).toBeHidden();

  try {
    const card = cardOf(page, "edit_brief");
    await expect(badge(card, "Done")).toBeVisible();
    await expect(removedSwitch(card)).not.toBeChecked();
    await expect(removedText(card)).toHaveCount(0);
    await expect(addedText(card)).toHaveText(["two months", "Do not remove the old renewal page."]);
    const cut = card.locator("div.max-h-36");
    expect(await cut.evaluate((el) => el.clientHeight)).toBe(144);
    await expect(addedText(card).last()).not.toBeInViewport();

    await card.getByRole("button", { name: "Show all" }).click();
    await expect(card.locator("div.max-h-36")).toHaveCount(0);
    await expect(addedText(card).last()).toBeInViewport();
    await expect(card.getByRole("button", { name: "Show the full Brief" })).toHaveCount(0);
    await expect(card.getByRole("button", { name: "Show the changes" })).toHaveCount(0);

    await card.getByText("Show removed text").click();
    await expect(removedText(card)).toHaveText(["one month", "Send a mail after the renewal."]);
    await expect(addedText(card)).toHaveCount(2);
    await removedSwitch(card).click();
    await expect(removedText(card)).toHaveCount(0);
    await removedSwitch(card).click();
    await expect(removedText(card)).toHaveCount(2);

    await page.route("**/api/chat?*", async (route) => {
      const response = await route.fetch();
      const body = (await response.json()) as {
        data: { messages: { action: { tool: string; oldBrief: string } | null }[] };
      };
      for (const message of body.data.messages) {
        if (message.action?.tool === "edit_brief") {
          message.action.oldBrief = "";
        }
      }
      await route.fulfill({ response, json: body });
    });
    await page.reload();
    const bare = cardOf(page, "edit_brief");
    await expect(bare.getByText("Let customers renew a plan two months early.")).toBeVisible();
    await expect(addedText(bare)).toHaveCount(0);
  } finally {
    await page.unroute("**/api/chat?*");
    await restoreBrief(page);
  }
});

const rejected = (page: Page) =>
  page.waitForResponse(
    (response) =>
      response.request().method() === "POST" && /\/api\/actions\/\d+\/reject$/.test(response.url()),
  );

test("the panel of a message_lead action of a Lead names the Workstream of the Lead and shows only the text", async ({
  page,
}) => {
  await openLeadChat(page);
  await sendToLead(page, messageLeadOfTwelve);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await expect(cardOf(page, waitText).getByText("workstream", { exact: true })).toHaveCount(0);
  await answer(page, leadToLeadTitle);
  const dialog = panel(page);
  await expect(dialog.getByText(/^Lead of /)).toContainText("Lead of #14 Early renewals");
  await expect(dialog.getByRole("link", { name: "#14 Early renewals" })).toHaveAttribute(
    "href",
    "/workstreams/owner/shop/14",
  );
  await expect(dialog.getByText(waitText)).toBeVisible();
  await expect(dialog.getByText("workstream", { exact: true })).toHaveCount(0);

  const answered = rejected(page);
  await reject(page).click();
  expect((await answered).ok()).toBe(true);
});

test("the panel of a message_lead action of the Coordinator names the Coordinator and shows only the text", async ({
  page,
}) => {
  await openChat(page);
  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await expect(cardOf(page, keepText).getByText("workstream", { exact: true })).toHaveCount(0);
  await answer(page, leadTitle);
  const dialog = panel(page);
  await expect(dialog.getByText(/^Coordinator/)).toContainText("Coordinator ·");
  await expect(dialog.getByRole("link")).toHaveCount(0);
  await expect(dialog.getByText(keepText)).toBeVisible();
  await expect(dialog.getByText("workstream", { exact: true })).toHaveCount(0);

  const answered = rejected(page);
  await reject(page).click();
  expect((await answered).ok()).toBe(true);
});
