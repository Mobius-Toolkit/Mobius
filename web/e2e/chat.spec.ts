import { expect, test, type Page, type Route } from "@playwright/test";

const phone = { width: 390, height: 844 };
const shop = "/workstreams/owner/shop/12";

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
          return box.height >= 40 && (box.left >= field.right || box.right <= field.left)
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

    // When the server refuses the message, the input gets the message back before the new text.
    await input.pressSequentially("lost");
    await send.tap();
    await expect(input).toHaveValue("");
    await input.pressSequentially("!");
    await expect.poll(() => held.length).toBe(3);
    await held[2].fulfill({ status: 500, json: { error: "The send failed." } });
    await expect(input).toHaveValue("lost!");
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
    await expect(page.getByText("What is the state of the plans?").first()).toBeVisible();
    await page.evaluate(`(async () => {
      for (let n = 0; n < 20; n++) {
        await fetch('/api/chat/messages', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ organization: 'owner', repository: 'owner/shop', workstream: 12, text: 'spam ${device} ' + n + ' ' + 'word '.repeat(40) }),
        })
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
    await page.goto("/workstreams/new");
    await input.fill(create);
    await send.click();
    // The side bar of a phone is hidden, and it has the link.
    const link = page.locator("nav a", { hasText: title });
    await expect(link).toBeAttached();
    await input.fill(move);
    await send.click();
    await expect(page.getByText(`Moved #${issue} to the Workstream #12.`)).toBeVisible();
    await expect(page).toHaveURL("/workstreams/new");
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
    // A live task waits for a slot, or its agent works.
    const live = async (number: number) => {
      const tasks = await get<{ number: number; state: string }[]>(
        page,
        `/api/workstreams/plants/garden/${workstream}/tasks`,
      );
      const state = tasks.find((task) => task.number === number)?.state;
      return state === "queued" || state === "working";
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

test("the voice button adds the spoken text to the message", async ({ page }) => {
  // The fake recognition gives the events that the test sends.
  await page.addInitScript(`window.SpeechRecognition = class extends EventTarget {
    start() { window.recognition = this }
    stop() { this.dispatchEvent(new Event('end')) }
  }`);
  await page.goto("/workstreams/new");
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
const fakeRecognition = `window.recognitions = []
  window.calls = []
  window.SpeechRecognition = class extends EventTarget {
    start() {
      if (window.startError) throw new Error(window.startError)
      window.recognitions.push(this)
      window.calls.push('start')
    }
    stop() { window.calls.push('stop') }
    abort() { window.calls.push('abort') }
  }`;

// A transcript that ends in ... is not final.
const result = (page: Page, ...transcripts: string[]) =>
  page.evaluate(
    `recognitions[0].dispatchEvent(Object.assign(new Event('result'), {
      results: ${JSON.stringify(transcripts)}.map((transcript) =>
        Object.assign([{ transcript }], {
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
  await page.goto("/workstreams/new");
  const main = page.getByRole("main");
  const input = page.getByLabel("Message to the Triager");
  await main.getByRole("button", { name: "Start voice input", exact: true }).click();
  await result(page, "red");
  await expect(input).toHaveValue("red");
  await result(page, "red");
  await result(page, "red", "roses...");
  await expect(input).toHaveValue("red");
  await result(page, "red", "roses");
  await expect(input).toHaveValue("red roses");
});

test("the spoken text goes in at the cursor", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/new");
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
  await page.goto("/workstreams/new");
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
    await page.goto("/workstreams/new");
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

test("the voice button lets a new voice input start at once", async ({ page }) => {
  await page.addInitScript(fakeRecognition);
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await mic.click();
  await main.getByRole("button", { name: "Stop voice input" }).click();
  await expect(mic).toBeVisible();
  await mic.click();
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  expect(await page.evaluate("calls")).toEqual(["start", "stop", "start"]);

  // The end of the old session does not stop the new session.
  await page.evaluate("recognitions[0].dispatchEvent(new Event('end'))");
  await expect(main.getByRole("button", { name: "Stop voice input" })).toBeVisible();
  await page.evaluate("recognitions[1].dispatchEvent(new Event('end'))");
  await expect(mic).toBeVisible();
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
  await page.route("/api/chat/messages", (route) =>
    route.fulfill({ status: 500, json: { error: "The send failed." } }),
  );
  await page.goto("/workstreams/owner/shop/12");
  const main = page.getByRole("main");
  const mic = main.getByRole("button", { name: "Start voice input", exact: true });
  await main.getByLabel("Message to the Lead").fill("Add a plan");
  await main.getByRole("button", { name: "Send" }).click();
  await expect(main.getByRole("alert")).toHaveText("The send failed.");

  await mic.click();
  await page.evaluate(
    "recognitions[0].dispatchEvent(Object.assign(new Event('error'), { error: 'no-speech' })); recognitions[0].dispatchEvent(new Event('end'))",
  );
  await expect(main.getByRole("alert")).toHaveText([
    "The microphone did not hear speech. Speak again.",
    "The send failed.",
  ]);

  await mic.click();
  await expect(main.getByRole("alert")).toHaveText("The send failed.");
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

test.describe("on a phone", () => {
  test.use({ viewport: phone });

  test("the textarea stays wide with the Stop, voice and Send buttons", async ({ page }) => {
    await page.addInitScript(fakeRecognition);
    await page.route("**/api/chat?*", async (route) => {
      const response = await route.fetch();
      const body = (await response.json()) as { data: { writing: boolean } };
      body.data.writing = true;
      await route.fulfill({ response, json: body });
    });
    await page.goto(shop);
    const main = page.getByRole("main");
    await expect(main.getByRole("button", { name: "Stop", exact: true })).toBeVisible();
    await expect(main.getByRole("button", { name: "Start voice input" })).toBeVisible();
    await expect(main.getByRole("button", { name: "Send" })).toBeVisible();
    const width = await page.evaluate("document.querySelector('textarea').clientWidth");
    expect(width).toBeGreaterThanOrEqual(120);
    expect(
      await page.evaluate(
        "document.querySelector('form').scrollWidth <= document.querySelector('form').clientWidth",
      ),
    ).toBe(true);
  });
});
