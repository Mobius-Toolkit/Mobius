import { expect, test, type Page } from "@playwright/test";
import { png } from "./images.js";
import {
  openBriefTab,
  routeBrief,
  selectionBottom,
  selectionIsEmpty,
  selectText,
} from "./selection.js";
import { stoppedItems } from "./stops.js";

const link = "https://github.com/owner/shop";
const shop = "/workstreams/owner/shop/12";
const [stopped] = stoppedItems;

// The fake Lead of owner/shop#12 answers a message of the fixture with this text.
const leadReply = "The Implementer works on #41. #42 and #45 wait for your decision.";

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
    .getByRole("button", { name: "Chat about #41", exact: true })
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
    .getByRole("button", { name: "Chat about #46", exact: true })
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
  const row = main
    .getByRole("list", { name: "Pending actions" })
    .getByRole("listitem")
    .filter({ hasText: stopped.title });

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

  await row.getByRole("button", { name: "Chat about #41", exact: true }).click();
  for (const card of attached(page, stoppedCard)) {
    await expect(card).toBeVisible();
  }
  await expect(message).toHaveValue("This is the new plan page.");
  await expect(image).toHaveJSProperty("naturalWidth", 200);
  await expect
    .poll(() => page.evaluate(storedParts))
    .toEqual([["This is the new plan page.", 1, 1]]);
});

test("the bubble of a sent message shows the card of the Inbox item, a button for a task of the Workstream", async ({
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
        cancelledAt: null,
        deliveredAt: null,
        failedAt: null,
        images: 0,
        inboxItems: posted,
        selections: [],
        organization: "owner",
        question: null,
        repository: "owner/shop",
        stoppedAt: null,
        voice: false,
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
    .getByRole("list", { name: "Pending actions" })
    .getByRole("listitem")
    .filter({ hasText: stopped.title })
    .getByRole("button", { name: "Chat about #41", exact: true })
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
  const card = bubble.getByRole("button", { name: /Add plan model/ });
  await expect(card).toContainText("#41");
  await expect(card).toContainText(stopped.reason);
  await expect(main.locator("form").getByRole("button", { name: /^Remove #/ })).toHaveCount(0);
});

const phone = { width: 390, height: 844 };
const desktop = { width: 1280, height: 800 };

const leadText =
  "The plan has two steps.\n\nFirst, cut the red roses in the morning.\n\n- Wash the buckets\n- Sort the white roses";

const chatMessage = (
  id: number,
  author: string,
  text: string,
  place: object,
  extra: object = {},
) => ({
  id,
  action: null,
  author,
  browserId: "",
  cancelledAt: null,
  deliveredAt: null,
  failedAt: null,
  images: 0,
  inboxItems: [],
  selections: [],
  question: null,
  stoppedAt: null,
  voice: false,
  text,
  time: "2026-10-15T12:00:00Z",
  ...place,
  ...extra,
});

const shopPlace = { organization: "owner", repository: "owner/shop", workstream: 12 };
const coordinatorPlace = { organization: "owner", repository: "", workstream: 0 };

// Adds the messages to each page of the chat that the server gives.
const addMessages = (page: Page, messages: () => object[]) =>
  page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { messages: object[] } };
    body.data.messages.push(...messages());
    await route.fulfill({ response, json: body });
  });

// The messages that the server stores in a Lead chat, by default the chat of owner/shop#12. The session cookie is Secure, so the request
// of Playwright has no login, and the page makes the request.
async function storedMessages(
  page: Page,
  place = "organization=owner&repository=owner/shop&workstream=12",
) {
  const body = (await page.evaluate(
    `fetch("/api/chat?${place}").then((response) => response.json())`,
  )) as {
    data: { messages: { id: number; author: string; text: string; selections: object[] }[] };
  };
  return body.data.messages;
}

for (const [device, viewport] of [
  ["desktop", desktop],
  ["phone", phone],
] as const) {
  test.describe(`a selection in a chat message on a ${device}`, () => {
    test.use({ viewport });

    test("shows the popover below the selection, and Add to chat puts a card in the composer", async ({
      page,
    }) => {
      const main = page.getByRole("main");
      const composer = main.locator("form");
      const popover = page.getByRole("button", { name: "Add to chat" });
      await addMessages(page, () => [chatMessage(1_000_001, "Lead", leadText, shopPlace)]);

      await page.goto(shop);
      const bubble = main.locator("[data-message='1000001']");
      await expect(bubble).toBeVisible();
      await expect(page.getByLabel("Message to the Lead")).toBeEditable();
      await expect(popover).toHaveCount(0);

      await selectText(bubble, "in the morning");
      await expect(popover).toBeVisible();
      await expect(popover).toHaveAttribute("title", "Add to chat");
      const bottom = await selectionBottom(page);
      const box = await popover.boundingBox();
      expect(box?.y).toBeGreaterThanOrEqual(bottom);
      expect(box?.x).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(viewport.width);

      await popover.click();
      const card = composer.locator("[data-selection-card]");
      await expect(card).toHaveCount(1);
      await expect(card).toContainText("Lead message · 12:00 PM");
      await expect(card).toContainText("in the morning");
      await expect(popover).toHaveCount(0);
      expect(await selectionIsEmpty(page)).toBe(true);

      await page.reload();
      await expect(composer.locator("[data-selection-card]")).toHaveCount(1);
      await composer.getByRole("button", { name: "Remove the selection" }).click();
      await expect(composer.locator("[data-selection-card]")).toHaveCount(0);
      await expect.poll(() => page.evaluate(storedDrafts)).toBe(0);
    });

    test("sends two selections with the message, and the sent message shows both cards", async ({
      page,
    }) => {
      const main = page.getByRole("main");
      const composer = main.locator("form");
      const popover = page.getByRole("button", { name: "Add to chat" });
      const question = `Why this one? (${device})`;

      await page.goto(shop);
      const lead = (await storedMessages(page)).find(
        (stored) => stored.author === "Lead" && stored.text === leadReply,
      );
      if (!lead) {
        throw new Error("The server has no Lead message to quote.");
      }
      const bubble = main.locator(`[data-message='${lead.id}']`);
      await expect(bubble).toBeVisible();
      await expect(page.getByLabel("Message to the Lead")).toBeEditable();
      await bubble.scrollIntoViewIfNeeded();
      await selectText(bubble, "works on");
      await popover.click();
      await expect(composer.locator("[data-selection-card]")).toHaveCount(1);
      await bubble.scrollIntoViewIfNeeded();
      await selectText(bubble, "wait for your decision");
      await popover.click();
      await expect(composer.locator("[data-selection-card]")).toHaveCount(2);

      await page.getByLabel("Message to the Lead").fill(question);
      const stored = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" && response.url().endsWith("/api/chat/messages"),
      );
      await main.getByRole("button", { name: "Send", exact: true }).click();
      expect((await stored).status()).toBe(204);

      const quoted = (text: string) => ({
        source: "message",
        text,
        context: lead.text,
        reference: "",
        link: "",
        part: "",
        messageId: lead.id,
        messageAuthor: "Lead",
      });
      await expect
        .poll(async () => (await storedMessages(page)).find((other) => other.text === question))
        .toMatchObject({
          author: "Owner",
          selections: [quoted("works on"), quoted("wait for your decision")],
        });
      const sentId = (await storedMessages(page)).find((other) => other.text === question)?.id;
      const sent = main.locator(`[data-message='${sentId}']`);
      await expect(sent.locator("[data-selection-card]")).toHaveCount(2);
      await expect(sent.locator("[data-selection-card]").first()).toContainText("Lead message");
      await expect(sent.locator("[data-selection-card]").first()).toContainText("works on");
      await expect(sent.locator("[data-selection-card]").last()).toContainText(
        "wait for your decision",
      );
      await expect(sent.getByRole("button", { name: "Remove the selection" })).toHaveCount(0);
      await expect(composer.locator("[data-selection-card]")).toHaveCount(0);
    });

    test("adds a selection of the open body of an event to the draft", async ({ page }) => {
      const main = page.getByRole("main");
      const composer = main.locator("form");
      await addMessages(page, () => [
        chatMessage(
          1_000_004,
          "Event",
          "Mobius closed #7\nThe Implementer finished the **plan** page.",
          shopPlace,
        ),
      ]);

      await page.goto(shop);
      const event = main.locator("[data-message='1000004']");
      await expect(event).toBeVisible();
      await expect(page.getByLabel("Message to the Lead")).toBeEditable();
      await event.getByRole("button", { name: /Mobius closed #7/ }).click();
      await selectText(event, "finished the");
      await page.getByRole("button", { name: "Add to chat" }).click();

      const card = composer.locator("[data-selection-card]");
      await expect(card).toHaveCount(1);
      await expect(card).toContainText("Event message");
      await expect(card).toContainText("finished the");
    });

    test("adds a selection of a Coordinator message to the draft of the Coordinator chat", async ({
      page,
    }) => {
      const main = page.getByRole("main");
      const composer = main.locator("form");
      await addMessages(page, () => [
        chatMessage(1_000_003, "Coordinator", "Plan the roses first.", coordinatorPlace),
      ]);

      await page.goto("/chat");
      const bubble = main.locator("[data-message='1000003']");
      await expect(bubble).toBeVisible();
      await expect(page.getByLabel("Message to the Coordinator")).toBeEditable();
      await selectText(bubble, "the roses");
      await page.getByRole("button", { name: "Add to chat" }).click();

      await expect(composer.locator("[data-selection-card]")).toContainText(
        "Coordinator message · 12:00 PM",
      );
      await expect(composer.locator("[data-selection-card]")).toContainText("the roses");
      await page.goto(shop);
      await expect(page.getByLabel("Message to the Lead")).toBeEditable();
      await expect(main.locator("form").locator("[data-selection-card]")).toHaveCount(0);
    });
  });

  test.describe(`a selection in the Brief tab on a ${device}`, () => {
    test.use({ viewport });

    const brief =
      "Reward repeat customers.\n\n**Scope**\n- Points on every order\n- One **free** plan for staff\n\n**Limits**\nDo not change the price of a plan.";

    test("shows the popover below the selection, and Add to chat puts a card in the composer", async ({
      page,
    }) => {
      const main = page.getByRole("main");
      const composer = main.locator("form");
      const popover = page.getByRole("button", { name: "Add to chat" });
      await routeBrief(page, "owner/shop", 12, brief);

      await page.goto(shop);
      await expect(page.getByLabel("Message to the Lead")).toBeEditable();
      const tab = await openBriefTab(page, device);
      await expect(tab.getByText("Points on every order")).toBeVisible();
      await expect(popover).toHaveCount(0);

      await selectText(tab, "Points on every order");
      await expect(popover).toBeVisible();
      const bottom = await selectionBottom(page);
      const box = await popover.boundingBox();
      expect(box?.y).toBeGreaterThanOrEqual(bottom);
      expect(box?.x).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(viewport.width);

      await popover.click();
      const card = composer.locator("[data-selection-card]");
      await expect(card).toHaveCount(1);
      await expect(card).toContainText("Brief of #12 · Scope");
      await expect(card).toContainText("Points on every order");
      await expect(popover).toHaveCount(0);
      if (device === "phone") {
        await expect(page.getByRole("dialog", { name: "Brief" })).toHaveCount(0);
      }
      expect(await selectionIsEmpty(page)).toBe(true);
    });

    test("sends the selection with the message, and the sent message shows its card", async ({
      page,
    }) => {
      const main = page.getByRole("main");
      const composer = main.locator("form");
      const question = `Why this reward? (${device})`;
      await routeBrief(page, "owner/shop", 12, brief);

      await page.goto(shop);
      await expect(page.getByLabel("Message to the Lead")).toBeEditable();
      const tab = await openBriefTab(page, device);
      await selectText(tab, "price of a plan");
      await page.getByRole("button", { name: "Add to chat" }).click();
      await expect(composer.locator("[data-selection-card]")).toHaveCount(1);

      await page.getByLabel("Message to the Lead").fill(question);
      const stored = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" && response.url().endsWith("/api/chat/messages"),
      );
      await main.getByRole("button", { name: "Send", exact: true }).click();
      expect((await stored).status()).toBe(204);

      await expect
        .poll(async () => (await storedMessages(page)).find((other) => other.text === question))
        .toMatchObject({
          author: "Owner",
          selections: [
            {
              source: "brief",
              text: "price of a plan",
              context: expect.stringContaining("Do not change the price of a plan."),
              reference: "#12",
              link: "https://github.com/owner/shop/issues/12",
              part: "Limits",
              messageId: 0,
              messageAuthor: "",
            },
          ],
        });
      const sentId = (await storedMessages(page)).find((other) => other.text === question)?.id;
      const sent = main.locator(`[data-message='${sentId}']`);
      await expect(sent.locator("[data-selection-card]")).toHaveCount(1);
      await expect(sent.locator("[data-selection-card]")).toContainText("Brief of #12 · Limits");
      await expect(composer.locator("[data-selection-card]")).toHaveCount(0);
    });
  });
}

const gardenPlace = "organization=plants&repository=plants/garden&workstream=19";

const taskBody =
  "### Instructions\n- Wear protective gloves.\n- Spread 5 cm of bark evenly over each bed.";

async function openTaskView(page: Page, device: string) {
  await page.route("**/api/workstreams/plants/garden/19/tasks/plants/garden/71", (route) =>
    route.fulfill({
      json: { data: { body: taskBody, pullRequest: null, pullRequestUrl: null } },
    }),
  );
  await page.goto("/workstreams/plants/garden/19");
  if (device === "phone") {
    await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
  }
  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  const view = page.getByRole("tabpanel", { name: "Tasks" }).filter({ visible: true });
  await view.getByRole("button", { name: /#71 Spread the bark/ }).click();
  await expect(view.getByText("Wear protective gloves.")).toBeVisible();
  await expect(page.locator("#side-task-input")).toBeEditable();
  return view;
}

for (const [device, viewport] of [
  ["desktop", desktop],
  ["phone", phone],
] as const) {
  test.describe(`a selection in the issue body of the task view on a ${device}`, () => {
    test.use({ viewport });

    test("shows the popover below the selection, and Add to chat puts a card in the composer of the view", async ({
      page,
    }) => {
      const popover = page.getByRole("button", { name: "Add to chat" });
      const view = await openTaskView(page, device);
      const composer = page.locator("form:has(#side-task-input)");
      await expect(popover).toHaveCount(0);

      await selectText(view.getByRole("heading", { name: "#71 Spread the bark" }), "Spread");
      await expect(popover).toHaveCount(0);

      await selectText(view, "Spread 5 cm of bark");
      await expect(popover).toBeVisible();
      const bottom = await selectionBottom(page);
      const box = await popover.boundingBox();
      expect(box?.y).toBeGreaterThanOrEqual(bottom);
      expect(box?.x).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(viewport.width);

      await popover.click();
      const card = composer.locator("[data-selection-card]");
      await expect(card).toHaveCount(1);
      await expect(card).toContainText("#71 · issue body");
      await expect(card).toContainText("Spread 5 cm of bark");
      await expect(popover).toHaveCount(0);
      expect(await selectionIsEmpty(page)).toBe(true);
    });

    test("sends the selection from the composer of the view, and the sent message shows its card", async ({
      page,
    }) => {
      const question = `Is this bark enough? (${device}, selection)`;
      const view = await openTaskView(page, device);
      const composer = page.locator("form:has(#side-task-input)");

      await selectText(view, "Spread 5 cm of bark evenly over each bed.");
      await page.getByRole("button", { name: "Add to chat" }).click();
      await expect(composer.locator("[data-selection-card]")).toHaveCount(1);
      await expect(
        page.getByRole("main").locator("form:has(#chat-input) [data-selection-card]"),
      ).toHaveCount(1);

      await page.locator("#side-task-input").fill(question);
      const stored = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" && response.url().endsWith("/api/chat/messages"),
      );
      await composer.getByRole("button", { name: "Send", exact: true }).click();
      expect((await stored).status()).toBe(204);

      await expect
        .poll(async () =>
          (await storedMessages(page, gardenPlace)).find((other) => other.text === question),
        )
        .toMatchObject({
          author: "Owner",
          selections: [
            {
              source: "task",
              text: "Spread 5 cm of bark evenly over each bed.",
              context: expect.stringContaining("Spread 5 cm of bark evenly over each bed."),
              reference: "#71",
              link: "https://github.com/plants/garden/issues/71",
              part: "issue body",
              messageId: 0,
              messageAuthor: "",
            },
          ],
        });
      const sentId = (await storedMessages(page, gardenPlace)).find(
        (other) => other.text === question,
      )?.id;
      const sent = page.getByRole("main").locator(`[data-message='${sentId}']`);
      await expect(sent.locator("[data-selection-card]")).toHaveCount(1);
      await expect(sent.locator("[data-selection-card]")).toContainText("#71 · issue body");
      await expect(composer.locator("[data-selection-card]")).toHaveCount(0);
    });
  });
}
