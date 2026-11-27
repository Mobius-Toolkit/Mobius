import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { buttonNames } from "./buttons.js";
import { attachToPanel, planItem, quotedPart } from "./drafts.js";
import { formFields } from "./forms.js";

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

// Opens the panel of the question #45 with an image, an Inbox item and a selection in its field.
async function attachToAnswer(page: Page) {
  await page.goto("/inbox");
  const panel = await attachToPanel(page, "answerDraft:1", () =>
    page.getByRole("main").getByRole("button", { name: "Answer" }).first().click(),
  );
  await panel.getByRole("textbox", { name: "Your answer" }).fill("Ten seats.");
  return panel;
}

test("the field of the answer sends the image, the Inbox item and the selection with the text", async ({
  page,
}) => {
  const bodies: string[] = [];
  await routeInbox(page, items);
  await page.route("**/api/inbox/1/answer", (route) => {
    bodies.push(route.request().postData() ?? "");
    return route.fulfill({ json: { data: { url: `${link}/issues/45#issuecomment-1` } } });
  });
  const panel = await attachToAnswer(page);

  const response = page.waitForResponse("**/api/inbox/1/answer");
  await panel.getByRole("button", { name: "Send" }).click();
  await response;

  expect(bodies).toHaveLength(1);
  expect(bodies[0]).toContain('name="images"; filename="');
  const fields = formFields(bodies[0]);
  expect(fields.text).toBe("Ten seats.");
  expect(fields.voice).toBe("false");
  expect(JSON.parse(fields.inboxItems)).toEqual({
    reference: planItem.reference,
    title: planItem.title,
    reason: planItem.reason,
    link: planItem.link,
  });
  expect(JSON.parse(fields.selections)).toEqual(quotedPart);
  await expect(panel).toHaveCount(0);
});

test("a failed answer puts the image, the Inbox item, the selection and the text back in the field", async ({
  page,
}) => {
  await routeInbox(page, items);
  await page.route("**/api/inbox/1/answer", (route) =>
    route.fulfill({ status: 409, json: { error: "The Owner must authorize the Mobius App." } }),
  );
  const panel = await attachToAnswer(page);

  await panel.getByRole("button", { name: "Send" }).click();

  await expect(panel.getByRole("alert")).toHaveText("The Owner must authorize the Mobius App.");
  await expect(panel.getByRole("textbox", { name: "Your answer" })).toHaveValue("Ten seats.");
  await expect(panel.getByRole("img", { name: "Image 1" })).toBeVisible();
  await expect(panel.locator("[data-attachment-card='inbox-item']")).toContainText(
    "Add plan model",
  );
  await expect(panel.locator("[data-attachment-card='selection']")).toContainText("works on #41");
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
    answers.push(formFields(route.request().postData() ?? ""));
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

  expect(answers).toEqual([{ text: "Use the new limits.", voice: "false" }]);
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
    bodies.push(formFields(route.request().postData() ?? ""));
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

  expect(bodies).toEqual([{ note: "Logged in.", voice: "false" }]);
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

async function openTodo(page: Page) {
  await routeInbox(page, [blockedReadyItem, todo]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  await row.getByRole("button", { name: "Complete todos (1)" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "MOBIUS_SMTP_HOST" }).click();
  return { row, panel: page.getByRole("dialog") };
}

test("a pull request shows Complete todos, opens its list on tap, and returns after an answer", async ({
  page,
}) => {
  const bodies: unknown[] = [];
  await page.route("**/api/inbox/7/done", (route) => {
    bodies.push(formFields(route.request().postData() ?? ""));
    return route.fulfill({ json: { data: { ...todo, dismissedAt: "2026-09-28T09:40:00Z" } } });
  });
  await routeInbox(page, [afterMergeTodo, blockedReadyItem, optionalTodo, todo]);
  await page.goto("/inbox");
  const main = page.getByRole("main");
  const row = main.getByRole("listitem");
  const badge = page.getByRole("link", { name: "Inbox" }).filter({ visible: true });

  await expect(row).toHaveCount(1);
  await expect(row).toContainText("PR #44Name the plan tiers");
  await expect(row.locator("[data-todo-item]")).toHaveCount(0);
  await expect(row.getByText("MOBIUS_SMTP_HOST")).toHaveCount(0);
  await expect(row.getByText("MOBIUS_SMTP_TIMEOUT")).toHaveCount(0);
  await expect(row.getByText("roles.lead.context")).toHaveCount(0);
  await expect
    .poll(() => buttonNames(row))
    .toEqual(["Complete todos (3)", "Chat about PR #44", "Details"]);
  await expect(row.getByRole("button", { name: "Merge" })).toHaveCount(0);
  await expect(row.getByText("Mobius merges after you close the open todos.")).toBeVisible();
  await expect(badge).toHaveText(/^Inbox\s*1$/);

  await row.getByRole("button", { name: "Details" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Name the plan tiers" })).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toHaveCount(0);

  await row.getByText("Name the plan tiers").click();
  await expect(dialog.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();
  await expect
    .poll(async () => (await buttonNames(dialog)).filter((name) => name !== "Close"))
    .toEqual(["MOBIUS_SMTP_HOST", "MOBIUS_SMTP_TIMEOUT", "roles.lead.context"]);

  const mandatoryLine = dialog.getByRole("button", { name: "MOBIUS_SMTP_HOST" });
  const optionalLine = dialog.getByRole("button", { name: "MOBIUS_SMTP_TIMEOUT" });
  const afterMergeLine = dialog.getByRole("button", { name: "roles.lead.context" });
  await expect(mandatoryLine.locator("[data-slot=badge]")).toHaveCount(0);
  await expect(optionalLine.locator("[data-slot=badge]")).toHaveText("optional");
  await expect(afterMergeLine.locator("[data-slot=badge]")).toHaveText("after merge");

  await mandatoryLine.click();
  await expect(dialog.getByRole("heading", { name: "MOBIUS_SMTP_HOST" })).toBeVisible();
  await expect(dialog.getByText("Mandatory env var")).toBeVisible();
  await expect(
    dialog.getByText("Reason: The server reads it at the start and has no default."),
  ).toBeVisible();
  await expect(dialog.getByText("File: internal/config/config.go:42")).toBeVisible();
  await expect(dialog.getByRole("link", { name: "PR #44 ↗" })).toHaveAttribute(
    "href",
    `${link}/pull/44`,
  );
  await expect(dialog.getByText("This todo blocks the merge until you complete it.")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Dismiss" })).toHaveCount(0);
  await dialog.getByRole("textbox", { name: "Note" }).fill("Set in prod.");
  const response = page.waitForResponse("**/api/inbox/7/done");
  await dialog.getByRole("button", { name: "Send" }).click();
  await response;

  expect(bodies).toEqual([{ note: "Set in prod.", voice: "false" }]);
  await expect(dialog.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();
  await expect
    .poll(async () => (await buttonNames(dialog)).filter((name) => name !== "Close"))
    .toEqual(["MOBIUS_SMTP_TIMEOUT", "roles.lead.context"]);
  await expect(row.getByRole("button", { name: "Complete todos (2)" })).toBeVisible();
  await expect(badge).toHaveText(/^Inbox\s*1$/);

  await optionalLine.click();
  await expect(dialog.getByRole("heading", { name: "MOBIUS_SMTP_TIMEOUT" })).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});

test("the todo panel shows Back only when the todo list opened it", async ({ page }) => {
  await routeInbox(page, [blockedReadyItem, todo, optionalTodo]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  const dialog = page.getByRole("dialog");
  const back = dialog.getByRole("button", { name: "Back" });

  await row.getByRole("button", { name: "Details" }).click();
  await expect(dialog.getByRole("heading", { name: "Name the plan tiers" })).toBeVisible();
  await expect(back).toHaveCount(0);
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toHaveCount(0);

  await row.getByRole("button", { name: "Complete todos (2)" }).click();
  await expect(dialog.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();
  await expect(back).toHaveCount(0);
  await dialog.getByRole("button", { name: "MOBIUS_SMTP_HOST" }).click();
  await expect(dialog.getByRole("heading", { name: "MOBIUS_SMTP_HOST" })).toBeVisible();
  await expect(back).toBeVisible();
  await back.click();
  await expect(dialog.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "MOBIUS_SMTP_HOST" })).toBeVisible();
  await expect(back).toHaveCount(0);
});

test("the todo list fits a phone with a long todo name that has no space", async ({ page }) => {
  const longName =
    "internal/config/roles/lead/context/default_context_file_path_for_the_lead_role.toml";
  const longTodo = {
    ...optionalTodo,
    id: 10,
    title: longName,
    todo: { ...optionalTodo.todo, name: longName, afterMerge: true },
  };
  await page.setViewportSize({ width: 360, height: 800 });
  await routeInbox(page, [blockedReadyItem, todo, longTodo]);
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  await row.getByRole("button", { name: "Complete todos (2)" }).click();
  const dialog = page.getByRole("dialog");
  const line = dialog.getByRole("button", { name: longName });
  await expect(line).toBeVisible();
  await expect(line.locator("[data-slot=badge]")).toHaveText(["optional", "after merge"]);
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  const body = line.locator("..");
  expect(await body.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  const sheet = await dialog.boundingBox();
  const chevron = await line.locator("svg").boundingBox();
  expect(chevron!.x + chevron!.width).toBeLessThanOrEqual(sheet!.x + sheet!.width);
});

test("the todo panel of the tray shows Back on a phone and returns to the list", async ({
  page,
}) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await routeInbox(page, [blockedReadyItem, todo, optionalTodo]);
  await page.goto("/workstreams/owner/shop/12");
  const list = page.getByRole("main").getByRole("list", { name: "Pending actions" });
  const sheet = page.getByRole("dialog");
  const back = sheet.getByRole("button", { name: "Back" });

  await list.getByRole("button", { name: "Complete todos (2)" }).click();
  await sheet.getByRole("button", { name: "MOBIUS_SMTP_TIMEOUT" }).click();
  await expect(sheet.getByRole("heading", { name: "MOBIUS_SMTP_TIMEOUT" })).toBeVisible();
  await expect(back).toBeVisible();
  expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await back.click();
  await expect(sheet.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();
});

test("Dismiss on an optional todo panel returns to the list without the todo", async ({
  page,
  context,
}) => {
  let current = [blockedReadyItem, todo, optionalTodo];
  await page.route("**/api/inbox", (route) => route.fulfill({ json: { data: current } }));
  await page.route("**/api/inbox/8/dismiss", (route) => {
    current = [blockedReadyItem, todo];
    return route.fulfill({ status: 204 });
  });
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  const dialog = page.getByRole("dialog");
  const badge = page.getByRole("link", { name: "Inbox" }).filter({ visible: true });

  await row.getByRole("button", { name: "Complete todos (2)" }).click();
  await dialog.getByRole("button", { name: "MOBIUS_SMTP_TIMEOUT" }).click();
  const dismissed = page.waitForResponse("**/api/inbox/8/dismiss");
  await dialog.getByRole("button", { name: "Dismiss" }).click();
  await dismissed;

  await expect(page.getByText("Dismissed: MOBIUS_SMTP_TIMEOUT")).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();
  await expect
    .poll(async () => (await buttonNames(dialog)).filter((name) => name !== "Close"))
    .toEqual(["MOBIUS_SMTP_HOST"]);
  await expect(row.getByRole("button", { name: "Complete todos (1)" })).toBeVisible();

  await reconnect(page, context);
  await expect(dialog.getByRole("button", { name: "MOBIUS_SMTP_TIMEOUT" })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "MOBIUS_SMTP_HOST" })).toBeVisible();
  await expect(badge).toHaveText(/^Inbox\s*1$/);
});

test("the refusal of Dismiss shows in the panel and keeps Dismiss enabled", async ({ page }) => {
  await routeInbox(page, [blockedReadyItem, todo, optionalTodo]);
  await page.route("**/api/inbox/8/dismiss", (route) =>
    route.fulfill({
      status: 409,
      json: { error: "A mandatory todo cannot be dismissed. Mark it as done." },
    }),
  );
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");
  const panel = page.getByRole("dialog");

  await row.getByRole("button", { name: "Complete todos (2)" }).click();
  await panel.getByRole("button", { name: "MOBIUS_SMTP_TIMEOUT" }).click();
  const refused = page.waitForResponse("**/api/inbox/8/dismiss");
  await panel.getByRole("button", { name: "Dismiss" }).click();
  await refused;

  await expect(panel.getByRole("alert")).toHaveText(
    "A mandatory todo cannot be dismissed. Mark it as done.",
  );
  await expect(panel.getByRole("button", { name: "Dismiss" })).toBeEnabled();
});

test("the panel of an optional todo says that it blocks the merge until it is completed or dismissed", async ({
  page,
}) => {
  await routeInbox(page, [blockedReadyItem, optionalTodo]);
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Complete todos (1)" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "MOBIUS_SMTP_TIMEOUT" }).click();
  await expect(
    dialog.getByText("This todo blocks the merge until you complete or dismiss it."),
  ).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Dismiss" })).toBeVisible();
});

test("the panel of an after-merge todo says that it does not block the merge", async ({ page }) => {
  await routeInbox(page, [blockedReadyItem, afterMergeTodo]);
  await page.goto("/inbox");

  await page.getByRole("main").getByRole("button", { name: "Complete todos (1)" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "roles.lead.context" }).click();

  await expect(
    dialog.getByText("Do this step after the merge. This todo does not block the merge."),
  ).toBeVisible();
  await expect(dialog.getByText("This todo blocks the merge until you complete it.")).toHaveCount(
    0,
  );
});

test("a todo with an empty note is marked as done and closes the list when no todo remains", async ({
  page,
}) => {
  const bodies: unknown[] = [];
  await page.route("**/api/inbox/7/done", (route) => {
    bodies.push(formFields(route.request().postData() ?? ""));
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

  expect(bodies).toEqual([{ note: "", voice: "false" }]);
  await expect(panel).toHaveCount(0);
  await expect(row.getByRole("button", { name: /Complete todos/ })).toHaveCount(0);
});

test("a failed done keeps the panel and shows the error", async ({ page }) => {
  await page.route("**/api/inbox/7/done", (route) =>
    route.fulfill({ status: 409, json: { error: "This item is not an open todo." } }),
  );
  const { panel } = await openTodo(page);

  await panel.getByRole("textbox", { name: "Note" }).fill("Set in prod.");
  const response = page.waitForResponse("**/api/inbox/7/done");
  await panel.getByRole("button", { name: "Send" }).click();
  await response;

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
  await expect(row.getByText("roles.lead.context")).toHaveCount(0);
  await expect(row.getByRole("link", { name: "PR #44" })).toHaveAttribute(
    "href",
    `${link}/pull/44`,
  );
  await expect(row.getByRole("link", { name: "Open PR" })).toHaveAttribute(
    "href",
    `${link}/pull/44`,
  );
  await expect.poll(() => buttonNames(row)).toEqual(["Complete todos (1)", "Chat about PR #44"]);

  await row.getByRole("button", { name: "Complete todos (1)" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "roles.lead.context" }).locator("[data-slot=badge]"),
  ).toHaveText("after merge");
  await dialog.getByRole("button", { name: "roles.lead.context" }).click();
  const response = page.waitForResponse("**/api/inbox/9/done");
  await dialog.getByRole("button", { name: "Send" }).click();
  await response;

  await expect(dialog).toHaveCount(0);
  await expect(row).toHaveCount(0);
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
  await expect(row.getByRole("button", { name: "Complete todos (2)" })).toBeVisible();

  current = [{ ...readyItem, mergeRefusal: "" }];
  await reconnect(page, context);

  await expect(row.getByRole("button", { name: "Merge" })).toBeVisible();
  await expect(row.getByRole("button", { name: /Complete todos/ })).toHaveCount(0);
});

test("the tray of a Workstream shows the todos of a pull request in one row and opens the sheet on phone", async ({
  page,
}) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await routeInbox(page, [blockedReadyItem, todo, optionalTodo]);
  await page.goto("/workstreams/owner/shop/12");
  const list = page.getByRole("main").getByRole("list", { name: "Pending actions" });

  await expect(page.getByRole("button", { name: "Waits for you (1)" })).toBeVisible();
  await expect(list.getByRole("listitem")).toHaveCount(1);
  await expect(list.getByRole("button", { name: "Complete todos (2)" })).toBeVisible();
  await expect(list.getByText("MOBIUS_SMTP_HOST")).toHaveCount(0);
  expect(await list.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);

  await list.getByRole("button", { name: "Complete todos (2)" }).click();
  const sheet = page.getByRole("dialog");
  await expect(sheet.getByRole("heading", { name: "Todos of PR #44" })).toBeVisible();
  expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
});

test("a tap on a disabled Merging button in a row with non-blocking todos does not open the todo list", async ({
  page,
}) => {
  await routeInbox(page, [{ ...readyItem, mergeRefusal: "" }, afterMergeTodo]);
  await page.route("**/api/inbox/4/merge", (route) =>
    route.fulfill({ json: { data: { ...readyItem, merging: true, mergeRefusal: "" } } }),
  );
  await page.goto("/inbox");
  const row = page.getByRole("main").getByRole("listitem");

  await expect(row.getByRole("button", { name: "Complete todos (1)" })).toBeVisible();
  const mergeDone = page.waitForResponse("**/api/inbox/4/merge");
  await row.getByRole("button", { name: "Merge" }).click();
  await mergeDone;

  const merging = row.getByRole("button", { name: "Merging" });
  await expect(merging).toBeDisabled();
  const box = await merging.boundingBox();
  expect(box).not.toBeNull();
  if (!box) throw new Error("Missing Merging button bounds");
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  await page.evaluate(
    () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))),
  );
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await row.getByText("Name the plan tiers").click();
  await expect(
    page.getByRole("dialog").getByRole("heading", { name: "Todos of PR #44" }),
  ).toBeVisible();
});
