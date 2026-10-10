import { expect, test, type Page, type Route } from "@playwright/test";
import { pasteImage, png } from "./images.js";

const phone = { width: 390, height: 844 };
const shop = "/workstreams/owner/shop/12";
const picture = /^Picture \d+ of /;

async function logIn(page: Page) {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
}

test.beforeEach(({ page }) => logIn(page));

// The session cookie is Secure. The browser sends it to 127.0.0.1 over HTTP, but the API requests of Playwright do
// not.
async function get<T>(page: Page, path: string) {
  const body = (await page.evaluate(
    `fetch(${JSON.stringify(path)}).then((response) => response.json())`,
  )) as { data: T };
  return body.data;
}

// The texts of the Owner messages that the server has in the Lead chat of owner/shop#12.
async function sent(page: Page) {
  const chat = await get<{ messages: { author: string; text: string }[] }>(
    page,
    "/api/chat?organization=owner&repository=owner/shop&workstream=12",
  );
  return chat.messages
    .filter((message) => message.author === "Owner")
    .map((message) => message.text);
}

// The image counts of the Owner messages that the server has in the Lead chat of owner/shop#12, with their texts.
async function sentImages(page: Page) {
  const chat = await get<{ messages: { author: string; text: string; images: number }[] }>(
    page,
    "/api/chat?organization=owner&repository=owner/shop&workstream=12",
  );
  return chat.messages
    .filter((message) => message.author === "Owner")
    .map((message) => `${message.text}:${message.images}`);
}

async function unread(page: Page, workstream: number) {
  const chats = await get<{ repository: string; workstream: number; count: number }[]>(
    page,
    "/api/unread",
  );
  return (
    chats.find((chat) => chat.repository === "plants/garden" && chat.workstream === workstream)
      ?.count ?? 0
  );
}

// The tests have no DOM types, so the check is a script. It tells if the chat scrolls and shows the message with text
// at its top, or shows its end.
const shows = (text: string, place: "top" | "end") => `(() => {
  const message = [...document.querySelectorAll('[data-message]')].find((element) => element.textContent.includes(${JSON.stringify(text)}))
  const list = message?.parentElement
  if (!list || list.scrollHeight <= list.clientHeight) {
    return false
  }
  return ${
    place === "top"
      ? "Math.abs(message.getBoundingClientRect().top - list.getBoundingClientRect().top) < 5"
      : "list.scrollHeight - list.scrollTop - list.clientHeight < 5"
  }
})()`;

async function say(page: Page, text: string) {
  await page.evaluate(`(() => {
    const form = new FormData()
    form.append('organization', 'owner')
    form.append('repository', 'owner/shop')
    form.append('workstream', '13')
    form.append('id', crypto.randomUUID())
    form.append('text', ${JSON.stringify(text)})
    return fetch('/api/chat/messages', { method: 'POST', body: form })
  })()`);
}

const runningLeads = async (page: Page) =>
  (
    await get<{ role: string; endedAt: string | null }[]>(
      page,
      "/api/workstreams/owner/shop/13/agents",
    )
  ).filter((agent) => agent.role === "lead_chat" && agent.endedAt === null).length;

const leadRow = (page: Page) =>
  page.getByRole("main").getByRole("button", { name: /Lead chat session/ });

// The fake agent is busy for some seconds before its reply, so the new Lead session of owner/shop#13 runs while the
// log opens. The Lead session of a test ends before the next test starts. These tests come first, because the later
// tests leave Lead sessions that run, and no slot is free for a new Lead.
test.describe("the agent log of a running Lead", () => {
  const seasonal = "/workstreams/owner/shop/13";
  test.afterEach(async ({ page }) => {
    await expect.poll(() => runningLeads(page), { timeout: 15_000 }).toBe(0);
  });

  test("the side panel shows a new entry with no reload", async ({ page }) => {
    await page.goto(seasonal);
    const tree = page.getByRole("complementary");
    await say(page, "Which tulips sell best?");
    await tree
      .getByRole("button", { name: /Lead chat session/ })
      .filter({ hasNotText: "stopped" })
      .click();
    await page.evaluate("window.sameDocument = true");
    await expect(tree.getByText("Yellow tulips sell best.")).toBeVisible({ timeout: 15_000 });
    expect(await page.evaluate("window.sameDocument")).toBe(true);
  });

  test("the agents page shows a new entry with no reload", async ({ page }) => {
    await page.goto("/agents");
    await say(page, "Which daisies sell best?");
    await leadRow(page).filter({ hasText: "Workstream #13" }).click();
    await page.evaluate("window.sameDocument = true");
    await expect(page.getByRole("main").getByText("White daisies sell best.")).toBeVisible({
      timeout: 15_000,
    });
    expect(await page.evaluate("window.sameDocument")).toBe(true);
  });

  test("the log shows the entries that arrived while the same live connection was down", async ({
    page,
  }) => {
    await page.addInitScript(`
      window.lostTranscripts = false;
      window.sources = [];
      const add = EventSource.prototype.addEventListener;
      EventSource.prototype.addEventListener = function (type, listener, options) {
        if (this.url.endsWith('/api/events') && !window.sources.includes(this)) {
          window.sources.push(this);
        }
        if (type === 'transcript') {
          return add.call(this, type, (event) => {
            if (!window.lostTranscripts) {
              listener(event);
            }
          }, options);
        }
        return add.call(this, type, listener, options);
      };
    `);
    await page.goto("/agents");
    const main = page.getByRole("main");
    await say(page, "Which poppies sell best?");
    await leadRow(page).filter({ hasText: "Workstream #13" }).click();
    await page.evaluate("window.lostTranscripts = true");

    await expect.poll(() => runningLeads(page), { timeout: 15_000 }).toBe(0);
    await expect(main.getByText("Orange poppies sell best.")).toBeHidden();

    await page.evaluate(`window.lostTranscripts = false;
      window.sources.forEach((source) => source.dispatchEvent(new Event('open')))`);
    await expect(main.getByText("Orange poppies sell best.")).toBeVisible();
  });

  test("the log shows the entries that arrived while the live connection was down", async ({
    page,
  }) => {
    await page.goto("/agents");
    const main = page.getByRole("main");
    await say(page, "Which lilies sell best?");
    await leadRow(page).filter({ hasText: "Workstream #13" }).click();
    await page.evaluate("window.sameDocument = true");
    let refused = 0;
    await page.route("/api/events", (route) => {
      refused++;
      return route.abort();
    });
    await page.evaluate("window.dispatchEvent(new Event('online'))");
    await expect.poll(() => refused).toBeGreaterThan(0);

    await expect.poll(() => runningLeads(page), { timeout: 15_000 }).toBe(0);
    await expect(main.getByText("Pink lilies sell best.")).toBeHidden();

    await page.unroute("/api/events");
    await page.evaluate("window.dispatchEvent(new Event('online'))");
    await expect(main.getByText("Pink lilies sell best.")).toBeVisible();
    expect(await page.evaluate("window.sameDocument")).toBe(true);
  });
});

test("Enter sends, and Shift+Enter adds a line on a desktop", async ({ page }) => {
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await input.pressSequentially("one");
  await input.press("Shift+Enter");
  await input.pressSequentially("two");
  await input.press("Enter");
  await expect.poll(() => sent(page)).toContain("one\ntwo");
  await expect(input).toHaveValue("");
});

const fontSize = (selector: string) =>
  `parseFloat(getComputedStyle(document.querySelector('${selector}')).fontSize)`;

test("the chat text is 14px, and the chat input is at least 16px on a phone", async ({ page }) => {
  await page.goto(shop);
  await expect(page.locator("[data-message]").first()).toBeVisible();
  expect(await page.evaluate(fontSize("[data-message] p"))).toBe(14);
  expect(await page.evaluate(fontSize("textarea"))).toBe(14);
  await page.setViewportSize(phone);
  expect(await page.evaluate(fontSize("[data-message] p"))).toBe(14);
  expect(await page.evaluate(fontSize("textarea"))).toBeGreaterThanOrEqual(16);
});

test.describe("images", () => {
  test("a pasted image and an uploaded image show, and the Owner removes one", async ({ page }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    await pasteImage(page, await png(page, 40, 30, "red"));
    await expect(main.getByRole("img", { name: "Image 1" })).toBeVisible();
    await main.locator("input[type=file]").setInputFiles({
      name: "photo.png",
      mimeType: "image/png",
      buffer: await png(page, 40, 30, "blue"),
    });
    await expect(main.getByRole("img", { name: "Image 2" })).toBeVisible();
    await main.getByRole("button", { name: "Remove image 1" }).click();
    await expect(main.getByRole("img", { name: "Image 1" })).toBeVisible();
    await expect(main.getByRole("img", { name: "Image 2" })).toBeHidden();
  });

  test("a message with an image and a message with only an image reach the server", async ({
    page,
  }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    const input = page.getByLabel("Message to the Lead");
    await input.fill("with image");
    await pasteImage(page, await png(page, 40, 30, "red"));
    await expect(main.getByRole("img", { name: "Image 1" })).toBeVisible();
    await main.getByRole("button", { name: "Send" }).click();
    await expect.poll(() => sentImages(page)).toContain("with image:1");
    await expect(main.getByRole("img", { name: "Image 1" })).toBeHidden();

    const image = await png(page, 40, 30, "green");
    await main.locator("input[type=file]").setInputFiles([
      { name: "one.png", mimeType: "image/png", buffer: image },
      { name: "two.png", mimeType: "image/png", buffer: image },
    ]);
    await expect(main.getByRole("img", { name: "Image 2" })).toBeVisible();
    await main.getByRole("button", { name: "Send" }).click();
    await expect.poll(() => sentImages(page)).toContain(":2");
    await expect(main.getByRole("img", { name: "Image 1" })).toBeHidden();
  });

  test("the history shows the images of a message after a reload", async ({ page }) => {
    await page.goto("/workstreams/plants/garden/25");
    const main = page.getByRole("main");
    const withText = main.locator("[data-message]", { hasText: "This is the new plan page." });
    const onlyImage = main
      .locator("[data-message]")
      .filter({ has: page.getByRole("img", { name: picture }) })
      .last();
    for (const [message, count] of [
      [withText, 2],
      [onlyImage, 1],
    ] as const) {
      const pictures = message.getByRole("img", { name: picture });
      await expect(pictures).toHaveCount(count);
      for (let position = 0; position < count; position++) {
        await expect(pictures.nth(position)).toHaveJSProperty("naturalWidth", 200);
      }
    }
    await expect(onlyImage.locator(":scope > *")).toHaveCount(2);
    await page.reload();
    await expect(withText.getByRole("img", { name: picture })).toHaveCount(2);
    await expect(withText.getByRole("img", { name: picture }).first()).toHaveJSProperty(
      "naturalWidth",
      200,
    );
  });

  test("a sent message shows its images in the history with no reload", async ({ page }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    await main.getByLabel("Message to the Lead").fill("Live images");
    await pasteImage(page, await png(page, 40, 30, "red"));
    await pasteImage(page, await png(page, 40, 30, "green"));
    await expect(main.getByRole("img", { name: "Image 2" })).toBeVisible();
    await main.getByRole("button", { name: "Send" }).click();
    const message = main.locator("[data-message]", { hasText: "Live images" });
    const pictures = message.getByRole("img", { name: picture });
    await expect(pictures).toHaveCount(2);
    await expect(pictures.first()).toHaveJSProperty("naturalWidth", 40);
    await expect(pictures.last()).toHaveJSProperty("naturalWidth", 40);
  });

  test("the images of a refused message stay in the queued message, not in the input", async ({
    page,
  }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    await page.route("/api/chat/messages", (route) =>
      route.fulfill({ status: 400, json: { error: "The image is too large." } }),
    );
    await pasteImage(page, await png(page, 40, 30, "red"));
    await main.getByRole("button", { name: "Send" }).click();
    const queued = main.locator("[data-queued]");
    await expect(queued.getByText("The image is too large.")).toBeVisible();
    await expect(queued.getByRole("img", { name: "Picture 1 of an unsent message" })).toBeVisible();
    await expect(main.getByRole("img", { name: "Image 1" })).toBeHidden();
  });

  test("a paste with text and an image keeps the default paste", async ({ page }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    const notCanceled = await pasteImage(page, await png(page, 40, 30, "red"), "cells");
    expect(notCanceled).toBe(true);
    await expect(main.getByRole("img", { name: "Image 1" })).toBeHidden();
  });

  test("a second add while the first scales an image still gets the error at 4 images", async ({
    page,
  }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    const image = await png(page, 40, 30, "red");
    const files = [1, 2, 3].map((n) => ({
      name: `${n}.png`,
      mimeType: "image/png",
      buffer: image,
    }));
    await main.locator("input[type=file]").setInputFiles(files);
    await expect(main.getByRole("img", { name: "Image 3" })).toBeVisible();
    await main.locator("input[type=file]").setInputFiles({
      name: "large.png",
      mimeType: "image/png",
      buffer: await png(page, 3136, 1000, "red"),
    });
    await pasteImage(page, image);
    await expect(main.getByRole("img", { name: "Image 4" })).toBeVisible();
    await expect(main.getByRole("img", { name: "Image 5" })).toBeHidden();
    await expect(main.getByText("A message has at most 4 images.")).toBeVisible();
  });

  test("a message has at most 4 images", async ({ page }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    const image = await png(page, 40, 30, "red");
    const files = [1, 2, 3, 4, 5].map((n) => ({
      name: `${n}.png`,
      mimeType: "image/png",
      buffer: image,
    }));
    await main.locator("input[type=file]").setInputFiles(files);
    await expect(main.getByText("A message has at most 4 images.")).toBeVisible();
    await expect(main.getByRole("img", { name: "Image 4" })).toBeVisible();
    await expect(main.getByRole("img", { name: "Image 5" })).toBeHidden();
    await pasteImage(page, image);
    await expect(main.getByText("A message has at most 4 images.")).toBeVisible();
    await expect(main.getByRole("img", { name: "Image 5" })).toBeHidden();
  });

  test("an image of another type gets an error, and a large image is scaled down", async ({
    page,
  }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    await main.locator("input[type=file]").setInputFiles({
      name: "notes.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("notes"),
    });
    await expect(main.getByText("notes.txt is not a PNG, JPEG, GIF or WebP image.")).toBeVisible();
    await expect(main.getByRole("img", { name: "Image 1" })).toBeHidden();

    await main.locator("input[type=file]").setInputFiles({
      name: "large.png",
      mimeType: "image/png",
      buffer: await png(page, 3136, 1000, "red"),
    });
    await expect(main.getByRole("img", { name: "Image 1" })).toHaveJSProperty("naturalWidth", 1568);
  });
});

test.describe("on a touch screen", () => {
  test.use({ viewport: phone, isMobile: true, hasTouch: true });

  test("Enter adds a line, and only Send sends", async ({ page }) => {
    await page.goto(shop);
    const input = page.getByLabel("Message to the Lead");
    await input.pressSequentially("three");
    await input.press("Enter");
    await input.pressSequentially("four");
    await expect(input).toHaveValue("three\nfour");
    await page.getByRole("button", { name: "Send" }).tap();
    await expect.poll(() => sent(page)).toContain("three\nfour");
    expect(await sent(page)).not.toContain("three");
  });

  test("two taps on Send in one turn send one message, and the buttons are easy to tap", async ({
    page,
  }) => {
    await page.goto(shop);
    const input = page.getByLabel("Message to the Lead");
    const send = page.getByRole("button", { name: "Send" });
    await input.pressSequentially("double");
    await page.evaluate(
      "for (let n = 0; n < 2; n++) document.querySelector('main form button[type=submit]').click()",
    );
    await expect.poll(() => sent(page)).toContain("double");
    await input.pressSequentially("after");
    await send.tap();
    await expect.poll(() => sent(page)).toContain("after");
    expect((await sent(page)).filter((text) => text === "double")).toHaveLength(1);
    expect(
      await page.evaluate(`(() => {
        const field = document.querySelector('main form textarea').getBoundingClientRect()
        const buttons = [...document.querySelectorAll('main form button')]
        return buttons.length > 0 && buttons.every((button) => {
          const box = button.getBoundingClientRect()
          return box.height >= 40 && box.top >= field.bottom
        })
      })()`),
    ).toBe(true);

    // The text that the Owner writes while a message goes to the server stays in the input.
    const held: Route[] = [];
    await page.route("/api/chat/messages", (route) => {
      held.push(route);
    });
    await input.pressSequentially("hello");
    await send.tap();
    await expect(input).toHaveValue("");
    await input.pressSequentially("more");
    await expect.poll(() => held.length).toBe(1);
    await held[0].continue();
    await expect.poll(() => sent(page)).toContain("hello");
    await expect(input).toHaveValue("more");
    await send.tap();
    await expect.poll(() => held.length).toBe(2);
    await held[1].continue();
    await expect.poll(() => sent(page)).toContain("more");

    // When the server refuses the message, the input keeps the new text.
    await input.pressSequentially("lost");
    await send.tap();
    await expect(input).toHaveValue("");
    await input.pressSequentially("!");
    await expect.poll(() => held.length).toBe(3);
    await held[2].fulfill({ status: 500, json: { error: "The send failed." } });
    await expect(page.locator("[data-queued]").getByText("The send failed.")).toBeVisible();
    await expect(input).toHaveValue("!");
    expect(await sent(page)).not.toContain("lost");
  });

  test("one tap on Send sends while the input has the focus", async ({ page }) => {
    await page.goto(shop);
    const input = page.getByLabel("Message to the Lead");
    await input.tap();
    await page.keyboard.type("one tap");
    await expect(input).toBeFocused();
    await page.getByRole("button", { name: "Send" }).tap();
    await expect.poll(() => sent(page)).toContain("one tap");
    expect((await sent(page)).filter((text) => text === "one tap")).toHaveLength(1);
    await expect(input).toBeFocused();
  });
});

const queued = (page: Page) => page.getByRole("main").locator("[data-queued]");

test.describe("the send queue", () => {
  const messages = "/api/chat/messages";

  // The time of the next try is the fixed time of the page plus the wait, so it is the same in each run.
  test.beforeEach(({ page }) => page.clock.setFixedTime("2026-10-15T12:00:00Z"));

  test("a message shows at once, and the input is free while it waits", async ({ page }) => {
    await page.goto(shop);
    const input = page.getByLabel("Message to the Lead");
    const held: Route[] = [];
    await page.route(messages, (route) => {
      held.push(route);
    });
    await input.fill("Queue one");
    await input.press("Enter");
    await expect(input).toHaveValue("");
    await expect(queued(page)).toHaveCount(1);
    await expect(queued(page).getByText("Queue one")).toBeVisible();
    await expect(queued(page).getByRole("img", { name: "Not sent yet" })).toBeVisible();
    await expect.poll(() => held.length).toBe(1);

    await input.fill("Queue two");
    await input.press("Enter");
    await expect(queued(page)).toHaveCount(2);
    expect(held).toHaveLength(1);
    await held[0].continue();
    await expect.poll(() => held.length).toBe(2);
    await held[1].continue();
    await expect.poll(() => sent(page)).toContain("Queue two");
    const texts = await sent(page);
    expect(texts.indexOf("Queue one")).toBeLessThan(texts.indexOf("Queue two"));
    await expect(page.locator("[data-message]", { hasText: "Queue one" })).toHaveCount(1);
    await expect(page.locator("[data-message]", { hasText: "Queue two" })).toHaveCount(1);
    await expect(queued(page)).toHaveCount(0);
  });

  test("a failed send shows the error and the next try, and Retry now sends it", async ({
    page,
  }) => {
    await page.goto(shop);
    const input = page.getByLabel("Message to the Lead");
    let fail = true;
    await page.route(messages, (route) =>
      fail ? route.fulfill({ status: 500, json: { error: "The send failed." } }) : route.continue(),
    );
    await input.fill("Retry me");
    await input.press("Enter");
    const message = queued(page);
    await expect(message.getByRole("img", { name: "Send failed" })).toBeVisible();
    await expect(message.getByText("The send failed.")).toBeVisible();
    await expect(message.getByText(/Next try at 12:00:02\sPM/)).toBeVisible();
    await expect(input).toHaveValue("");
    fail = false;
    await message.getByRole("button", { name: "Retry now" }).click();
    await expect.poll(() => sent(page)).toContain("Retry me");
    await expect(queued(page)).toHaveCount(0);
    await expect(page.locator("[data-message]", { hasText: "Retry me" })).toHaveCount(1);
  });

  test("the waits grow to 5 and 10 seconds", async ({ page }) => {
    await page.goto(shop);
    await page.route(messages, (route) => route.abort());
    await page.getByLabel("Message to the Lead").fill("Never stored");
    await page.getByLabel("Message to the Lead").press("Enter");
    await expect(queued(page).getByText(/Next try at 12:00:02\sPM/)).toBeVisible();
    await expect(queued(page).getByText(/Next try at 12:00:05\sPM/)).toBeVisible({
      timeout: 10_000,
    });
    await expect(queued(page).getByText(/Next try at 12:00:10\sPM/)).toBeVisible({
      timeout: 15_000,
    });
  });

  test("the queue stays after a reload and keeps sending", async ({ page }) => {
    await page.goto(shop);
    await page.route(messages, (route) => route.abort());
    await page.getByLabel("Message to the Lead").fill("After reload");
    await page.getByLabel("Message to the Lead").press("Enter");
    await expect(queued(page).getByText("Next try at")).toBeVisible();
    await page.unroute(messages);
    await page.reload();
    await expect.poll(() => sent(page)).toContain("After reload");
    await expect(queued(page)).toHaveCount(0);
    await expect(page.locator("[data-message]", { hasText: "After reload" })).toHaveCount(1);
  });

  test("Delete removes a message that the server did not store", async ({ page }) => {
    await page.goto(shop);
    await page.route(messages, (route) =>
      route.fulfill({ status: 500, json: { error: "The send failed." } }),
    );
    const input = page.getByLabel("Message to the Lead");
    await input.fill("Delete me");
    await input.press("Enter");
    await input.fill("Wait behind");
    await input.press("Enter");
    await expect(queued(page)).toHaveCount(2);
    await expect(queued(page).first().getByText("Next try at")).toBeVisible();
    await queued(page).first().getByRole("button", { name: "Delete the message" }).click();
    await expect(queued(page)).toHaveCount(1);
    await expect(queued(page).getByText("Delete me")).toBeHidden();
    await queued(page).getByRole("button", { name: "Delete the message" }).click();
    await expect(queued(page)).toHaveCount(0);
    await page.reload();
    await expect(page.locator("[data-message]").first()).toBeVisible();
    await expect(queued(page)).toHaveCount(0);
    expect(await sent(page)).not.toContain("Delete me");
  });

  test("the input keeps its text and images while a message fails", async ({ page }) => {
    await page.goto(shop);
    const input = page.getByLabel("Message to the Lead");
    await page.route(messages, (route) =>
      route.fulfill({ status: 500, json: { error: "The send failed." } }),
    );
    await input.fill("Fails");
    await input.press("Enter");
    await input.fill("Draft text");
    await pasteImage(page, await png(page, 40, 30, "red"));
    await expect(queued(page).getByText("The send failed.")).toBeVisible();
    await expect(input).toHaveValue("Draft text");
    await expect(page.getByRole("main").getByRole("img", { name: "Image 1" })).toBeVisible();
  });
});

test("a new message scrolls the chat to its end", async ({ page }) => {
  for (const [device, size] of [
    ["desktop", undefined],
    ["phone", phone],
  ] as const) {
    if (size) {
      await page.setViewportSize(size);
    }
    await page.goto(shop);
    // The chat shows its messages after the live connection opens.
    await expect(page.locator("[data-message]").first()).toBeVisible();
    await page.evaluate(`(async () => {
      for (let n = 0; n < 20; n++) {
        const form = new FormData()
        form.append('organization', 'owner')
        form.append('repository', 'owner/shop')
        form.append('workstream', '12')
        form.append('id', crypto.randomUUID())
        form.append('text', 'spam ${device} ' + n + ' ' + 'word '.repeat(40))
        await fetch('/api/chat/messages', { method: 'POST', body: form })
      }
    })()`);
    await expect.poll(() => page.evaluate(shows(`spam ${device} 19 `, "end"))).toBe(true);
  }
});

test("an event shows folded and muted in the chat, and wraps on a phone", async ({ page }) => {
  const main = page.getByRole("main");
  // The tests have no DOM types, so the checks are scripts.
  const muted = `(() => {
    const event = [...document.querySelectorAll('[data-message]')].find((element) => element.textContent.includes('A comment arrived'))
    const probe = document.createElement('span')
    probe.style.color = 'var(--muted-foreground)'
    document.body.append(probe)
    const color = getComputedStyle(probe).color
    probe.remove()
    return getComputedStyle(event).color === color
  })()`;
  const narrow = `(() => {
    const list = document.querySelector('[data-message]').parentElement
    return list.scrollWidth <= list.clientWidth
  })()`;
  for (const size of [undefined, phone]) {
    if (size) {
      await page.setViewportSize(size);
    }
    await page.goto("/workstreams/plants/garden/25");
    const folded = main.locator("[data-message]", {
      hasText: "A comment arrived",
    });
    const single = main.locator("[data-message]", {
      hasText: "A label changed.",
    });
    const summary = folded.getByRole("button");
    const rest = main.getByText("The rest of the comment.");
    await expect(folded).toBeVisible();
    expect(await page.evaluate(muted)).toBe(true);
    expect(await page.evaluate(narrow)).toBe(true);
    await expect(summary).toHaveAttribute("aria-expanded", "false");
    await expect(summary).toHaveText(/^A comment arrived: word/);
    await expect(rest).toBeHidden();
    await expect(single).toBeVisible();
    await expect(single.getByRole("button")).toHaveCount(0);

    await summary.click();
    await expect(summary).toHaveAttribute("aria-expanded", "true");
    await expect(rest).toBeVisible();
    expect(await page.evaluate(narrow)).toBe(true);
  }
});

test("an empty chat opens with the Brief, and a click on its head folds it", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/workstreams/plants/garden/18");
  const brief = main.getByText("Cut the old canes in March.");
  await expect(brief).toBeVisible();
  await expect(brief.locator("strong")).toHaveText("old");
  const head = main.getByRole("button", { name: "Prune the roses" });
  await head.click();
  await expect(brief).toBeHidden();
  await expect(head).toBeVisible();
  // The chat of the next Workstream opens with its own Brief.
  await page.getByRole("link", { name: "Mulch the beds" }).click();
  const next = main.getByText("Put bark on the beds.");
  await expect(next).toBeVisible();
  await expect(next.locator("strong")).toHaveText("bark");
});

test("a switch to a chat shows its first unread message", async ({ page }) => {
  for (const [size, first, second] of [
    [undefined, [14, "Water the roses"], [15, "Feed the roses"]],
    [phone, [16, "Cut the roses"], [17, "Sell the roses"]],
  ] as const) {
    if (size) {
      await page.setViewportSize(size);
    }
    // A phone has the links to the chats on the Workstreams tab.
    const switchTo = async (title: string) => {
      if (size) {
        await page
          .getByRole("link", { name: "Workstreams", exact: true })
          .filter({ visible: true })
          .click();
      }
      await page.getByRole("link", { name: title }).filter({ visible: true }).click();
    };
    const expectShown = (text: string, place: "top" | "end") =>
      expect.poll(() => page.evaluate(shows(text, place))).toBe(true);

    await page.goto(`/workstreams/plants/garden/${first[0]}`);
    await expectShown(`Note 6 of #${first[0]}.`, "top");
    // The page marks the messages as seen, and the position stays.
    await expect.poll(() => unread(page, first[0])).toBe(0);
    await expectShown(`Note 6 of #${first[0]}.`, "top");
    await switchTo(second[1]);
    await expectShown(`Note 6 of #${second[0]}.`, "top");
    await expect.poll(() => unread(page, second[0])).toBe(0);
    await expectShown(`Note 6 of #${second[0]}.`, "top");
    await switchTo(first[1]);
    await expectShown(`Note 12 of #${first[0]}.`, "end");
    await switchTo(second[1]);
    await expectShown(`Note 12 of #${second[0]}.`, "end");
  }
});

// The chat of plants/garden#60 has 50 messages in pages of 20, and the Owner saw the first 15. The first test opens the
// chat, and the page marks all messages as seen.
const seeds = "/workstreams/plants/garden/60";

// The tests have no DOM types, so the check is a script. It gives the number of messages in the list.
const messageCount = `document.querySelectorAll('[data-message]').length`;

// The distance of the message with text from the top of the list.
const topOf = (text: string) => `(() => {
  const message = [...document.querySelectorAll('[data-message]')].find((element) => element.textContent.includes(${JSON.stringify(text)}))
  return message.getBoundingClientRect().top - message.parentElement.getBoundingClientRect().top
})()`;

test("a chat opens at the first unread message when it is in an older page", async ({ page }) => {
  await page.goto(seeds);
  await expect.poll(() => page.evaluate(shows("Seed note 16.", "top"))).toBe(true);
  expect(await page.evaluate(messageCount)).toBeGreaterThan(20);
  await expect.poll(() => unread(page, 60)).toBe(0);
  await expect.poll(() => page.evaluate(shows("Seed note 16.", "top"))).toBe(true);
});

test("a chat shows its newest 20 messages", async ({ page }) => {
  for (const size of [undefined, phone]) {
    if (size) {
      await page.setViewportSize(size);
    }
    await page.goto(seeds);
    await expect.poll(() => page.evaluate(shows("Seed note 50.", "end"))).toBe(true);
    expect(await page.evaluate(messageCount)).toBe(20);
    await expect(page.getByText("Seed note 31.")).toBeVisible();
    await expect(page.getByText("Seed note 30.")).toHaveCount(0);
  }
});

test("a scroll up loads older messages and keeps the position", async ({ page }) => {
  await page.goto(seeds);
  await expect.poll(() => page.evaluate(shows("Seed note 50.", "end"))).toBe(true);
  // The scroll starts the load of the older page, and the script reads the place of the message before it ends.
  const scrollToTop = (text: string) =>
    page.evaluate<number>(`(() => {
      const list = document.querySelector('[data-message]').parentElement
      list.scrollTop = 0
      return ${topOf(text)}
    })()`);

  const first = await scrollToTop("Seed note 31.");
  await expect.poll(() => page.evaluate(messageCount)).toBe(40);
  await expect(page.getByText("Seed note 11.")).toBeAttached();
  await expect.poll(() => page.evaluate(topOf("Seed note 31."))).toBe(first);

  const second = await scrollToTop("Seed note 11.");
  await expect.poll(() => page.evaluate(messageCount)).toBe(50);
  await expect.poll(() => page.evaluate(topOf("Seed note 11."))).toBe(second);
  await expect(page.getByText("Seed note 1.")).toBeAttached();
});

test("the messages that came while the connection was down show after the reconnect", async ({
  page,
  context,
}) => {
  await page.goto(seeds);
  await expect.poll(() => page.evaluate(shows("Seed note 50.", "end"))).toBe(true);
  await context.setOffline(true);
  await expect(page.getByRole("status")).toHaveText("Offline");
  const added = await page.request.post("/e2e/seed-notes/25");
  expect(added.ok()).toBe(true);
  await context.setOffline(false);
  await expect.poll(() => page.evaluate(messageCount)).toBe(45);
  await expect(page.getByText("Late note 25.")).toBeAttached();
  await expect(page.getByText("Late note 1.")).toBeAttached();
  await expect(page.getByText("Seed note 50.")).toBeAttached();
  await expect(page.getByText("Seed note 30.")).toHaveCount(0);
});

test("the Triager chat stays open after its actions", async ({ page }) => {
  const input = page.getByLabel("Message to the Triager");
  const send = page.getByRole("button", { name: "Send" });
  for (const [size, create, move, title, issue] of [
    [undefined, "Create the desktop Workstream.", "Move #7 to the Workstream.", "Desktop plans", 7],
    [phone, "Create the phone Workstream.", "Move #8 to the Workstream.", "Phone plans", 8],
  ] as const) {
    if (size) {
      await page.setViewportSize(size);
    }
    await page.goto("/chat");
    await input.fill(create);
    await send.click();
    // The side bar of a phone is hidden, and it has the link.
    const link = page.locator("nav a", { hasText: title });
    await expect(link).toBeAttached();
    await input.fill(move);
    await send.click();
    await expect(page.getByText(`Moved #${issue} to the Workstream #12.`)).toBeVisible();
    await expect(page).toHaveURL("/chat");
    await expect(link).toBeAttached();
  }
});

test("the open chat shows the messages that arrived while the live connection was down", async ({
  page,
  browser,
}) => {
  await page.setViewportSize(phone);
  await page.goto("/workstreams/plants/garden/12");
  await expect(page.getByText("Red roses sell best.")).toBeVisible();
  await page.evaluate("window.sameDocument = true");
  // The page connects again when the browser is online again. The new live connection fails until the route goes
  // away.
  let refused = 0;
  await page.route("/api/events", (route) => {
    refused++;
    return route.abort();
  });
  await page.evaluate("window.dispatchEvent(new Event('online'))");
  await expect.poll(() => refused).toBeGreaterThan(0);

  const other = await browser.newPage();
  await logIn(other);
  await other.goto("/workstreams/plants/garden/12");
  await other.getByLabel("Message to the Lead").fill("Water the roses at night.");
  await other.getByRole("button", { name: "Send" }).click();
  await expect(other.getByText("Water the roses at night.")).toBeVisible();
  await other.context().close();
  await expect(page.getByText("Water the roses at night.")).toBeHidden();

  await page.unroute("/api/events");
  await page.evaluate("window.dispatchEvent(new Event('online'))");
  await expect(page.getByText("Water the roses at night.")).toBeVisible();
  expect(await page.evaluate("window.sameDocument")).toBe(true);
});

test("Resume takes the issue off the list, and the hint goes away", async ({ page }) => {
  // GitHub sends the browser to this page after the Owner authorizes the App.
  await page.goto("/api/github/user-callback?code=user-code");
  const main = page.getByRole("main");
  for (const [size, workstream, numbers, issues] of [
    [undefined, 20, [21, 22], ["#21 Dig the tulip beds", "#22 Buy tulip bulbs"]],
    [phone, 30, [31, 32], ["#31 Dig the lily beds", "#32 Buy lily bulbs"]],
  ] as const) {
    if (size) {
      await page.setViewportSize(size);
    }
    // A live task waits for a slot, or its agent works, or it waits for the Lead to start an Implementer.
    const live = async (number: number) => {
      const tasks = await get<{ number: number; state: string }[]>(
        page,
        `/api/workstreams/plants/garden/${workstream}/tasks`,
      );
      const state = tasks.find((task) => task.number === number)?.state;
      return state === "queued" || state === "working" || state === "waits for start_implementer";
    };
    await page.goto(`/workstreams/plants/garden/${workstream}`);
    const resume = main.getByRole("button", { name: "Resume" });
    await expect(resume).toHaveCount(2);
    // The list is above the input and fits the screen.
    expect(
      await page.evaluate(`(() => {
        const field = document.querySelector('main form textarea').getBoundingClientRect()
        const buttons = [...document.querySelectorAll('main button')].filter((button) => button.textContent === 'Resume')
        return buttons.every((button) => {
          const box = button.getBoundingClientRect()
          return box.left >= 0 && box.right <= window.innerWidth && box.bottom <= field.top
        }) && document.documentElement.scrollWidth <= window.innerWidth
      })()`),
    ).toBe(true);
    // The side bar of a phone is hidden, and it has the hint.
    const hint = page
      .locator(`nav a[href="/workstreams/plants/garden/${workstream}"]`)
      .getByText("needs you");
    await expect(hint).toBeAttached();

    await resume.first().click();
    await expect(main.getByText(issues[0])).toBeHidden();
    await expect(main.getByText(issues[1])).toBeVisible();
    await expect(hint).toBeAttached();
    await expect.poll(() => live(numbers[0])).toBe(true);
    await resume.click();
    await expect(main.getByText(issues[1])).toBeHidden();
    await expect(hint).not.toBeAttached();
    await expect.poll(() => live(numbers[1])).toBe(true);
  }
});

test("Start gives only an open task with no blocker a button, and the row shows ready", async ({
  page,
}) => {
  await page.goto("/api/github/user-callback?code=user-code");
  await page.goto("/workstreams/plants/garden/19");
  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  const rows = page.getByRole("listitem");
  const start = page.getByRole("button", { name: /^Start #/ });
  await expect(rows.filter({ hasText: "#70 Order the bark" })).toBeVisible();
  await expect(rows.filter({ hasText: "blocked by #70" })).toBeVisible();
  await expect(rows.filter({ hasText: "needs-human" })).toBeVisible();
  await expect(start).toHaveCount(1);
  await expect(start).toHaveAccessibleName("Start #70");

  await start.click();
  await expect(rows.filter({ hasText: "#70 Order the bark" }).getByText("ready")).toBeVisible();
  await expect(start).toHaveCount(0);
});

test("Resume gives a needs-human task with no blocker a button that waits for the request", async ({
  page,
}) => {
  await page.goto("/api/github/user-callback?code=user-code");
  // The route adds #73, a needs-human task with a blocker.
  await page.route("**/api/workstreams/plants/garden/19/tasks", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: object[] };
    body.data.push({
      number: 73,
      title: "Rake the bark",
      state: "needs-human",
      depth: 0,
      otherRepository: false,
      url: "https://github.com/plants/garden/issues/73",
      blockedBy: [{ number: 70, workstreamTitle: "" }],
    });
    await route.fulfill({ response, json: body });
  });
  const requests: Route[] = [];
  await page.route("**/api/repositories/plants/garden/issues/*/resume", (route) => {
    requests.push(route);
  });
  await page.goto("/workstreams/plants/garden/19");
  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  const rows = page.getByRole("listitem");
  const resume = page.getByRole("button", { name: /^Resume #/ });
  await expect(rows.filter({ hasText: "#73 Rake the bark" })).toBeVisible();
  await expect(rows.filter({ hasText: "#73 Rake the bark" }).getByRole("button")).toHaveCount(0);
  await expect(resume).toHaveCount(1);
  await expect(resume).toHaveAccessibleName("Resume #72");
  await expect(resume).toHaveAttribute("title", "Resume #72");

  await resume.click();
  await expect(resume).toBeDisabled();
  await expect(resume.locator('[data-slot="spinner"]')).toBeVisible();
  await expect.poll(() => requests.length).toBe(1);
  expect(requests[0].request().url()).toMatch(/\/issues\/72\/resume$/);

  await requests[0].fulfill({ status: 204 });
  await expect(resume).toHaveCount(0);
  await expect(
    rows.filter({ hasText: "#72 Water the bark" }).getByText("needs-human"),
  ).toBeVisible();
});

test("the Tasks tab shows the closed tasks muted, with no Start button, when the switch is on", async ({
  page,
}) => {
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  const rows = page.getByRole("listitem");
  const closed = rows.filter({ hasText: "#37 Remove the old plan page" });
  await expect(rows.filter({ hasText: "#41 Add plan model" })).toBeVisible();
  await expect(closed).toBeHidden();

  await page.getByLabel("Show closed tasks").filter({ visible: true }).click();
  await expect(closed.getByText("closed", { exact: true })).toBeVisible();
  await expect(closed.getByText("#37 Remove the old plan page")).toHaveClass(
    /text-muted-foreground/,
  );
  await expect(closed.getByRole("button")).toHaveCount(0);
  await expect(rows.filter({ hasText: "#36 Rename the plan table" })).toBeVisible();

  await page.getByLabel("Show closed tasks").filter({ visible: true }).click();
  await expect(closed).toBeHidden();
});

test("the voice button adds the spoken text to the message", async ({ page }) => {
  // The fake recognition gives the events that the test sends.
  await page.addInitScript(`window.SpeechRecognition = class extends EventTarget {
    start() { window.recognition = this }
    stop() { this.dispatchEvent(new Event('end')) }
  }`);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await input.fill("Plant");
  await mic.click();
  await page.evaluate(
    "recognition.dispatchEvent(Object.assign(new Event('result'), { results: [Object.assign([{ transcript: ' red roses ' }], { isFinal: true })] }))",
  );
  await expect(input).toHaveValue("Plant red roses");
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await expect(mic).toBeVisible();

  await mic.click();
  await page.evaluate(
    "recognition.dispatchEvent(Object.assign(new Event('error'), { error: 'not-allowed' })); recognition.dispatchEvent(new Event('end'))",
  );
  await expect(main.getByText("The browser blocks the microphone.")).toBeVisible();
  await expect(mic).toBeVisible();
});

// The fake recognition keeps each instance in recognitions and the calls in calls. It sends no event by itself.
// Like WebKit, it throws on a start before the end of the run before.
const fakeRecognition = `window.recognitions = []
  window.calls = []
  window.SpeechRecognition = class extends EventTarget {
    constructor() {
      super()
      window.recognitions.push(this)
      this.addEventListener('end', () => { this.active = false })
    }
    start() {
      if (this.active) throw new Error('InvalidStateError')
      if (window.startError) throw new Error(window.startError)
      this.active = true
      window.calls.push('start')
    }
    stop() { window.calls.push('stop') }
    abort() { window.calls.push('abort') }
  }`;

const emit = (page: Page, type: string) =>
  page.evaluate(`recognitions[0].dispatchEvent(new Event('${type}'))`);

// A transcript that ends in ... is not final.
const result = (page: Page, ...transcripts: string[]) =>
  page.evaluate(
    `recognitions[0].dispatchEvent(Object.assign(new Event('result'), {
      results: ${JSON.stringify(transcripts)}.map((transcript) =>
        Object.assign([{ transcript: transcript.replace(/\\.\\.\\.$/, '') }], {
          isFinal: !transcript.endsWith('...'),
        }),
      ),
    }))`,
  );

const select = (page: Page, start: number, end: number) =>
  page.evaluate(`document.querySelector('textarea').setSelectionRange(${start}, ${end})`);

const selection = (page: Page) =>
  page.evaluate(
    "[document.querySelector('textarea').selectionStart, document.querySelector('textarea').selectionEnd]",
  );

test("two result events add the spoken text once", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red");
  await expect(input).toHaveValue("red");
  await result(page, "red");
  await result(page, "red", "roses...");
  await expect(input).toHaveValue("red roses");
  await result(page, "red", "roses");
  await expect(input).toHaveValue("red roses");
});

test("a final result that repeats the final result before it adds only the new text", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red", "red...");
  await expect(input).toHaveValue("red");
  await result(page, "red", "red", "red roses");
  await expect(input).toHaveValue("red roses");
  await result(page, "red", "red", "red roses", "red roses");
  await expect(input).toHaveValue("red roses");
});

test("two final results in one event that extend each other add one space", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red", "red roses");
  await expect(input).toHaveValue("red roses");
});

test("an interim result shows in the field and a later interim result replaces it", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await input.fill("Plant");
  await input.blur();
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red...");
  await expect(input).toHaveValue("Plant red");
  await result(page, "red rows...");
  await expect(input).toHaveValue("Plant red rows");
  await result(page, "red roses...");
  await expect(input).toHaveValue("Plant red roses");
});

test("a final result replaces the draft and does not repeat it", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red roses...");
  await result(page, "red roses");
  await expect(input).toHaveValue("red roses");
  await result(page, "red roses", "and white...");
  await expect(input).toHaveValue("red roses and white");
  await result(page, "red roses", "and white lilies");
  await expect(input).toHaveValue("red roses and white lilies");
});

test("an end with only a draft keeps the draft text and the next run adds after it", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await emit(page, "audiostart");
  await result(page, "red roses...");
  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "start"]);
  await expect(input).toHaveValue("red roses");

  await result(page, "and white...");
  await expect(input).toHaveValue("red roses and white");
  await result(page, "and white lilies");
  await expect(input).toHaveValue("red roses and white lilies");
});

test("a final result after Stop and before the end adds the text one time", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red roses...");
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await result(page, "red roses");
  await emit(page, "end");
  await expect(input).toHaveValue("red roses");
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start", "stop"]);
});

test("a result after Send does not put text in the field", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  const input = main.getByLabel("Message to the Lead");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "Add a plan...");
  await expect(input).toHaveValue("Add a plan");
  await main.getByRole("button", { name: "Send" }).click();
  await expect(input).toHaveValue("");
  await result(page, "Add a plan");
  await result(page, "Add a plan", "today");
  await expect(input).toHaveValue("");
});

test("the field scrolls to the end of the voice text", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await input.fill(Array.from({ length: 20 }, (_, index) => `Line ${index}`).join("\n"));
  await input.blur();
  await input.evaluate((field) => {
    field.scrollTop = 0;
  });
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red roses...");
  await expect(input).toHaveValue(/Line 19 red roses$/);
  const atEnd = () =>
    input.evaluate((field) => field.scrollTop + field.clientHeight - field.scrollHeight);
  expect(Math.abs(await atEnd())).toBeLessThanOrEqual(1);

  await result(
    page,
    "red roses and so many white lilies that the text needs a new line in the field...",
  );
  await expect(input).toHaveValue(/lilies/);
  expect(await input.evaluate((field) => field.scrollHeight > field.clientHeight)).toBe(true);
  expect(Math.abs(await atEnd())).toBeLessThanOrEqual(1);
  expect(await page.evaluate("document.activeElement === document.querySelector('textarea')")).toBe(
    false,
  );
});

test("text that the user types during a recording stays", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red roses...");
  await expect(input).toHaveValue("red roses");

  await input.pressSequentially(" really");
  await result(page, "red roses and white...");
  await expect(input).toHaveValue("red roses really and white");
  await result(page, "red roses and white lilies");
  await expect(input).toHaveValue("red roses really and white lilies");
});

test("a final result with other words than the draft does not move the new words after a touch", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "send an e mail...");
  await expect(input).toHaveValue("send an e mail");

  await input.pressSequentially(" now");
  await result(page, "send an email");
  await expect(input).toHaveValue("send an e mail now");
  await result(page, "send an email", "to Bob...");
  await expect(input).toHaveValue("send an e mail now to Bob");
});

test("a draft with more than one result does not repeat its words after a touch", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "hello world...", "how...");
  await expect(input).toHaveValue("hello world how");

  await input.pressSequentially(" now");
  await result(page, "hello world", "how are...");
  await expect(input).toHaveValue("hello world how now are");
});

test("a draft that repeats the last final text does not show the repeated words", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red", "red...");
  await expect(input).toHaveValue("red");
  await result(page, "red", "red roses...");
  await expect(input).toHaveValue("red roses");
});

test("a run that ends with a draft that repeats the last final text keeps the words one time", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red", "red roses...");
  await emit(page, "end");
  await expect(input).toHaveValue("red roses");
});

test("the field scrolls to the voice text that goes in at the cursor before other text", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  const lines = Array.from({ length: 20 }, (_, index) => `Line ${index}`);
  await input.fill(lines.join("\n"));
  const cursor = lines.slice(0, 13).join("\n").length;
  await select(page, cursor, cursor);
  await input.evaluate((field) => {
    field.scrollTop = 0;
  });
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(
    page,
    "red roses and so many white lilies that the text needs a new line in the field and then a few more words to be sure...",
  );
  await expect(input).toHaveValue(/Line 12 red roses.*words to be sure\nLine 13/);
  const gap = await page.evaluate<number>(`(() => {
    const field = document.querySelector('textarea');
    const probe = field.cloneNode();
    field.after(probe);
    probe.value = field.value.slice(0, field.selectionStart);
    const end = probe.scrollHeight;
    probe.remove();
    return field.scrollTop + field.clientHeight - end;
  })()`);
  expect(await input.evaluate((field) => field.scrollTop)).toBeGreaterThan(0);
  expect(Math.abs(gap)).toBeLessThanOrEqual(1);
});

test("the spoken text goes in at the cursor", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await input.fill("Plant roses");
  await select(page, 5, 5);
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, " red ");
  await expect(input).toHaveValue("Plant red roses");
  expect(await selection(page)).toEqual([9, 9]);

  await select(page, 0, 0);
  await result(page, "red", "Now");
  await expect(input).toHaveValue("Now Plant red roses");
  expect(await selection(page)).toEqual([3, 3]);
});

test("the spoken text replaces the selection", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await input.fill("Plant red roses.");
  await select(page, 6, 9);
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "white");
  await expect(input).toHaveValue("Plant white roses.");
  expect(await selection(page)).toEqual([11, 11]);
});

test.describe("with a German browser", () => {
  test.use({ locale: "de-DE" });

  test("the voice input gets the language of the browser", async ({ page }) => {
    await page.addInitScript(fakeRecognition);
    await page.goto("/chat");
    await page
      .getByRole("main")
      .getByRole("button", { name: "Start voice input", exact: true })
      .click();
    expect(await page.evaluate("recognitions[0].lang")).toBe("de-DE");
  });
});

test("Send stops the voice input", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await main.getByLabel("Message to the Lead").fill("Add a plan");
  await main.getByRole("button", { name: "Send" }).click();
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start", "abort"]);
});

test("Send stops a voice input that the voice button stopped", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await main.getByLabel("Message to the Lead").fill("Add a plan");
  await main.getByRole("button", { name: "Send" }).click();
  await expect(main.getByLabel("Message to the Lead")).toHaveValue("");
  expect(await page.evaluate("calls")).toEqual(["start", "stop", "abort"]);
});

test("a result of the old run after a new start does not come into the new recording", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "hello world...");
  await expect(input).toHaveValue("hello world");
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "hello world");
  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "stop", "start"]);
  await expect(input).toHaveValue("hello world");
  await result(page, "again");
  await expect(input).toHaveValue("hello world again");
});

test("the voice button lets a new voice input start after the end of the old run", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await mic.click();
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await expect(mic).toBeVisible();
  await mic.click();
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start", "stop"]);

  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "stop", "start"]);
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  expect(await page.evaluate("recognitions.length")).toBe(1);
});

test("a tap after an error starts a new voice input after the end of the old run", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await mic.click();
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'no-speech' }))",
  );
  await expect(mic).toBeVisible();
  await mic.click();
  expect(await page.evaluate("calls")).toEqual(["start"]);

  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "start"]);
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
});

test("the voice input keeps the session open across pauses", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  await page
    .getByRole("main")
    .getByRole("button", { name: "Start voice input", exact: true })
    .click();
  expect(await page.evaluate("recognitions[0].continuous")).toBe(true);
});

test("each recording adds its text after the existing text", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await input.fill("Plant");
  await input.blur();
  await mic.click();
  await result(page, "red");
  await expect(input).toHaveValue("Plant red");
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await emit(page, "end");
  await expect(mic).toBeVisible();

  await mic.click();
  await result(page, "roses");
  await expect(input).toHaveValue("Plant red roses");
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await emit(page, "end");
  await expect(mic).toBeVisible();

  await mic.click();
  await result(page, "today");
  await expect(input).toHaveValue("Plant red roses today");
  expect(await page.evaluate("recognitions.length")).toBe(1);
  expect(await page.evaluate("calls")).toEqual(["start", "stop", "start", "stop", "start"]);
});

test("the spoken text goes at the end when the textarea has no focus", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await input.fill("Plant roses");
  await select(page, 5, 5);
  await input.blur();
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "today");
  await expect(input).toHaveValue("Plant roses today");
  expect(await selection(page)).toEqual([17, 17]);
});

test("a session that the browser ends restarts", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await emit(page, "audiostart");
  await result(page, "red");
  await expect(input).toHaveValue("red");

  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "start"]);
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();

  // The result list of the new run starts again at the first result.
  await result(page, "roses");
  await expect(input).toHaveValue("red roses");
  await result(page, "roses");
  await expect(input).toHaveValue("red roses");
});

test("a session that the browser ends after an aborted error restarts", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await emit(page, "audiostart");
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'aborted' })); recognitions[0].dispatchEvent(new Event('end'))",
  );
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "start"]);
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  await expect(main.getByText("The voice input failed")).toHaveCount(0);
});

test("the voice button goes back to the start state when the browser refuses the restart", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await mic.click();
  await emit(page, "audiostart");
  await page.evaluate("window.startError = 'NotAllowedError'");
  await emit(page, "end");
  await expect(main.getByText("The voice input did not start.")).toBeVisible();
  await expect(mic).toBeVisible();

  await page.evaluate("window.startError = ''");
  await mic.click();
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  await expect(main.getByText("The voice input did not start.")).toHaveCount(0);
  expect(await page.evaluate("calls")).toEqual(["start", "start"]);
});

test("a session that never heard audio does not restart", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await emit(page, "end");
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start"]);
});

test("a session that ends with an error does not restart", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await emit(page, "audiostart");
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'network' })); recognitions[0].dispatchEvent(new Event('end'))",
  );
  await expect(main.getByText("The speech service has no network connection.")).toBeVisible();
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start"]);
});

test("a session that the user stops does not restart", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/chat");
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await emit(page, "audiostart");
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await emit(page, "end");
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start", "stop"]);
});

// The history change opens the page with no reload, so the page keeps its recognition object.
const open = (page: Page, path: string) =>
  page.evaluate(
    `history.pushState({}, '', '${path}'); dispatchEvent(new PopStateEvent('popstate'))`,
  );

const plants = "/workstreams/plants/garden/12";
const seasonal = "/workstreams/owner/shop/13";

test("the voice input works in each Workstream after a switch", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto(shop);
  const main = page.getByRole("main");
  const input = main.getByLabel("Message to the Lead");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  const stop = main.getByRole("button", { name: "Stop voice input" });

  await mic.click();
  await result(page, "red");
  await expect(input).toHaveValue("red");
  await stop.click();
  await emit(page, "end");
  await expect(mic).toBeVisible();

  await open(page, seasonal);
  await expect(input).toHaveValue("");
  await mic.click();
  await result(page, "roses");
  await expect(input).toHaveValue("roses");
  await stop.click();
  await emit(page, "end");
  await expect(mic).toBeVisible();

  await open(page, shop);
  await mic.click();
  await result(page, "today");
  await expect(input).toHaveValue("today");
  await stop.click();
  await emit(page, "end");
  await expect(mic).toBeVisible();

  await open(page, plants);
  await mic.click();
  await result(page, "lilies");
  await expect(input).toHaveValue("lilies");
  expect(await page.evaluate("recognitions.length")).toBe(1);
});

test("a switch during a voice input stops it and shows the start state", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto(shop);
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();

  await open(page, plants);
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start", "abort"]);
});

test("a voice result of the old run after a switch does not go into the new field", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto(shop);
  const main = page.getByRole("main");
  const input = main.getByLabel("Message to the Lead");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await open(page, seasonal);
  await expect(input).toHaveValue("");

  await result(page, "red");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "roses");
  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "abort", "start"]);
  await result(page, "today");
  await expect(input).toHaveValue("today");
});

test("a voice button tap after a switch during a run starts after the end of the old run", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto(shop);
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await open(page, seasonal);
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start", "abort"]);

  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "abort", "start"]);
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  await expect(main.getByText("The voice input did not start.")).toHaveCount(0);
  expect(await page.evaluate("recognitions.length")).toBe(1);
});

test("a voice error of the old run after a switch does not show in the new Workstream", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto(shop);
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await open(page, seasonal);
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'no-speech' }))",
  );
  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "abort", "start"]);
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  await expect(main.getByText("The microphone did not hear speech.")).toHaveCount(0);
});

test("the voice input shows a message when it does not start", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.addInitScript("window.startError = 'InvalidStateError'");
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await mic.click();
  await expect(main.getByText("The voice input did not start.")).toBeVisible();
  await expect(mic).toBeVisible();
});

const voiceErrors = [
  [
    "not-allowed",
    "The browser blocks the microphone. Allow the microphone in the browser settings.",
  ],
  [
    "service-not-allowed",
    "The speech service of the browser or of the device is not available. Turn on Siri or Dictation, or use a different browser.",
  ],
  ["no-speech", "The microphone did not hear speech. Speak again."],
  ["audio-capture", "The browser cannot find a microphone. Connect a microphone and try again."],
  ["network", "The speech service has no network connection. Check the connection and try again."],
  ["language-not-supported", "The speech service does not support this language."],
  ["unknown-code", "The voice input failed: unknown-code"],
];

for (const [code, message] of voiceErrors) {
  test(`the voice input shows a message for ${code}`, async ({ page }) => {
    await page.addInitScript(fakeRecognition);
    await page.goto("/workstreams/owner/shop/12");
    const main = page.getByRole("main");
    await main.getByRole("button", { name: "Start voice input", exact: true }).click();
    await page.evaluate(
      `recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: '${code}' }))`,
    );
    await expect(main.getByRole("alert")).toHaveText(message);
  });
}

test("the voice input shows no message for aborted", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await mic.click();
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'aborted' })); recognitions[0].dispatchEvent(new Event('end'))",
  );
  await expect(mic).toBeVisible();
  await expect(main.getByRole("alert")).toHaveCount(0);
});

test("a new voice input clears the voice error and keeps the send error", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await main.locator("input[type=file]").setInputFiles({
    name: "notes.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("notes"),
  });
  const sendError = "notes.txt is not a PNG, JPEG, GIF or WebP image.";
  await expect(main.getByRole("alert")).toHaveText(sendError);

  await mic.click();
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'no-speech' })); recognitions[0].dispatchEvent(new Event('end'))",
  );
  await expect(main.getByRole("alert")).toHaveText([
    "The microphone did not hear speech. Speak again.",
    sendError,
  ]);

  await mic.click();
  await expect(main.getByRole("alert")).toHaveText(sendError);
});

test("a successful send removes the voice error", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.route("/api/chat/messages", (route) => route.fulfill({ status: 204 }));
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'no-speech' })); recognitions[0].dispatchEvent(new Event('end'))",
  );
  await expect(main.getByRole("alert")).toHaveText(
    "The microphone did not hear speech. Speak again.",
  );

  await main.getByLabel("Message to the Lead").fill("Add a plan");
  await main.getByRole("button", { name: "Send" }).click();
  await expect(main.getByRole("alert")).toHaveCount(0);
});

// The fake wake lock keeps each lock in locks. A lock has released true after its release().
const fakeWakeLock = `window.locks = []
  Object.defineProperty(navigator, 'wakeLock', { configurable: true, value: {
    request: async (type) => {
      if (window.wakeLockError) throw new Error(window.wakeLockError)
      const lock = { type, released: false, release: async () => { lock.released = true } }
      window.locks.push(lock)
      return lock
    },
  } })`;

const heldLocks = (page: Page) =>
  page.evaluate("locks.filter((lock) => !lock.released).map((lock) => lock.type)");

const wakeLockTest = (name: string, run: (page: Page) => Promise<void>) =>
  test(name, async ({ page }) => {
    await page.addInitScript(fakeRecognition);
    await page.addInitScript(fakeWakeLock);
    await page.goto(shop);
    await run(page);
  });

wakeLockTest("a voice input start requests the screen wake lock", async (page) => {
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
});

wakeLockTest("the voice button stop releases the screen wake lock", async (page) => {
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await expect.poll(() => heldLocks(page)).toEqual([]);
});

wakeLockTest("Send releases the screen wake lock", async (page) => {
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red roses");
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await main.getByRole("button", { name: "Send" }).click();
  await expect.poll(() => heldLocks(page)).toEqual([]);
});

wakeLockTest("a voice error releases the screen wake lock", async (page) => {
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'network' }))",
  );
  await expect.poll(() => heldLocks(page)).toEqual([]);
});

wakeLockTest("an end without a restart releases the screen wake lock", async (page) => {
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await emit(page, "end");
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
  await expect.poll(() => heldLocks(page)).toEqual([]);
});

wakeLockTest("a restart after an end keeps the screen wake lock", async (page) => {
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await emit(page, "audiostart");
  await emit(page, "end");
  await expect.poll(() => page.evaluate("calls")).toEqual(["start", "start"]);
  expect(await heldLocks(page)).toEqual(["screen"]);
  expect(await page.evaluate("locks.length")).toBe(1);
});

wakeLockTest("a switch during a voice input releases the screen wake lock", async (page) => {
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await open(page, plants);
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
  await expect.poll(() => heldLocks(page)).toEqual([]);
});

wakeLockTest("a page that becomes visible again requests the screen wake lock", async (page) => {
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await page.evaluate(
    "locks[0].released = true; document.dispatchEvent(new Event('visibilitychange'))",
  );
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  expect(await page.evaluate("locks.length")).toBe(2);
});

wakeLockTest("a page that becomes visible while not listening requests no lock", async (page) => {
  await page.evaluate("document.dispatchEvent(new Event('visibilitychange'))");
  await page.getByRole("main").getByLabel("Message to the Lead").click();
  expect(await page.evaluate("locks.length")).toBe(0);
});

wakeLockTest("a refused wake lock request shows no error", async (page) => {
  await page.evaluate("window.wakeLockError = 'NotAllowedError'");
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red roses");
  await expect(main.getByLabel("Message to the Lead")).toHaveValue("red roses");
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  await expect(main.getByRole("alert")).toHaveCount(0);
});

test("the voice input works without navigator.wakeLock", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.addInitScript("delete Navigator.prototype.wakeLock");
  await page.goto(shop);
  const main = page.getByRole("main");
  expect(await page.evaluate("navigator.wakeLock")).toBeUndefined();
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red roses");
  await expect(main.getByLabel("Message to the Lead")).toHaveValue("red roses");
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await expect(main.getByRole("button", { name: "Start voice input", exact: true })).toBeVisible();
});

test("the note closes the Workstream when all tasks are closed", async ({ page }) => {
  const main = page.getByRole("main");
  const note = main.getByText("All tasks are closed.");
  const close = main.getByRole("button", { name: "Close Workstream" });
  for (const [size, workstream, task, closes] of [
    [undefined, 50, 52, true],
    [phone, 55, 57, false],
  ] as const) {
    if (size) {
      await page.setViewportSize(size);
    }
    const agents = () =>
      get<{ role: string; endedAt: string | null; endReason: string }[]>(
        page,
        `/api/workstreams/plants/garden/${workstream}/agents`,
      );
    // The side bar of a phone is hidden, and it has the link.
    const link = page.locator(`nav a[href="/workstreams/plants/garden/${workstream}"]`);
    const done = link.getByText("done", { exact: true });
    await page.goto(`/workstreams/plants/garden/${workstream}`);
    await expect(link).toBeAttached();
    await expect(note).toBeHidden();
    await expect(done).not.toBeAttached();

    // The Lead moves the last open task to another Workstream, and its session still runs.
    await page.getByLabel("Message to the Lead").fill(`Move #${task} to the Workstream #20.`);
    await page.getByRole("button", { name: "Send" }).click();
    await expect(note).toBeVisible();
    await expect(done).toBeAttached();
    expect(
      await page.evaluate(`(() => {
        const button = [...document.querySelectorAll('main button')].find((button) => button.textContent === 'Close Workstream')
        const box = button.getBoundingClientRect()
        const note = button.parentElement
        return box.left >= 0 && box.right <= window.innerWidth && note.scrollWidth <= note.clientWidth
      })()`),
    ).toBe(true);
    expect(
      (await agents()).some((agent) => agent.role === "lead_chat" && agent.endedAt === null),
    ).toBe(true);

    await close.click();
    if (closes) {
      await expect(page).toHaveURL("/workstreams");
      await expect(link).not.toBeAttached();
      await expect(main.getByText("Water the roses")).toBeVisible();
      await expect(main.getByText("Plant daisies")).not.toBeAttached();
      await expect(note).not.toBeAttached();
      // The close stops each agent session of the Workstream.
      await expect
        .poll(async () =>
          (await agents()).every(
            (agent) =>
              agent.endedAt !== null &&
              (agent.role !== "lead_chat" || agent.endReason === "stopped"),
          ),
        )
        .toBe(true);
    } else {
      await expect(note.locator("..").locator(".text-destructive")).toBeVisible();
      await expect(page).toHaveURL(`/workstreams/plants/garden/${workstream}`);
      await expect(link).toBeAttached();
      await expect(note).toBeVisible();
    }
  }
});

test("the Details tab closes the Workstream with its open issues", async ({ page }) => {
  const main = page.getByRole("main");
  const dialog = page.getByRole("dialog");
  const closeButton = page
    .getByRole("tabpanel", { name: "Details" })
    .getByRole("button", { name: "Close" });
  await page.goto("/workstreams/plants/garden/30");
  await page.getByRole("tab", { name: "Details" }).filter({ visible: true }).click();
  await expect(page.getByText("Sep 28, 2026")).toBeVisible();
  await expect(page.getByText("Completed tasks")).toBeVisible();
  await closeButton.click();
  await expect(dialog.getByText("#31 Dig the lily beds")).toBeVisible();
  await expect(dialog.getByText("#32 Buy lily bulbs")).toBeVisible();
  await expect(dialog.getByText("#30 Plant lilies")).toBeVisible();

  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  expect(
    await get<{ open: boolean }>(page, "/api/workstreams/plants/garden/30/details"),
  ).toMatchObject({ open: true });

  await closeButton.click();
  await dialog.getByRole("button", { name: "Close as won't do" }).click();
  await expect(page).toHaveURL("/workstreams");
  await expect(main.getByText("Plant lilies")).not.toBeAttached();
  expect(
    await get<{ open: boolean }>(page, "/api/workstreams/plants/garden/30/details"),
  ).toMatchObject({ open: false });
});

test("the voice button is not there without SpeechRecognition", async ({ page }) => {
  await page.addInitScript(
    "delete window.SpeechRecognition; delete window.webkitSpeechRecognition",
  );
  await page.goto(shop);
  const main = page.getByRole("main");
  await expect(main.getByRole("button", { name: "Send" })).toBeVisible();
  await expect(main.getByRole("button", { name: /voice input/ })).toHaveCount(0);
});

test("the voice input works with webkitSpeechRecognition only", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.addInitScript(
    "window.webkitSpeechRecognition = window.SpeechRecognition; delete window.SpeechRecognition",
  );
  await page.goto(shop);
  const main = page.getByRole("main");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red roses");
  await expect(main.getByLabel("Message to the Lead")).toHaveValue("red roses");
});

test("the voice button shows an icon, a name and its state", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto(shop);
  const main = page.getByRole("main");
  const start = main.getByRole("button", { name: "Start voice input", exact: true });
  await expect(start).toHaveAttribute("aria-pressed", "false");
  await expect(start).toHaveAttribute("title", "Start voice input");
  await expect(start.locator("svg")).toHaveCount(1);
  await expect(start).toHaveText("");

  await start.click();
  const stop = main.getByRole("button", { name: "Stop voice input", exact: true });
  await expect(stop).toHaveAttribute("aria-pressed", "true");
  await expect(stop).toHaveAttribute("title", "Stop voice input");
  await expect(stop.locator("svg")).toHaveCount(1);
  await expect(stop).toHaveText("");

  await stop.click();
  await expect(start).toHaveAttribute("aria-pressed", "false");
});

const writingNow = (page: Page) =>
  page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { writing: boolean } };
    body.data.writing = true;
    await route.fulfill({ response, json: body });
  });

test("the send button follows the text and the writing agent", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await writingNow(page);
  await page.goto(shop);
  const main = page.getByRole("main");
  const input = main.getByLabel("Message to the Lead");
  const send = main.getByRole("button", { name: "Send", exact: true });
  const stop = main.getByRole("button", { name: "Stop the reply", exact: true });

  await expect(stop).toBeEnabled();
  await expect(stop).toHaveAttribute("title", "Stop the reply");
  await expect(send).toHaveCount(0);
  await input.fill("   ");
  await expect(stop).toBeEnabled();
  await input.fill("Add a plan");
  await expect(send).toBeEnabled();
  await expect(send).toHaveAttribute("title", "Send");
  await expect(stop).toHaveCount(0);

  await input.fill("");
  const stopped = page.waitForRequest((request) => request.url().endsWith("/api/chat/stop"));
  await stop.click();
  await stopped;
});

test("the send button is disabled with no text while the agent does not write", async ({
  page,
}) => {
  await page.goto(shop);
  const main = page.getByRole("main");
  const input = main.getByLabel("Message to the Lead");
  const send = main.getByRole("button", { name: "Send", exact: true });
  await expect(send).toBeDisabled();
  await expect(main.getByRole("button", { name: "Stop the reply" })).toHaveCount(0);
  await input.fill("  ");
  await expect(send).toBeDisabled();
  await input.fill("Add a plan");
  await expect(send).toBeEnabled();
});

test("the box holds the text area, the attach button, the voice button and the send button", async ({
  page,
}) => {
  await page.addInitScript(fakeRecognition);
  await page.goto(shop);
  const main = page.getByRole("main");
  const input = main.getByLabel("Message to the Lead");
  const box = input.locator("..");
  const attach = main.getByRole("button", { name: "Attach images", exact: true });
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  const send = main.getByRole("button", { name: "Send", exact: true });
  await expect(input).toHaveCSS("border-top-width", "0px");
  await expect(box).toHaveCSS("border-top-width", "1px");
  const frame = (await box.boundingBox())!;
  const field = (await input.boundingBox())!;
  const attachBox = (await attach.boundingBox())!;
  const micBox = (await mic.boundingBox())!;
  const sendBox = (await send.boundingBox())!;
  for (const button of [attachBox, micBox, sendBox]) {
    expect(button.x).toBeGreaterThanOrEqual(frame.x);
    expect(button.x + button.width).toBeLessThanOrEqual(frame.x + frame.width);
    expect(button.y).toBeGreaterThanOrEqual(frame.y);
    expect(button.y + button.height).toBeLessThanOrEqual(frame.y + frame.height);
    expect(button.y).toBeGreaterThanOrEqual(field.y + field.height);
  }
  expect(field.width).toBeGreaterThan(frame.width - 24);
  expect(attachBox.x).toBeLessThan(micBox.x);
  expect(micBox.x + micBox.width).toBeLessThanOrEqual(sendBox.x);
  expect(attachBox.x).toBeLessThan(frame.x + 16);
  expect(sendBox.x + sendBox.width).toBeGreaterThan(frame.x + frame.width - 16);
  for (const button of [attach, mic, send]) {
    await expect(button).toHaveCSS("border-top-color", "rgba(0, 0, 0, 0)");
  }
  await expect(send).toHaveCSS("border-top-left-radius", /e\+07px$/);

  const border = (await page.evaluate(
    "getComputedStyle(document.querySelector('main form textarea').parentElement).borderTopColor",
  )) as string;
  await input.focus();
  await expect(box).not.toHaveCSS("border-top-color", border);
  await expect(box).toHaveCSS("box-shadow", "none");
});

for (const [device, size] of [
  ["desktop", undefined],
  ["phone", phone],
] as const) {
  test(`a long text grows the box and stays above the buttons on a ${device}`, async ({ page }) => {
    await page.addInitScript(fakeRecognition);
    if (size) {
      await page.setViewportSize(size);
    }
    await page.goto(shop);
    const main = page.getByRole("main");
    const input = main.getByLabel("Message to the Lead");
    const attach = main.getByRole("button", { name: "Attach images", exact: true });
    const mic = main.getByRole("button", { name: "Start voice input", exact: true });
    const send = main.getByRole("button", { name: "Send", exact: true });
    const short = (await input.boundingBox())!.height;
    await input.fill("A long line without a break. ".repeat(80));
    const long = (await input.boundingBox())!;
    expect(long.height).toBeGreaterThan(short);
    expect(long.height).toBeLessThanOrEqual(160);
    expect(await input.evaluate((field) => field.scrollHeight > field.clientHeight)).toBe(true);
    for (const button of [attach, mic, send]) {
      const buttonBox = (await button.boundingBox())!;
      expect(buttonBox.y).toBeGreaterThanOrEqual(long.y + long.height);
      if (size) {
        expect(buttonBox.width).toBeGreaterThanOrEqual(44);
        expect(buttonBox.height).toBeGreaterThanOrEqual(44);
      }
    }
  });
}

test("the voice button in the listening state has a red background", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto(shop);
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await expect(mic).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
  await mic.click();
  const listening = main.getByRole("button", { name: "Stop voice input", exact: true });
  await expect(listening).toBeVisible();
  await expect(listening).not.toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
  await expect(listening).toHaveAttribute("aria-pressed", "true");
});

test.describe("on a phone", () => {
  test.use({ viewport: phone });

  test("the textarea stays wide with the voice button and the Stop button", async ({ page }) => {
    await page.addInitScript(fakeRecognition);
    await page.route("**/api/chat?*", async (route) => {
      const response = await route.fetch();
      const body = (await response.json()) as { data: { writing: boolean } };
      body.data.writing = true;
      await route.fulfill({ response, json: body });
    });
    await page.goto(shop);
    const main = page.getByRole("main");
    await expect(main.getByRole("button", { name: "Stop the reply", exact: true })).toBeVisible();
    await expect(main.getByRole("button", { name: "Start voice input" })).toBeVisible();
    const width = await page.evaluate("document.querySelector('textarea').clientWidth");
    expect(width).toBeGreaterThanOrEqual(120);
    expect(
      await page.evaluate(
        "document.querySelector('form').scrollWidth <= document.querySelector('form').clientWidth",
      ),
    ).toBe(true);
  });
});

test("the chat shows one separator before the first message of each day", async ({ page }) => {
  const times = [
    "2025-12-30T20:00:00Z",
    "2026-09-28T09:30:00Z",
    "2026-09-28T21:40:00Z",
    "2026-10-14T09:12:00Z",
    "2026-10-15T08:37:00Z",
    "2026-10-15T08:38:00Z",
  ];
  await page.clock.setFixedTime("2026-10-15T12:00:00Z");
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { messages: unknown[] } };
    body.data.messages = times.map((time, index) => ({
      id: index + 1,
      author: "Lead",
      text: `Message ${index + 1}`,
      time,
      images: 0,
      organization: "owner",
      repository: "owner/shop",
      workstream: 12,
    }));
    await route.fulfill({ response, json: body });
  });
  await page.goto(shop);
  const main = page.getByRole("main");
  await expect(main.getByText("Message 6")).toBeVisible();

  const order = await main
    .locator("[role=separator], [data-message]")
    .evaluateAll((elements) =>
      elements.map((element) =>
        element.getAttribute("role") === "separator"
          ? (element.getAttribute("aria-label") ?? "")
          : (element.textContent ?? "").match(/Message \d/)?.[0],
      ),
    );
  expect(order).toEqual([
    "Tue, Dec 30, 2025",
    "Message 1",
    "Mon, Sep 28",
    "Message 2",
    "Message 3",
    "Yesterday",
    "Message 4",
    "Today",
    "Message 5",
    "Message 6",
  ]);
  await expect(main.getByRole("separator", { name: "Mon, Sep 28" })).toHaveText("Mon, Sep 28");
});

test.describe("a time zone east of UTC", () => {
  test.use({ timezoneId: "Europe/Warsaw" });

  test("the chat shows the UTC times of a Mobius text in the local time", async ({ page }) => {
    await page.route("**/api/chat?*", async (route) => {
      const response = await route.fetch();
      const body = (await response.json()) as { data: { messages: unknown[] } };
      body.data.messages = [
        {
          id: 1,
          author: "Event",
          text: "2026-09-28 09:30 UTC stop of #42\n\nSince 2026-09-28 23:45 UTC the check fails. Keep 09:30 UTC.",
          time: "2026-09-28T09:30:00Z",
          images: 0,
          organization: "owner",
          repository: "owner/shop",
          workstream: 12,
        },
      ];
      await route.fulfill({ response, json: body });
    });
    await page.goto(shop);
    const main = page.getByRole("main");
    await expect(main.getByText("Sep 28, 11:30 AM stop of #42")).toBeVisible();
    await main.getByText("Sep 28, 11:30 AM stop of #42").click();
    await expect(
      main.getByText("Since Sep 29, 01:45 AM the check fails. Keep 09:30 UTC."),
    ).toBeVisible();
  });
});

async function longHistory(page: Page) {
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { messages: unknown[] } };
    body.data.messages = Array.from({ length: 60 }, (_, index) => ({
      id: 1 + index,
      author: index % 2 === 0 ? "Owner" : "Lead",
      text: `History ${index} ${"word ".repeat(30)}`,
      time: "2026-10-15T08:37:00Z",
      images: 0,
      organization: "owner",
      repository: "owner/shop",
      workstream: 12,
    }));
    await route.fulfill({ response, json: body });
  });
}

// The page and the content area must not scroll, and only the message list does.
const pageScroll = `(() => ({
  window: window.scrollY,
  document: document.scrollingElement.scrollHeight - document.scrollingElement.clientHeight,
  content: document.getElementById('content').scrollTop,
  contentOverflow: document.getElementById('content').scrollHeight - document.getElementById('content').clientHeight,
}))()`;

for (const [device, size] of [
  ["desktop", undefined],
  ["phone", phone],
] as const) {
  test(`the message list scrolls and the page does not on a ${device}`, async ({ page }) => {
    if (size) {
      await page.setViewportSize(size);
    }
    await longHistory(page);
    await page.goto(shop);
    const list = page.locator("[data-message]").first().locator("..");
    await expect(page.getByText("History 59 ")).toBeVisible();
    await expect.poll(() => page.evaluate(shows("History 59 ", "end"))).toBe(true);

    const end = await list.evaluate((element) => element.scrollTop);
    expect(end).toBeGreaterThan(0);
    await list.hover();
    // A wheel scroll is animated, and a long one can stop before the end of the list.
    const scrollBy = (delta: number) =>
      expect.poll(async () => {
        await page.mouse.wheel(0, delta);
        return list.evaluate((element) => element.scrollTop);
      });
    await scrollBy(-100000).toBe(0);
    await scrollBy(100000).toBeGreaterThanOrEqual(end);
    await expect(list).toHaveCSS("overscroll-behavior-y", "contain");
    expect(await page.evaluate(pageScroll)).toEqual({
      window: 0,
      document: 0,
      content: 0,
      contentOverflow: 0,
    });
  });
}

test.describe("on a phone with the keyboard", () => {
  test.use({ viewport: phone, isMobile: true, hasTouch: true });

  test("the input stays on the tab bar after a send and after the focus leaves", async ({
    page,
  }) => {
    await longHistory(page);
    await page.goto(shop);
    await expect.poll(() => page.evaluate(shows("History 59 ", "end"))).toBe(true);
    const form = page.getByRole("main").locator("form");
    const tabBar = page.locator("nav").last();
    const gap = async () =>
      (await tabBar.boundingBox())!.y -
      (await form.boundingBox())!.y -
      (await form.boundingBox())!.height;
    expect(await gap()).toBeCloseTo(0, 0);

    const input = page.getByLabel("Message to the Lead");
    await input.tap();
    await page.keyboard.type("keyboard");
    await page.getByRole("button", { name: "Send" }).tap();
    await expect.poll(() => sent(page)).toContain("keyboard");
    await expect.poll(() => page.evaluate(shows("keyboard", "end"))).toBe(true);
    expect(await gap()).toBeCloseTo(0, 0);

    await input.blur();
    await page.getByRole("main").locator("[data-message]").first().tap();
    expect(await gap()).toBeCloseTo(0, 0);
    expect(await page.evaluate(pageScroll)).toEqual({
      window: 0,
      document: 0,
      content: 0,
      contentOverflow: 0,
    });
  });
});

async function expectStates(page: Page) {
  const main = page.getByRole("main");
  const message = (text: string) => main.locator("[data-message]", { hasText: text });
  const stored = message("Stored note").getByRole("img", {
    name: "Stored, waits for the agent",
  });
  await expect(stored).toBeVisible();
  await expect(stored).toHaveAttribute("title", "Stored, waits for the agent");
  const delivered = message("Delivered note").getByRole("img", {
    name: "Delivered to the agent at 08:40 AM",
  });
  await expect(delivered).toBeVisible();
  await expect(delivered).toHaveAttribute("title", "Delivered to the agent at 08:40 AM");
  const stopped = message("Stopped note").getByRole("img", {
    name: "Stopped, the agent did not get it, at 08:41 AM",
  });
  await expect(stopped).toBeVisible();
  await expect(stopped).toHaveAttribute("title", "Stopped, the agent did not get it, at 08:41 AM");
  await expect(message("Lead note").getByRole("img")).toHaveCount(0);
}

async function fulfillLoad(route: Route, deliveredAt: string | null) {
  const response = await route.fetch();
  const body = (await response.json()) as {
    data: { messages: { text: string; deliveredAt: string | null }[] };
  };
  for (const other of body.data.messages) {
    if (other.text === "Delivered by a load") {
      other.deliveredAt = deliveredAt;
    }
  }
  await route.fulfill({ response, json: body });
}

test.describe("the state of an Owner message", () => {
  const states = [
    { text: "Stored note", deliveredAt: null, stoppedAt: null },
    { text: "Delivered note", deliveredAt: "2026-10-15T08:40:00Z", stoppedAt: null },
    { text: "Stopped note", deliveredAt: null, stoppedAt: "2026-10-15T08:41:00Z" },
  ];

  async function showStates(
    page: Page,
    organization: string,
    repository: string,
    workstream: number,
  ) {
    await page.route("**/api/chat?*", async (route) => {
      const response = await route.fetch();
      const body = (await response.json()) as { data: { messages: unknown[] } };
      body.data.messages = [
        ...states.map((state, index) => ({
          id: index + 1,
          author: "Owner",
          browserId: "",
          text: state.text,
          time: "2026-10-15T08:37:00Z",
          deliveredAt: state.deliveredAt,
          stoppedAt: state.stoppedAt,
          images: 0,
          organization,
          repository,
          workstream,
        })),
        {
          id: 4,
          author: "Lead",
          browserId: "",
          text: "Lead note",
          time: "2026-10-15T08:37:00Z",
          deliveredAt: null,
          stoppedAt: null,
          images: 0,
          organization,
          repository,
          workstream,
        },
      ];
      await route.fulfill({ response, json: body });
    });
  }

  test("the Lead chat shows an icon for each state", async ({ page }) => {
    await showStates(page, "owner", "owner/shop", 12);
    await page.goto(shop);
    await expectStates(page);
  });

  test("the Triager chat shows an icon for each state", async ({ page }) => {
    await showStates(page, "owner", "", 0);
    await page.goto("/chat");
    await expectStates(page);
  });

  test("an older load does not take the delivery time from a message", async ({ page }) => {
    await page.goto(shop);
    const main = page.getByRole("main");
    await expect(main.locator("[data-message]").first()).toBeVisible();
    const held: Route[] = [];
    await page.route("**/api/chat?*", (route) => {
      held.push(route);
    });
    const input = page.getByLabel("Message to the Lead");
    const message = main.locator("[data-message]", { hasText: "Delivered by a load" });
    const deliveredIcon = message.getByRole("img", { name: /^Delivered to the agent at / });

    await input.fill("Delivered by a load");
    await input.press("Enter");
    await expect.poll(() => held.length).toBe(1);
    await fulfillLoad(held[0], "2026-10-15T08:40:00Z");
    await expect(deliveredIcon).toBeVisible();

    await input.fill("Second message");
    await input.press("Enter");
    await expect.poll(() => held.length).toBe(2);
    await fulfillLoad(held[1], null);
    await expect(main.locator("[data-message]", { hasText: "Second message" })).toHaveCount(1);
    await expect(deliveredIcon).toBeVisible();
    await expect(message.getByRole("img", { name: "Stored, waits for the agent" })).toHaveCount(0);
  });
});
