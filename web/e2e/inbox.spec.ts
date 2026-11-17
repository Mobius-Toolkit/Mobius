import { expect, test, type BrowserContext, type Locator, type Page } from "@playwright/test";
import { buttonNames } from "./buttons.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const link = "https://github.com/owner/shop";

const base = {
  dismissedAt: null,
  action: null,
  answer: "",
  mergeRefusal: "",
  merging: false,
  todo: null,
  withdrawn: false,
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
  blockingTodos: [],
  mergeQueuePlace: null,
  mergeQueueSize: 0,
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
  await expect.poll(() => buttonNames(rows.nth(0))).toEqual(["Answer", "Chat about #45"]);
  await expect(action(0, "Open GitHub")).toHaveAttribute("href", `${link}/issues/45`);
  await expect.poll(() => buttonNames(rows.nth(1))).toEqual(["Resume", "Chat about #42"]);
  await expect(action(1, "Open GitHub")).toHaveAttribute("href", `${link}/issues/42`);
  await expect
    .poll(() => buttonNames(rows.nth(2)))
    .toEqual(["Start again", "Chat about #41", "Details"]);
  await expect(action(3, "Open PR")).toHaveAttribute("href", `${link}/pull/44`);
  await expect
    .poll(() => buttonNames(rows.nth(4)))
    .toEqual(["Send again", "Chat about #12", "Details", "Dismiss"]);
  await expect.poll(() => buttonNames(rows.nth(5))).toEqual(["Chat about #46", "Details"]);
  await expect(rows.getByRole("button", { name: "Dismiss" })).toHaveCount(1);
});

test("a failed auto-upgrade shows its npm error and the Owner dismisses it", async ({ page }) => {
  const failed = {
    ...base,
    id: 9,
    issue: 0,
    kind: "tool upgrade",
    title: "Auto-upgrade of dsh failed",
    reason: "npm ERR! network down",
    text: "",
  };
  await routeInbox(page, [failed]);
  await page.route("**/api/inbox/9/dismiss", (route) => route.fulfill({ status: 204 }));

  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  await expect(row).toContainText("Auto-upgrade of dsh failed");
  await expect(row).toContainText("npm ERR! network down");

  const dismissed = page.waitForResponse("**/api/inbox/9/dismiss");
  await row.getByRole("button", { name: "Dismiss" }).click();

  await expect(page.getByText("Dismissed: Auto-upgrade of dsh failed")).toBeVisible();
  await dismissed;
});

test("the reason is on the line of the title on a desktop and below it on a phone", async ({
  page,
}) => {
  await routeInbox(page, items);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem").nth(1);
  const title = row.getByText("Let customers change plans");
  const reason = row.getByText("The implementer session hung");

  // One call in the page reads all boxes, so they always come from one layout.
  const layout = () =>
    row.evaluate((element) => {
      const deepest = (text: string) =>
        [...element.querySelectorAll("*")]
          .filter((e) => e.textContent?.includes(text))
          .reduce((a, b) => (a.contains(b) ? b : a));
      const titleElement = deepest("Let customers change plans");
      // scrollWidth rounds, so a cut of less than one pixel needs the width of the text itself.
      const range = document.createRange();
      range.selectNodeContents(titleElement);
      const resume = [...element.querySelectorAll("button")].find(
        (e) => e.textContent?.trim() === "Resume",
      )!;
      return {
        title: titleElement.getBoundingClientRect(),
        reason: deepest("The implementer session hung").getBoundingClientRect(),
        buttons: resume.getBoundingClientRect(),
        cut: range.getBoundingClientRect().width - titleElement.getBoundingClientRect().width,
      };
    });

  await page.setViewportSize({ width: 1280, height: 800 });
  await expect(async () => {
    const desktop = await layout();
    expect(desktop.reason.y).toBeLessThan(desktop.title.bottom);
    expect(desktop.cut).toBeLessThanOrEqual(0);
  }).toPass();

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(async () => {
    const phone = await layout();
    expect(phone.reason.y).toBeGreaterThanOrEqual(phone.title.bottom);
    expect(phone.buttons.y).toBeGreaterThanOrEqual(phone.reason.bottom);
  }).toPass();
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
  // The answer is a comment. A comment on an open task of a Workstream starts a turn of its Lead, and the fake Lead of
  // owner/shop#14 does not end that turn. The issue #43 is closed, so Mobius gives its comment no Lead event.
  const query = new URLSearchParams({
    kind: "Lead failed",
    repository: "owner/shop",
    workstream: "12",
    issue: "43",
    title: "Add season table",
    reason: "The Lead did not get your message",
  });
  const created = await page.request.post(`/e2e/inbox-items?${query}`);
  const id = Number(await created.text());
  try {
    await routeInbox(page, [{ ...items[0], id, issue: 43, link: `${link}/issues/43` }]);
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
    expect(answered.data.url).toContain("/issues/43#issuecomment-");
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
  await rows.nth(1).getByRole("button", { name: "Resume" }).click();
  await expect(panel.getByRole("textbox", { name: "Note" })).toHaveValue("");
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
  await page.getByRole("dialog").getByRole("button", { name: "Resume" }).click();
  await expect(main.getByRole("button", { name: "Resume" })).toBeHidden();
  await main.getByRole("button", { name: "Start again" }).click();

  await expect
    .poll(() => resumed)
    .toEqual([
      "/api/repositories/owner/shop/issues/42/resume",
      "/api/repositories/owner/shop/issues/41/resume",
    ]);
});

test("Resume opens a panel with the stop reason, and a send with a note answers and does not resume", async ({
  page,
}) => {
  const resumed: string[] = [];
  const answers: unknown[] = [];
  await routeInbox(page, items);
  await page.route("**/api/repositories/*/*/issues/*/resume", (route) => {
    resumed.push(new URL(route.request().url()).pathname);
    return route.fulfill({ status: 204 });
  });
  await page.route("**/api/inbox/2/answer", (route) => {
    answers.push(route.request().postDataJSON());
    return route.fulfill({ json: { data: { url: `${link}/issues/42#issuecomment-1` } } });
  });
  await page.goto("/inbox");
  const main = page.getByRole("main");
  const panel = page.getByRole("dialog");
  const row = main.getByRole("listitem").filter({ hasText: "Let customers change plans" });

  await expect(row.getByRole("button", { name: "Answer" })).toHaveCount(0);
  await row.getByRole("button", { name: "Resume" }).click();
  await expect(panel.getByText(/^Mobius stopped the session at /)).toBeVisible();
  await expect(panel.getByRole("textbox", { name: "Note" })).toHaveAttribute(
    "placeholder",
    "Note (optional)",
  );
  await panel.getByRole("textbox", { name: "Note" }).fill("Use the new limits.");
  const response = page.waitForResponse("**/api/inbox/2/answer");
  await panel.getByRole("button", { name: "Resume" }).click();
  await response;

  expect(answers).toEqual([{ text: "Use the new limits." }]);
  await expect(panel).toHaveCount(0);
  await expect(row).toHaveCount(0);
  expect(resumed).toEqual([]);
});

test("a failed Resume keeps the panel and the note, and shows the error", async ({ page }) => {
  await routeInbox(page, items);
  await page.route("**/api/inbox/2/answer", (route) =>
    route.fulfill({ status: 409, json: { error: "The task does not wait." } }),
  );
  await page.goto("/inbox");
  const panel = page.getByRole("dialog");

  await page.getByRole("main").getByRole("button", { name: "Resume" }).click();
  await panel.getByRole("textbox", { name: "Note" }).fill("Use the new limits.");
  await panel.getByRole("button", { name: "Resume" }).click();

  await expect(panel.getByRole("alert")).toHaveText("The task does not wait.");
  await expect(panel.getByRole("textbox", { name: "Note" })).toHaveValue("Use the new limits.");
});

const ownerAction = {
  ...base,
  id: 8,
  issue: 40,
  kind: "owner action",
  title: "Run `wrangler login` on the Mobius server",
  reason: "Integrate loyalty plans",
  text: "Run `wrangler login` on the Mobius server.",
  link: `${link}/issues/40`,
  pullRequest: 1110,
  pullRequestUrl: `${link}/pull/1110`,
};

async function openOwnerAction(page: Page, item: object = ownerAction) {
  await routeInbox(page, [item]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  await row.getByRole("button", { name: "Done" }).click();
  return { row, panel: page.getByRole("dialog") };
}

test("an owner action row has the badge, the pull request and the buttons Done, Open PR and Chat", async ({
  page,
}) => {
  await routeInbox(page, [ownerAction]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");

  await expect(row.locator("[data-slot=badge]")).toHaveText("Owner action");
  await expect(row).toContainText("PR #1110");
  await expect(row.getByRole("link", { name: "PR #1110" })).toHaveAttribute(
    "href",
    `${link}/pull/1110`,
  );
  await expect.poll(() => buttonNames(row)).toEqual(["Done", "Chat about PR #1110", "Details"]);
  await expect(row.getByRole("link", { name: "Open PR" })).toHaveAttribute(
    "href",
    `${link}/pull/1110`,
  );
  await expect(row.getByRole("button", { name: "Dismiss" })).toHaveCount(0);
});

test("an owner action with no pull request shows the issue and no Open PR", async ({ page }) => {
  const { row, panel } = await openOwnerAction(page, {
    ...ownerAction,
    pullRequest: 0,
    pullRequestUrl: "",
  });

  await expect(row.getByRole("link", { name: "#40", exact: true })).toHaveAttribute(
    "href",
    `${link}/issues/40`,
  );
  await expect(row.getByRole("link", { name: "Open PR" })).toHaveCount(0);
  await expect(panel.getByText("Pull request:")).toHaveCount(0);
  await expect(
    panel.getByText("Mobius starts the next round after you mark the action as done."),
  ).toBeVisible();
});

test("Done opens a panel with the action, and the note marks the action as done", async ({
  page,
}) => {
  const bodies: unknown[] = [];
  await page.route("**/api/inbox/8/done", (route) => {
    bodies.push(route.request().postDataJSON());
    return route.fulfill({
      json: { data: { ...ownerAction, dismissedAt: "2026-09-28T09:40:00Z" } },
    });
  });
  const { row, panel } = await openOwnerAction(page);

  await expect(
    panel.getByRole("heading", { name: "Run `wrangler login` on the Mobius server" }),
  ).toBeVisible();
  await expect(panel.getByRole("link", { name: "PR #1110 ↗" })).toHaveAttribute(
    "href",
    `${link}/pull/1110`,
  );
  await expect(
    panel.getByText("Mobius starts the next round after you mark the action as done."),
  ).toBeVisible();
  await expect(panel.getByRole("textbox", { name: "Note" })).toHaveAttribute(
    "placeholder",
    "Note (optional)",
  );
  await panel.getByRole("textbox", { name: "Note" }).fill("Logged in.");
  const response = page.waitForResponse("**/api/inbox/8/done");
  await panel.getByRole("button", { name: "Send" }).click();
  await response;

  expect(bodies).toEqual([{ note: "Logged in." }]);
  await expect(panel).toHaveCount(0);
  await expect(row).toHaveCount(0);
});

test("a failed done of an owner action keeps the panel and the note", async ({ page }) => {
  await page.route("**/api/inbox/8/done", (route) =>
    route.fulfill({ status: 409, json: { error: "The task does not wait for the action." } }),
  );
  const { panel } = await openOwnerAction(page);

  await panel.getByRole("textbox", { name: "Note" }).fill("Logged in.");
  await panel.getByRole("button", { name: "Send" }).click();

  await expect(panel.getByRole("alert")).toHaveText("The task does not wait for the action.");
  await expect(panel.getByRole("textbox", { name: "Note" })).toHaveValue("Logged in.");
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

  await expect.poll(() => buttonNames(row)).toEqual(["Merge", "Chat about PR #44", "Details"]);
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

  await expect(main.getByText("Inbox", { exact: true })).toBeVisible();
  await expect(main.getByRole("listitem")).toHaveCount(0);
  await expect(main.getByRole("button", { name: "Merging" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Inbox" }).filter({ visible: true })).toHaveText(
    /^Inbox$/,
  );
});

async function reconnect(page: Page, context: BrowserContext) {
  await context.setOffline(true);
  await expect(page.getByRole("status").filter({ hasText: "Offline" })).toHaveText("Offline");
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
  await expect(badge).toHaveText(/^Inbox\s*1$/);

  await row.getByRole("button", { name: "Merge" }).click();
  await expect(row.getByRole("button", { name: "Merging" })).toBeDisabled();
  current = { ...readyItem, merging: true, mergeRefusal: "" };
  await reconnect(page, context);

  await expect(row).toHaveCount(0);
  await expect(badge).toHaveText(/^Inbox$/);

  const refusal = "Pull request #44 has an open review thread.";
  current = { ...readyItem, merging: false, mergeRefusal: refusal };
  await reconnect(page, context);

  await expect(row.getByText(refusal)).toBeVisible();
  await expect(row.getByRole("button", { name: "Merge" })).toBeEnabled();
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
  pullRequest: 44,
  pullRequestUrl: `${link}/pull/44`,
  todo: {
    kind: "env_var",
    name: "MOBIUS_SMTP_HOST",
    path: "internal/config/config.go",
    line: 42,
    mandatory: true,
    reason: "The server reads it at the start and has no default.",
    note: "",
    dismissable: false,
    afterMerge: false,
  },
};

const optionalTodo = {
  ...todo,
  id: 8,
  title: "MOBIUS_SMTP_TIMEOUT",
  reason: "optional · Deployment needs",
  todo: {
    ...todo.todo,
    name: "MOBIUS_SMTP_TIMEOUT",
    mandatory: false,
    dismissable: true,
    reason: "The server waits 10 seconds when the variable is not set.",
  },
};

const afterMergeTodo = {
  ...todo,
  id: 9,
  title: "roles.lead.context",
  todo: {
    ...todo.todo,
    kind: "config",
    name: "roles.lead.context",
    reason: "An older release refuses the key.",
    afterMerge: true,
  },
};

const blockedReadyItem = {
  ...readyItem,
  blockingTodos: [7, 8],
  mergeRefusal: "Mobius merges after you close the open todos.",
};

const todoLine = (row: Locator, name: string) =>
  row.locator("[data-todo-item]").filter({ hasText: name });

async function openTodo(page: Page) {
  await routeInbox(page, [blockedReadyItem, todo]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  await row.getByRole("button", { name: "Complete MOBIUS_SMTP_HOST" }).click();
  return { row, panel: page.getByRole("dialog") };
}

test("a pull request shows its todos as lines inside one row", async ({ page }) => {
  await routeInbox(page, [afterMergeTodo, blockedReadyItem, optionalTodo, todo]);
  await page.goto("/inbox");
  const main = page.getByRole("main");
  const row = main.getByRole("listitem");
  const badge = page.getByRole("link", { name: "Inbox" }).filter({ visible: true });

  await expect(row).toHaveCount(1);
  await expect(row).toContainText("PR #44Name the plan tiers");
  await expect(main.locator("[data-todo-item]")).toHaveCount(3);
  await expect
    .poll(() =>
      main
        .locator("[data-todo-item]")
        .evaluateAll((lines) => lines.map((line) => line.getAttribute("data-todo-item"))),
    )
    .toEqual(["7", "8", "9"]);
  await expect
    .poll(() => buttonNames(row))
    .toEqual([
      "Chat about PR #44",
      "Details",
      "Complete MOBIUS_SMTP_HOST",
      "Complete MOBIUS_SMTP_TIMEOUT",
      "Dismiss MOBIUS_SMTP_TIMEOUT",
      "Complete roles.lead.context",
    ]);
  await expect(row.getByRole("button", { name: "Merge" })).toHaveCount(0);
  await expect(row.getByText("Mobius merges after you close the open todos.")).toBeVisible();
  await expect(
    todoLine(row, "MOBIUS_SMTP_HOST").getByText(
      "The server reads it at the start and has no default.",
    ),
  ).toBeVisible();
  await expect(todoLine(row, "MOBIUS_SMTP_HOST").locator("[data-slot=badge]")).toHaveCount(0);
  await expect(todoLine(row, "MOBIUS_SMTP_TIMEOUT").locator("[data-slot=badge]")).toHaveText(
    "optional",
  );
  await expect(todoLine(row, "roles.lead.context").locator("[data-slot=badge]")).toHaveText(
    "after merge",
  );
  await expect(badge).toHaveText(/^Inbox\s*1$/);
});

test("Complete on a line opens the panel, and the note removes the line", async ({ page }) => {
  const bodies: unknown[] = [];
  await page.route("**/api/inbox/7/done", (route) => {
    bodies.push(route.request().postDataJSON());
    return route.fulfill({ json: { data: { ...todo, dismissedAt: "2026-09-28T09:40:00Z" } } });
  });
  await routeInbox(page, [blockedReadyItem, todo, optionalTodo]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  const panel = page.getByRole("dialog");
  const badge = page.getByRole("link", { name: "Inbox" }).filter({ visible: true });

  await row.getByRole("button", { name: "Complete MOBIUS_SMTP_HOST" }).click();
  await expect(panel.getByRole("heading", { name: "MOBIUS_SMTP_HOST" })).toBeVisible();
  await expect(panel.getByText("Mandatory env var")).toBeVisible();
  await expect(
    panel.getByText("Reason: The server reads it at the start and has no default."),
  ).toBeVisible();
  await expect(panel.getByText("File: internal/config/config.go:42")).toBeVisible();
  await expect(panel.getByRole("link", { name: "PR #44 ↗" })).toHaveAttribute(
    "href",
    `${link}/pull/44`,
  );
  await expect(panel.getByText("This todo blocks the merge until you complete it.")).toBeVisible();
  await panel.getByRole("textbox", { name: "Note" }).fill("Set in prod.");
  const response = page.waitForResponse("**/api/inbox/7/done");
  await panel.getByRole("button", { name: "Send" }).click();
  await response;

  expect(bodies).toEqual([{ note: "Set in prod." }]);
  await expect(panel).toHaveCount(0);
  await expect(todoLine(row, "MOBIUS_SMTP_HOST")).toHaveCount(0);
  await expect(todoLine(row, "MOBIUS_SMTP_TIMEOUT")).toHaveCount(1);
  await expect(badge).toHaveText(/^Inbox\s*1$/);
});

test("Dismiss on an optional line removes the line", async ({ page, context }) => {
  let current = [blockedReadyItem, todo, optionalTodo];
  await page.route("**/api/inbox", (route) => route.fulfill({ json: { data: current } }));
  await page.route("**/api/inbox/8/dismiss", (route) => {
    current = [blockedReadyItem, todo];
    return route.fulfill({ status: 204 });
  });
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  const badge = page.getByRole("link", { name: "Inbox" }).filter({ visible: true });

  await expect(row.getByRole("button", { name: "Dismiss MOBIUS_SMTP_HOST" })).toHaveCount(0);
  const dismissed = page.waitForResponse("**/api/inbox/8/dismiss");
  await row.getByRole("button", { name: "Dismiss MOBIUS_SMTP_TIMEOUT" }).click();
  await dismissed;

  await expect(page.getByText("Dismissed: MOBIUS_SMTP_TIMEOUT")).toBeVisible();
  await reconnect(page, context);
  await expect(todoLine(row, "MOBIUS_SMTP_TIMEOUT")).toHaveCount(0);
  await expect(todoLine(row, "MOBIUS_SMTP_HOST")).toHaveCount(1);
  await expect(badge).toHaveText(/^Inbox\s*1$/);
});

test("the refusal of Dismiss shows, and the line keeps Dismiss", async ({ page }) => {
  await routeInbox(page, [blockedReadyItem, todo, optionalTodo]);
  await page.route("**/api/inbox/8/dismiss", (route) =>
    route.fulfill({
      status: 409,
      json: { error: "A mandatory todo cannot be dismissed. Mark it as done." },
    }),
  );
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");

  await row.getByRole("button", { name: "Dismiss MOBIUS_SMTP_TIMEOUT" }).click();

  await expect(
    page.getByText("A mandatory todo cannot be dismissed. Mark it as done."),
  ).toBeVisible();
  await expect(row.getByRole("button", { name: "Dismiss MOBIUS_SMTP_TIMEOUT" })).toBeEnabled();
});

test("the panel of an optional todo says that it blocks the merge until it is completed or dismissed", async ({
  page,
}) => {
  await routeInbox(page, [blockedReadyItem, optionalTodo]);
  await page.goto("/inbox");

  await page
    .getByRole("main")
    .getByRole("button", { name: "Complete MOBIUS_SMTP_TIMEOUT" })
    .click();
  await expect(
    page
      .getByRole("dialog")
      .getByText("This todo blocks the merge until you complete or dismiss it."),
  ).toBeVisible();
});

test("the panel of an after-merge todo says that it does not block the merge", async ({ page }) => {
  await routeInbox(page, [blockedReadyItem, afterMergeTodo]);
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Complete roles.lead.context" }).click();
  const panel = page.getByRole("dialog");

  await expect(
    panel.getByText("Do this step after the merge. This todo does not block the merge."),
  ).toBeVisible();
  await expect(panel.getByText("This todo blocks the merge until you complete it.")).toHaveCount(0);
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
  await expect(todoLine(row, "MOBIUS_SMTP_HOST")).toHaveCount(0);
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

test("an after-merge todo with no row ready for review shows the row Deployment todos", async ({
  page,
}) => {
  await page.route("**/api/inbox/9/done", (route) =>
    route.fulfill({ json: { data: { ...afterMergeTodo, dismissedAt: "2026-09-28T09:40:00Z" } } }),
  );
  await routeInbox(page, [afterMergeTodo]);
  await page.goto("/inbox");
  const main = page.getByRole("main");
  const row = main.getByRole("listitem");

  await expect(row).toHaveCount(1);
  await expect(row.getByText("Deployment todos")).toBeVisible();
  await expect(row.getByRole("link", { name: "PR #44" })).toHaveAttribute(
    "href",
    `${link}/pull/44`,
  );
  await expect(row.getByRole("link", { name: "Open PR" })).toHaveAttribute(
    "href",
    `${link}/pull/44`,
  );
  await expect(todoLine(row, "roles.lead.context")).toHaveCount(1);
  await expect(todoLine(row, "roles.lead.context").locator("[data-slot=badge]")).toHaveText(
    "after merge",
  );
  await expect
    .poll(() => buttonNames(row))
    .toEqual(["Chat about PR #44", "Complete roles.lead.context"]);

  await row.getByRole("button", { name: "Complete roles.lead.context" }).click();
  const response = page.waitForResponse("**/api/inbox/9/done");
  await page.getByRole("dialog").getByRole("button", { name: "Send" }).click();
  await response;

  await expect(row).toHaveCount(0);
});

test("a row with four todos shows three lines and Show 1 more", async ({ page }) => {
  const port = {
    ...todo,
    id: 10,
    title: "MOBIUS_SMTP_PORT",
    todo: { ...todo.todo, name: "MOBIUS_SMTP_PORT" },
  };
  await routeInbox(page, [
    { ...blockedReadyItem, blockingTodos: [7, 8, 10] },
    todo,
    optionalTodo,
    afterMergeTodo,
    port,
  ]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");

  await expect(row.locator("[data-todo-item]")).toHaveCount(3);
  await expect(todoLine(row, "MOBIUS_SMTP_PORT")).toHaveCount(0);

  await row.getByRole("button", { name: "Show 1 more" }).click();

  await expect(row.locator("[data-todo-item]")).toHaveCount(4);
  await expect(todoLine(row, "MOBIUS_SMTP_PORT")).toHaveCount(1);
  await expect(row.getByRole("button", { name: "Show 1 more" })).toHaveCount(0);

  await row.getByRole("button", { name: "Show less" }).click();

  await expect(row.locator("[data-todo-item]")).toHaveCount(3);
  await expect(row.getByRole("button", { name: "Show 1 more" })).toBeVisible();
});

test("the ready row shows Merge again after its last blocking todo closes", async ({
  page,
  context,
}) => {
  let current = [blockedReadyItem, todo, optionalTodo];
  await page.route("**/api/inbox", (route) => route.fulfill({ json: { data: current } }));
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");

  await expect(row.getByRole("button", { name: "Merge" })).toHaveCount(0);
  await expect(row.getByRole("button", { name: /open todo/ })).toHaveCount(0);

  current = [{ ...readyItem, mergeRefusal: "" }];
  await reconnect(page, context);

  await expect(row.getByRole("button", { name: "Merge" })).toBeVisible();
  await expect(row.locator("[data-todo-item]")).toHaveCount(0);
});

test("the tray of a Workstream shows the todos of a pull request in one row", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await routeInbox(page, [blockedReadyItem, todo, optionalTodo]);
  await page.goto("/workstreams/owner/shop/12");
  const list = page.getByRole("main").getByRole("list", { name: "Pending actions" });

  await expect(page.getByRole("button", { name: "Waits for you (1)" })).toBeVisible();
  await expect(list.getByRole("listitem")).toHaveCount(1);
  await expect(list.locator("[data-todo-item]")).toHaveCount(2);
  expect(await list.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
});
