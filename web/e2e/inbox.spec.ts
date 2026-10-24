import { expect, test, type BrowserContext, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const link = "https://github.com/owner/shop";

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

const items = [
  {
    ...base,
    id: 1,
    issue: 45,
    kind: "question",
    title: "Pick the plan limits",
    reason: "The Lead asks a question",
    text: "Which seat limit?\n\n- `10` seats\n- `40` seats",
    link: `${link}/issues/45`,
  },
  {
    ...base,
    id: 2,
    issue: 42,
    kind: "stopped",
    title: "Let customers change plans",
    reason: "The implementer session hung",
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

  const action = (row: number, name: string) => rows.nth(row).getByRole("link", { name });
  await expect(rows.nth(0).getByRole("button")).toHaveText(["Answer", "Chat"]);
  await expect(action(0, "Open GitHub")).toHaveAttribute("href", `${link}/issues/45`);
  await expect(rows.nth(1).getByRole("button")).toHaveText(["Resume", "Answer", "Chat"]);
  await expect(action(1, "Open GitHub")).toHaveAttribute("href", `${link}/issues/42`);
  await expect(rows.nth(2).getByRole("button")).toHaveText(["Start again", "Chat", "Details"]);
  await expect(action(3, "Open PR")).toHaveAttribute("href", `${link}/pull/44`);
  await expect(rows.nth(4).getByRole("button")).toHaveText([
    "Send again",
    "Chat",
    "Details",
    "Dismiss",
  ]);
  await expect(rows.nth(5).getByRole("button")).toHaveText(["Chat", "Details"]);
  await expect(rows.getByRole("button", { name: "Dismiss" })).toHaveCount(1);
  await expect(page.getByRole("main").getByRole("tab", { name: "To do 6" })).toBeVisible();
});

test("the reason is on the line of the title on a desktop and below it on a phone", async ({
  page,
}) => {
  await routeInbox(page, items);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem").nth(1);
  const title = row.getByText("Let customers change plans");
  const reason = row.getByText("The implementer session hung");
  const buttons = row.getByRole("button", { name: "Resume" });

  await page.setViewportSize({ width: 1280, height: 800 });
  const desktop = { title: await title.boundingBox(), reason: await reason.boundingBox() };
  expect(desktop.reason!.y).toBeLessThan(desktop.title!.y + desktop.title!.height);
  // scrollWidth rounds, so a cut of less than one pixel needs the width of the text itself.
  const cut = await title.evaluate((e) => {
    const range = document.createRange();
    range.selectNodeContents(e);
    return range.getBoundingClientRect().width - e.getBoundingClientRect().width;
  });
  expect(cut).toBeLessThanOrEqual(0);

  await page.setViewportSize({ width: 390, height: 844 });
  const phone = {
    title: await title.boundingBox(),
    reason: await reason.boundingBox(),
    buttons: await buttons.boundingBox(),
  };
  expect(phone.reason!.y).toBeGreaterThanOrEqual(phone.title!.y + phone.title!.height);
  expect(phone.buttons!.y).toBeGreaterThanOrEqual(phone.reason!.y + phone.reason!.height);
  await expect(title).toHaveJSProperty("scrollWidth", await title.evaluate((e) => e.clientWidth));
  await expect(reason).toHaveJSProperty("scrollWidth", await reason.evaluate((e) => e.clientWidth));
});

test("Details sits at the right edge of an item that has it, the action at the left edge on a phone", async ({
  page,
}) => {
  await routeInbox(page, items);
  await page.goto("/inbox");
  const rows = page.getByRole("main").getByRole("listitem");

  for (const size of [
    { width: 1280, height: 800 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(size);
    await expect(rows.nth(0)).toBeVisible();
    for (const row of [2, 3, 5]) {
      const item = await rows.nth(row).boundingBox();
      const details = await rows.nth(row).getByRole("button", { name: "Details" }).boundingBox();
      expect(item!.x + item!.width - (details!.x + details!.width)).toBeLessThan(24);
    }
  }

  const answer = await rows.nth(0).getByRole("button", { name: "Answer" }).boundingBox();
  const item = await rows.nth(0).boundingBox();
  expect(answer!.x - item!.x).toBeLessThan(24);
});

test("Details opens a panel with the text and no input, and the panel closes", async ({ page }) => {
  await routeInbox(page, items);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem").nth(3);
  const panel = page.getByRole("dialog");

  await expect(panel).toHaveCount(0);
  await row.getByRole("button", { name: "Details" }).click();
  await expect(panel.getByRole("heading", { name: "Name the plan tiers" })).toBeVisible();
  await expect(panel.getByText("The pull request waits for your approval")).toBeVisible();
  await expect(panel.getByText("The pull request waits.")).toBeVisible();
  await expect(panel.getByText(/^Integrate loyalty plans · Sep 28, 09:30\sAM$/)).toBeVisible();
  await expect(panel.getByRole("link", { name: "Approval report" })).toHaveAttribute(
    "href",
    `${link}/pull/44#issuecomment-1`,
  );
  await expect(panel.getByRole("textbox")).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  await expect(row).toBeVisible();
});

test("Answer opens a panel with the rendered text, and a sent answer closes the panel and the row", async ({
  page,
}) => {
  await page.goto("/api/github/user-callback?code=shop-code");
  const created = await page.request.post("/e2e/inbox-items");
  const id = Number(await created.text());
  try {
    await routeInbox(page, [{ ...items[0], id, issue: 40, link: `${link}/issues/40` }]);
    await page.goto("/inbox");
    const main = page.getByRole("main");
    const panel = page.getByRole("dialog");

    await main.getByRole("button", { name: "Answer" }).click();
    await expect(panel.getByRole("listitem")).toHaveText(["10 seats", "40 seats"]);
    await expect(panel.locator("code")).toHaveText(["10", "40"]);
    await panel.getByRole("textbox", { name: "Your answer" }).fill("Ten seats.");
    const response = page.waitForResponse(`**/api/inbox/${id}/answer`);
    await panel.getByRole("button", { name: "Send" }).click();

    const answered = (await (await response).json()) as { data: { url: string } };
    expect(answered.data.url).toContain("/issues/40#issuecomment-");
    await expect(panel).toHaveCount(0);
    await expect(main.getByRole("listitem")).toHaveCount(0);
  } finally {
    await page.request.delete(`/e2e/inbox-items/${id}`);
  }
});

test("a failed answer keeps the panel and the text, and shows the error", async ({ page }) => {
  await routeInbox(page, items);
  await page.route("**/api/inbox/*/answer", (route) =>
    route.fulfill({ status: 409, json: { error: "The Owner must authorize the Mobius App." } }),
  );
  await page.goto("/inbox");
  const panel = page.getByRole("dialog");

  await page.getByRole("main").getByRole("button", { name: "Answer" }).first().click();
  const input = panel.getByRole("textbox", { name: "Your answer" });
  await input.fill("Ten seats.");
  await panel.getByRole("button", { name: "Send" }).click();

  await expect(panel.getByRole("alert")).toHaveText("The Owner must authorize the Mobius App.");
  await expect(input).toHaveValue("Ten seats.");
});

test("each item keeps its own draft of the answer", async ({ page }) => {
  await routeInbox(page, items);
  await page.goto("/inbox");
  const rows = page.getByRole("main").getByRole("listitem");
  const panel = page.getByRole("dialog");
  const input = panel.getByRole("textbox", { name: "Your answer" });

  await rows.nth(0).getByRole("button", { name: "Answer" }).click();
  await input.fill("Ten seats.");
  await page.keyboard.press("Escape");
  await rows.nth(1).getByRole("button", { name: "Answer" }).click();
  await expect(input).toHaveValue("");
  await page.keyboard.press("Escape");
  await rows.nth(0).getByRole("button", { name: "Answer" }).click();
  await expect(input).toHaveValue("Ten seats.");
});

test("the panel of a question opens on a phone with the full text and no focus on the input", async ({
  page,
}) => {
  await routeInbox(page, items);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/inbox");
  const rows = page.getByRole("main").getByRole("listitem");
  const panel = page.getByRole("dialog");

  await expect(rows.nth(0).getByRole("button", { name: "Details" })).toHaveCount(0);
  await expect(rows.nth(1).getByRole("button", { name: "Details" })).toHaveCount(0);
  await expect(rows.nth(2).getByRole("button", { name: "Details" })).toHaveCount(1);
  await expect(rows.nth(3).getByRole("button", { name: "Details" })).toHaveCount(1);
  await expect(rows.nth(4).getByRole("button", { name: "Details" })).toHaveCount(1);
  await expect(rows.nth(5).getByRole("button", { name: "Details" })).toHaveCount(1);

  await rows.nth(0).getByRole("button", { name: "Answer" }).click();
  await expect(panel).toBeFocused();
  await expect(panel.getByRole("textbox", { name: "Your answer" })).not.toBeFocused();
  await expect(panel.getByRole("listitem").last()).toBeInViewport({ ratio: 1 });
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
});

test("the input of the panel stays visible on a phone while it has the focus", async ({ page }) => {
  await routeInbox(page, items);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/inbox");
  const panel = page.getByRole("dialog");

  await page.getByRole("main").getByRole("button", { name: "Answer" }).first().click();
  const input = panel.getByRole("textbox", { name: "Your answer" });
  await input.focus();
  await expect(input).toBeFocused();
  await expect(input).toBeInViewport({ ratio: 1 });

  await expect
    .poll(async () => {
      const box = await panel.boundingBox();
      return [box!.x, box!.y + box!.height, box!.width];
    })
    .toEqual([0, 844, 390]);

  await page.setViewportSize({ width: 390, height: 420 });
  await expect(input).toBeInViewport({ ratio: 1 });
  await expect(panel.getByRole("button", { name: "Send" })).toBeInViewport({ ratio: 1 });
  await expect(panel.getByRole("heading", { name: "Pick the plan limits" })).toBeInViewport();
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

test("Send again sends the message of the Lead failed item again", async ({ page }) => {
  const sent: string[] = [];
  await routeInbox(page, items);
  await page.route("**/api/inbox/*/send", (route) => {
    sent.push(new URL(route.request().url()).pathname);
    return route.fulfill({ status: 204 });
  });
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Send again" }).click();

  await expect.poll(() => sent).toEqual(["/api/inbox/5/send"]);
});

const readyItem = items[3];

test("Merge is the primary button of a ready for review item and shows Merging after a press", async ({
  page,
}) => {
  const merged: string[] = [];
  await routeInbox(page, [{ ...readyItem, merging: false, mergeRefusal: "" }]);
  await page.route("**/api/inbox/*/merge", (route) => {
    merged.push(new URL(route.request().url()).pathname);
    return route.fulfill({ json: { data: { ...readyItem, merging: true, mergeRefusal: "" } } });
  });
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");

  await expect(row.getByRole("button")).toHaveText(["Merge", "Chat", "Details"]);
  await expect(row.getByRole("link", { name: "Open PR" })).toHaveAttribute(
    "href",
    `${link}/pull/44`,
  );
  await row.getByRole("button", { name: "Merge" }).click();

  await expect(row.getByRole("button", { name: "Merging" })).toBeDisabled();
  await expect.poll(() => merged).toEqual(["/api/inbox/4/merge"]);
});

test("an item that is merging is not in the To do list and not in the counts after a load", async ({
  page,
}) => {
  await routeInbox(page, [{ ...readyItem, merging: true, mergeRefusal: "" }]);
  await page.goto("/inbox");
  const main = page.getByRole("main");

  await expect(main.getByRole("tab", { name: "To do", exact: true })).toBeVisible();
  await expect(main.getByRole("listitem")).toHaveCount(0);
  await expect(main.getByRole("button", { name: "Merging" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Inbox" }).filter({ visible: true })).toHaveText(
    /^Inbox$/,
  );
});

async function reconnect(page: Page, context: BrowserContext) {
  await context.setOffline(true);
  await expect(page.getByRole("status")).toHaveText("Offline");
  const reopened = page.waitForResponse("/api/events");
  await context.setOffline(false);
  await reopened;
}

test("a merging item leaves the To do list and the counts, and comes back with the reason of the refusal", async ({
  page,
  context,
}) => {
  let current = { ...readyItem, merging: false, mergeRefusal: "" };
  await page.route("**/api/inbox", (route) => route.fulfill({ json: { data: [current] } }));
  await page.route("**/api/inbox/*/merge", (route) =>
    route.fulfill({ json: { data: { ...readyItem, merging: true, mergeRefusal: "" } } }),
  );
  await page.goto("/inbox");
  const main = page.getByRole("main");
  const row = main.getByRole("listitem");
  const badge = page.getByRole("link", { name: "Inbox" }).filter({ visible: true });

  await expect(row).toHaveCount(1);
  await expect(main.getByRole("tab", { name: "To do 1" })).toBeVisible();
  await expect(badge).toHaveText(/^Inbox\s*1$/);

  await row.getByRole("button", { name: "Merge" }).click();
  await expect(row.getByRole("button", { name: "Merging" })).toBeDisabled();
  current = { ...readyItem, merging: true, mergeRefusal: "" };
  await reconnect(page, context);

  await expect(row).toHaveCount(0);
  await expect(main.getByRole("tab", { name: "To do", exact: true })).toBeVisible();
  await expect(badge).toHaveText(/^Inbox$/);

  const refusal = "Pull request #44 has an open review thread.";
  current = { ...readyItem, merging: false, mergeRefusal: refusal };
  await reconnect(page, context);

  await expect(row.getByText(refusal)).toBeVisible();
  await expect(row.getByRole("button", { name: "Merge" })).toBeEnabled();
  await expect(main.getByRole("tab", { name: "To do 1" })).toBeVisible();
  await expect(badge).toHaveText(/^Inbox\s*1$/);
});

test("the row shows the refusal of GitHub, and Merge works again", async ({ page }) => {
  const refusal = "Pull request #44 has an open review thread.";
  await routeInbox(page, [{ ...readyItem, merging: false, mergeRefusal: refusal }]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");

  await expect(row.getByText(refusal)).toBeVisible();
  await expect(row.getByRole("button", { name: "Merge" })).toBeEnabled();
});

test("the row shows the refusal of the route, and Merge works again", async ({ page }) => {
  await routeInbox(page, [{ ...readyItem, merging: false, mergeRefusal: "" }]);
  await page.route("**/api/inbox/*/merge", (route) =>
    route.fulfill({ status: 409, json: { error: "The task of #30 is not ready for review." } }),
  );
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");

  await row.getByRole("button", { name: "Merge" }).click();

  await expect(row.getByText("The task of #30 is not ready for review.")).toBeVisible();
  await expect(row.getByRole("button", { name: "Merge" })).toBeEnabled();
});

const todo = {
  ...base,
  id: 7,
  issue: 40,
  kind: "todo",
  title: "MOBIUS_SMTP_HOST",
  reason: "mandatory · Deployment needs",
  text: "env_var: MOBIUS_SMTP_HOST",
  link: `${link}/issues/40`,
  pullRequest: 1110,
  pullRequestUrl: `${link}/pull/1110`,
  todo: {
    kind: "env_var",
    name: "MOBIUS_SMTP_HOST",
    path: "internal/config/config.go",
    line: 42,
    mandatory: true,
    reason: "The server reads it at the start and has no default.",
    note: "",
  },
};

async function openTodo(page: Page) {
  await routeInbox(page, [todo]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  await expect(row.getByRole("button")).toHaveText(["Done…", "Chat", "Details"]);
  await expect(row.getByRole("link", { name: "Open PR" })).toHaveAttribute(
    "href",
    `${link}/pull/1110`,
  );
  await row.getByRole("button", { name: "Done…" }).click();
  return { row, panel: page.getByRole("dialog") };
}

test("Done opens a panel with the details of the need, and the note marks the todo as done", async ({
  page,
}) => {
  const bodies: unknown[] = [];
  await page.route("**/api/inbox/7/done", (route) => {
    bodies.push(route.request().postDataJSON());
    return route.fulfill({ json: { data: { ...todo, dismissedAt: "2026-09-28T09:40:00Z" } } });
  });
  const { row, panel } = await openTodo(page);

  await expect(panel.getByRole("heading", { name: "MOBIUS_SMTP_HOST" })).toBeVisible();
  await expect(panel.getByText("Mandatory env var")).toBeVisible();
  await expect(
    panel.getByText("Reason: The server reads it at the start and has no default."),
  ).toBeVisible();
  await expect(panel.getByText("File: internal/config/config.go:42")).toBeVisible();
  await expect(panel.getByRole("link", { name: "PR #1110 ↗" })).toHaveAttribute(
    "href",
    `${link}/pull/1110`,
  );
  await expect(panel.getByText("Mobius merges after this todo is done.")).toBeVisible();
  await panel.getByRole("textbox", { name: "Note" }).fill("Set in prod.");
  const response = page.waitForResponse("**/api/inbox/7/done");
  await panel.getByRole("button", { name: "Send" }).click();
  await response;

  expect(bodies).toEqual([{ note: "Set in prod." }]);
  await expect(panel).toHaveCount(0);
  await expect(row).toHaveCount(0);
});

test("a todo with an empty note is marked as done", async ({ page }) => {
  const bodies: unknown[] = [];
  await page.route("**/api/inbox/7/done", (route) => {
    bodies.push(route.request().postDataJSON());
    return route.fulfill({ json: { data: { ...todo, dismissedAt: "2026-09-28T09:40:00Z" } } });
  });
  const { row, panel } = await openTodo(page);

  await expect(panel.getByRole("textbox", { name: "Note" })).toHaveAttribute(
    "placeholder",
    "Note (optional)",
  );
  const response = page.waitForResponse("**/api/inbox/7/done");
  await panel.getByRole("button", { name: "Send" }).click();
  await response;

  expect(bodies).toEqual([{ note: "" }]);
  await expect(panel).toHaveCount(0);
  await expect(row).toHaveCount(0);
});

test("a failed done keeps the panel and shows the error", async ({ page }) => {
  await page.route("**/api/inbox/7/done", (route) =>
    route.fulfill({ status: 409, json: { error: "This item is not an open todo." } }),
  );
  const { panel } = await openTodo(page);

  await panel.getByRole("textbox", { name: "Note" }).fill("Set in prod.");
  await panel.getByRole("button", { name: "Send" }).click();

  await expect(panel.getByRole("alert")).toHaveText("This item is not an open todo.");
  await expect(panel.getByRole("textbox", { name: "Note" })).toHaveValue("Set in prod.");
});

test("Details of a todo opens the same panel with no note field", async ({ page }) => {
  await routeInbox(page, [todo]);
  await page.goto("/inbox");
  const panel = page.getByRole("dialog");

  await page.getByRole("main").getByRole("button", { name: "Details" }).click();
  await expect(panel.getByText("File: internal/config/config.go:42")).toBeVisible();
  await expect(panel.getByText("Mobius merges after this todo is done.")).toBeVisible();
  await expect(panel.getByRole("textbox")).toHaveCount(0);
});
