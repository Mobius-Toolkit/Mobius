import { expect, test, type Page } from "@playwright/test";
import { png } from "./images.js";
import { stoppedItems } from "./stops.js";

const link = "https://github.com/owner/shop";
const shop = "/workstreams/owner/shop/12";
const [stopped] = stoppedItems;

// An Inbox item that no Workstream owns.
const diskItem = {
  ...stopped,
  id: 301,
  issue: 46,
  kind: "full disk",
  workstream: 0,
  title: "Free disk space",
  reason: "The local check waits for free disk",
  text: "The disk is full.",
  link: `${link}/issues/46`,
};

// The tests have no DOM types, so the scripts read the draft store in the browser.
const storedDrafts = `new Promise((resolve) => {
  const open = indexedDB.open("mobius");
  open.onsuccess = () => {
    const count = open.result.transaction("drafts").objectStore("drafts").count();
    count.onsuccess = () => resolve(count.result);
  };
})`;

const storedParts = `new Promise((resolve) => {
  const open = indexedDB.open("mobius");
  open.onsuccess = () => {
    const all = open.result.transaction("drafts").objectStore("drafts").getAll();
    all.onsuccess = () =>
      resolve(all.result.map((draft) => [draft.text, draft.images.length, draft.inboxItems.length]));
  };
})`;

const clearDrafts = `new Promise((resolve) => {
  const open = indexedDB.open("mobius");
  open.onsuccess = () => {
    const clear = open.result.transaction("drafts", "readwrite").objectStore("drafts").clear();
    clear.onsuccess = () => resolve(undefined);
  };
})`;

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
  await page.route("**/api/inbox", (route) =>
    route.fulfill({ json: { data: [stopped, diskItem] } }),
  );
});

test.afterEach(async ({ page }) => {
  await page.evaluate(clearDrafts);
  await expect.poll(() => page.evaluate(storedDrafts)).toBe(0);
});

const attached = (page: Page, item: { reference: string; title: string; reason: string }) => {
  const composer = page.getByRole("main").locator("form");
  return [
    composer.getByText(item.title),
    composer.getByText(item.reason),
    composer.getByRole("button", { name: `Remove ${item.reference}` }),
  ];
};

const stoppedCard = { reference: "#41", title: stopped.title, reason: stopped.reason };
const diskCard = { reference: "#46", title: diskItem.title, reason: diskItem.reason };

test("Chat on an Inbox item of a Workstream opens its chat with the item in the composer", async ({
  page,
}) => {
  await page.goto("/inbox");
  await page
    .getByRole("main")
    .getByRole("listitem")
    .filter({ hasText: stopped.title })
    .getByRole("button", { name: "Chat" })
    .click();

  await expect(page).toHaveURL(shop);
  await expect(page.getByLabel("Message to the Lead")).toBeVisible();
  for (const card of attached(page, stoppedCard)) {
    await expect(card).toBeVisible();
  }
  await expect(
    page
      .getByRole("main")
      .locator("form")
      .getByRole("button", { name: /^Remove / }),
  ).toHaveCount(1);
});

test("Chat on an Inbox item with no Workstream opens the Coordinator chat with the item in the composer", async ({
  page,
}) => {
  await page.goto("/inbox");
  await page
    .getByRole("main")
    .getByRole("listitem")
    .filter({ hasText: diskItem.title })
    .getByRole("button", { name: "Chat" })
    .click();

  await expect(page).toHaveURL("/chat");
  await expect(page.getByLabel("Message to the Coordinator")).toBeVisible();
  for (const card of attached(page, diskCard)) {
    await expect(card).toBeVisible();
  }
  await expect(
    page
      .getByRole("main")
      .locator("form")
      .getByRole("button", { name: /^Remove / }),
  ).toHaveCount(1);
});

test("the draft keeps its text and image after a reload, and Chat adds the item to it", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const composer = main.locator("form");
  const message = page.getByLabel("Message to the Lead");
  const image = composer.getByRole("img", { name: "Image 1" });
  const row = main.getByRole("listitem").filter({ hasText: stopped.title });

  await page.goto(shop);
  await message.fill("This is the new plan page.");
  await composer.locator("input[type=file]").setInputFiles({
    name: "plan.png",
    mimeType: "image/png",
    buffer: await png(page, 200, 150, "#2563eb"),
  });
  await expect(image).toHaveJSProperty("naturalWidth", 200);
  await expect
    .poll(() => page.evaluate(storedParts))
    .toEqual([["This is the new plan page.", 1, 0]]);

  await page.reload();
  await expect(message).toHaveValue("This is the new plan page.");
  await expect(image).toHaveJSProperty("naturalWidth", 200);
  await expect(composer.getByRole("button", { name: /^Remove #/ })).toHaveCount(0);

  await row.getByRole("button", { name: "Chat" }).click();
  for (const card of attached(page, stoppedCard)) {
    await expect(card).toBeVisible();
  }
  await expect(message).toHaveValue("This is the new plan page.");
  await expect(image).toHaveJSProperty("naturalWidth", 200);
  await expect
    .poll(() => page.evaluate(storedParts))
    .toEqual([["This is the new plan page.", 1, 1]]);
});

test("the bubble of a sent message shows the card of the Inbox item with its link", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const text = "Please look at this item.";
  let posted: { reference: string; title: string; reason: string; link: string }[] | undefined;
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data: { messages: { id: number; time: string }[] };
    };
    if (posted) {
      body.data.messages.push({
        id: 1_000_000,
        action: null,
        author: "Owner",
        browserId: "",
        deliveredAt: null,
        images: 0,
        inboxItems: posted,
        organization: "owner",
        repository: "owner/shop",
        stoppedAt: null,
        text,
        time: "2026-10-15T12:00:00Z",
        workstream: 12,
      } as (typeof body.data.messages)[number]);
    }
    await route.fulfill({ response, json: body });
  });
  await page.route("**/api/chat/messages", (route) => {
    const parts = route.request().postData() ?? "";
    posted = [...parts.matchAll(/name="inboxItems"\r\n\r\n(.*?)\r\n--/g)].map(
      (part) => JSON.parse(part[1]) as NonNullable<typeof posted>[number],
    );
    return route.fulfill({ status: 204 });
  });

  await page.goto(shop);
  await main
    .getByRole("listitem")
    .filter({ hasText: stopped.title })
    .getByRole("button", { name: "Chat" })
    .click();
  await page.getByLabel("Message to the Lead").fill(text);
  await main.getByRole("button", { name: "Send", exact: true }).click();

  await expect
    .poll(() => posted)
    .toEqual([
      {
        reference: "#41",
        title: stopped.title,
        reason: stopped.reason,
        link: `${link}/issues/41`,
      },
    ]);
  const bubble = main.locator("[data-message]", { hasText: text });
  await expect(bubble).toHaveCount(1);
  await expect(main.locator("[data-queued]")).toHaveCount(0);
  const card = bubble.getByRole("link", { name: /Add plan model/ });
  await expect(card).toHaveAttribute("href", `${link}/issues/41`);
  await expect(card).toContainText("#41");
  await expect(card).toContainText(stopped.reason);
  await expect(card).toHaveAttribute("target", "_blank");
  await expect(main.locator("form").getByRole("button", { name: /^Remove #/ })).toHaveCount(0);
});
