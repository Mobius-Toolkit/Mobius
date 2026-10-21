import { expect, test, type BrowserContext, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
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
  id: 301,
  issue: 45,
  kind: "question",
  title: "Pick the plan limits",
  reason: "The Lead asks a question",
  text: "Which seat limit?",
  link: `${link}/issues/45`,
};

const otherQuestion = {
  ...question,
  id: 302,
  issue: 46,
  title: "Pick the plan names",
  link: `${link}/issues/46`,
};

const needsHuman = (issue: number) => ({
  number: issue,
  pullRequest: null,
  pullRequestUrl: null,
  question: true,
  reason: "",
  repository: "owner/shop",
  stopped: false,
  title: "Pick",
  url: `${link}/issues/${issue}`,
  workstream: 12,
});

const approval = {
  ...base,
  id: 303,
  issue: 0,
  workstream: 0,
  repository: "",
  workstreamRepository: "",
  kind: "approval",
  title: "Message to the Lead of #14",
  reason: "The Coordinator waits for your approval",
  text: "",
  action: {
    id: 77,
    role: "coordinator",
    tool: "message_lead",
    arguments: [{ name: "text", value: "Keep the prices in cents." }],
    state: "pending",
    error: "",
    message: "",
    result: "",
    decidedAt: null,
    time: "2026-09-28T09:30:00Z",
  },
};

// An open panel hides main from the role queries, so the rows have no role query.
const rows = (page: Page) => page.locator("main li");
const panel = (page: Page) => page.getByRole("dialog");
const mark = (page: Page) =>
  page
    .locator('nav a[href="/workstreams/owner/shop/12"]')
    .filter({ visible: true })
    .getByText("needs you");

async function routeLists(page: Page, inbox: () => object[], issues: () => number[] = () => []) {
  await page.route("**/api/inbox", (route) => route.fulfill({ json: { data: inbox() } }));
  await page.route("**/api/needs-human", (route) =>
    route.fulfill({ json: { data: issues().map(needsHuman) } }),
  );
}

// Holds the response of the request until it answers it.
async function hold(page: Page, url: string) {
  let answer!: (response: { status?: number; json: object }) => void;
  const answered = new Promise<{ status?: number; json: object }>((resolve) => (answer = resolve));
  await page.route(url, async (route) => route.fulfill(await answered));
  return answer;
}

async function send(page: Page, text: string) {
  await rows(page).first().getByRole("button", { name: "Answer" }).click();
  await panel(page).getByRole("textbox", { name: "Your answer" }).fill(text);
  await panel(page).getByRole("button", { name: "Send" }).click();
}

async function reconnect(page: Page, context: BrowserContext) {
  await context.setOffline(true);
  await expect(page.getByRole("status")).toHaveText("Offline");
  const reopened = page.waitForResponse("/api/events");
  await context.setOffline(false);
  await reopened;
}

test("an answered question leaves the Inbox before the response arrives", async ({ page }) => {
  await routeLists(page, () => [question]);
  const respond = await hold(page, "**/api/inbox/301/answer");
  await page.goto("/inbox");
  await expect(rows(page)).toHaveCount(1);

  await send(page, "Ten seats.");

  await expect(panel(page)).toHaveCount(0);
  await expect(rows(page)).toHaveCount(0);
  await expect(page.getByRole("main").getByText("Nothing waits for you.")).toBeVisible();
  respond({ json: { data: { url: `${link}/issues/45#issuecomment-1` } } });
  await expect(rows(page)).toHaveCount(0);
});

test("an approved action leaves the tray and the Inbox at once", async ({ page }) => {
  await routeLists(page, () => [approval]);
  const respond = await hold(page, "**/api/actions/77/approve");
  await page.goto("/chat");
  const tray = page.getByRole("heading", { name: "Waits for you (1)" });
  await expect(tray).toBeVisible();

  await page.getByRole("button", { name: "Answer" }).click();
  await panel(page).getByRole("button", { name: "Approve" }).click();

  await expect(tray).toHaveCount(0);
  await page.locator('nav a[href="/inbox"]').filter({ visible: true }).click();
  await expect(page.getByRole("main").getByText("Nothing waits for you.")).toBeVisible();
  await expect(rows(page)).toHaveCount(0);
  respond({ json: { data: { ...approval.action, state: "done", result: "Sent." } } });
});

test("the mark of a Workstream goes away at once with its last open issue", async ({ page }) => {
  await routeLists(
    page,
    () => [question],
    () => [45],
  );
  const respond = await hold(page, "**/api/inbox/301/answer");
  await page.goto("/inbox");
  await expect(mark(page)).toBeVisible();

  await send(page, "Ten seats.");

  await expect(rows(page)).toHaveCount(0);
  await expect(mark(page)).toHaveCount(0);
  respond({ json: { data: { url: `${link}/issues/45#issuecomment-1` } } });
});

test("the mark of a Workstream stays while another issue of it needs the Owner", async ({
  page,
}) => {
  await routeLists(
    page,
    () => [question, otherQuestion],
    () => [45, 46],
  );
  const respond = await hold(page, "**/api/inbox/301/answer");
  await page.goto("/inbox");
  await expect(mark(page)).toBeVisible();

  await send(page, "Ten seats.");

  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText("Pick the plan names");
  await expect(mark(page)).toBeVisible();
  respond({ json: { data: { url: `${link}/issues/45#issuecomment-1` } } });
});

test("a failed answer brings the row and the mark back, and opens the panel with the error and the text", async ({
  page,
}) => {
  await routeLists(
    page,
    () => [question],
    () => [45],
  );
  const respond = await hold(page, "**/api/inbox/301/answer");
  await page.goto("/inbox");
  await send(page, "Ten seats.");
  await expect(rows(page)).toHaveCount(0);
  await expect(mark(page)).toHaveCount(0);

  respond({ status: 409, json: { error: "The Owner must authorize the Mobius App." } });

  await expect(panel(page).getByRole("alert")).toHaveText(
    "The Owner must authorize the Mobius App.",
  );
  await expect(panel(page).getByRole("textbox", { name: "Your answer" })).toHaveValue("Ten seats.");
  await expect(mark(page)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(rows(page)).toHaveCount(1);
});

test("a read of the lists while the record exists does not show the answered item again", async ({
  page,
  context,
}) => {
  let list: object[] = [question];
  await routeLists(
    page,
    () => list,
    () => [45],
  );
  await page.route("**/api/inbox/301/answer", (route) =>
    route.fulfill({ json: { data: { url: `${link}/issues/45#issuecomment-1` } } }),
  );
  await page.goto("/inbox");
  await send(page, "Ten seats.");
  await expect(rows(page)).toHaveCount(0);

  list = [question, otherQuestion];
  await reconnect(page, context);

  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText("Pick the plan names");
  await expect(mark(page)).toHaveCount(0);
});

test("the item and the mark come back two minutes after the answer when the server still gives them", async ({
  page,
}) => {
  await page.clock.install();
  await routeLists(
    page,
    () => [question],
    () => [45],
  );
  await page.route("**/api/inbox/301/answer", (route) =>
    route.fulfill({ json: { data: { url: `${link}/issues/45#issuecomment-1` } } }),
  );
  await page.goto("/inbox");
  await send(page, "Ten seats.");
  await expect(rows(page)).toHaveCount(0);

  await page.clock.fastForward("02:01");

  await expect(rows(page)).toHaveCount(1);
  await expect(mark(page)).toBeVisible();
});
