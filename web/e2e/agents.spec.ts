import { expect, test, type Locator, type Page, type Route } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

// The Curators of the repositories use the paused Harness too, so more than one entry shows the pause.
const states = [
  { reason: "runs .mobius/check", badge: "checks", dot: /bg-green-600/ },
  { reason: "waits for a check slot", badge: "waits for check", dot: /bg-amber-500/ },
  { reason: /paused until Sep 28, 12:00\sPM/, badge: "paused", dot: /bg-amber-500/ },
  { reason: "no free Implementer slot (2/2)", badge: "queued", dot: /bg-amber-500/ },
];

test("the Agents page shows the state of each agent with a queue reason", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/agents");
  for (const { reason, badge, dot } of states) {
    const entry = main.getByRole("button").filter({ hasText: reason }).first();
    await expect(entry.getByText(badge, { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(dot);
  }
  const running = main.getByRole("button", { name: /Ticket #41 Add plan model/ });
  await expect(running.locator("span.rounded-full")).toHaveClass(/bg-green-600/);
  await expect(running.getByText("queued")).toBeHidden();
});

test("the Agents page shows the count of each Role and no global count", async ({ page }) => {
  await page.goto("/agents");
  await expect(page.getByRole("heading", { name: "Implementer 2 / 2" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Reviewer 0 / 2" })).toBeVisible();
  await expect(page.getByRole("banner").getByText(/^\d+ \/ \d+$/)).toBeHidden();
  await expect(page.getByRole("main").getByText(/^\d+ \/ \d+$/)).toBeHidden();
});

test("the Agents tab of a Workstream shows the state of each agent with a queue reason", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  const tree = page.getByRole("complementary");
  for (const { reason, badge, dot } of states) {
    const entry = tree.getByRole("button").filter({ hasText: reason }).first();
    await expect(entry.getByText(badge, { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(dot);
  }
});

// Changes the live Implementer session of #41, and sets the old reason again at the end.
async function withLowLoad(page: Page, run: () => Promise<void>) {
  const put = (reason: string) =>
    page.request.put(`/e2e/agents/41/queue-reason?reason=${encodeURIComponent(reason)}`);
  expect((await put("waits for a low load")).ok()).toBe(true);
  try {
    await run();
  } finally {
    expect((await put("")).ok()).toBe(true);
  }
}

test("the Agents page shows the state of a session that waits for a low load", async ({ page }) => {
  await withLowLoad(page, async () => {
    await page.goto("/agents");
    const entry = page.getByRole("main").getByRole("button", { name: /Ticket #41/ });
    await expect(entry.getByText("waits for a low load")).toBeVisible();
    await expect(entry.getByText("waits for load", { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(/bg-amber-500/);
  });
});

test("the Agents tab of a Workstream shows the state of a session that waits for a low load", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await withLowLoad(page, async () => {
    await page.goto("/workstreams/owner/shop/12");
    await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
    const entry = page
      .getByRole("complementary")
      .getByRole("button")
      .filter({ hasText: "waits for a low load" });
    await expect(entry).toHaveCount(1);
    await expect(entry.getByText("waits for load", { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(/bg-amber-500/);
  });
});

test.describe("the transcript of an agent on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("has the agent name and the back button in the top bar, and the back button closes it", async ({
    page,
  }) => {
    await page.goto("/agents");
    await page
      .getByRole("main")
      .getByRole("button", { name: /Ticket #41 Add plan model/ })
      .click();
    const bar = page.getByRole("banner");
    await expect(bar.getByRole("heading")).not.toHaveText("Agents");
    await expect(page.getByRole("main").getByRole("button", { name: "Back" })).toBeHidden();
    await bar.getByRole("button", { name: "Back" }).click();
    await expect(page).toHaveURL("/agents");
    await expect(bar.getByRole("heading")).toHaveText("Agents");
  });
});

// The log of the Implementer of #41 has 30 entries, and a new entry comes as a server-sent event. The tests have no DOM
// types, so the checks are scripts.
const entry = (id: number) => ({
  id,
  session: 0,
  kind: "update",
  time: "2026-09-28T12:00:00Z",
  text: `Entry ${id} ${"word ".repeat(30)}`,
  body: "",
  folded: false,
  error: false,
  harnessToolName: "",
  raw: "{}",
});

async function mockLog(page: Page) {
  await page.addInitScript(`
    window.sources = [];
    const add = EventSource.prototype.addEventListener;
    EventSource.prototype.addEventListener = function (type, listener, options) {
      if (this.url.endsWith('/transcript/events') && !window.sources.includes(this)) {
        window.sources.push(this);
      }
      return add.call(this, type, listener, options);
    };
  `);
  let session = 0;
  await page.route(/\/api\/agents\/\d+\/transcript(\?.*)?$/, (route) => {
    session = Number(/agents\/(\d+)\//.exec(route.request().url())?.[1]);
    return route.fulfill({
      json: {
        data: {
          lines: Array.from({ length: 30 }, (_, n) => ({ ...entry(n), session })),
          older: false,
        },
      },
    });
  });
  return (id: number) =>
    page.evaluate(
      `window.sources.at(-1).dispatchEvent(new MessageEvent('transcript', { data: ${JSON.stringify(
        JSON.stringify({ ...entry(id), session }),
      )} }))`,
    );
}

const log = `[...document.querySelectorAll('li')].find((element) => element.textContent.includes('Entry 0 ')).parentElement`;
const scrolls = `(() => { const list = ${log}; return list.scrollHeight > list.clientHeight })()`;
const gap = `(() => { const list = ${log}; return list.scrollHeight - list.scrollTop - list.clientHeight })()`;
// The scroll event of a script scroll comes after the script, and the log reads its place from that event.
const scrollTo = (page: Page, place: "top" | "end") =>
  page.evaluate(`new Promise((resolve) => {
    const list = ${log}
    list.addEventListener('scroll', () => requestAnimationFrame(resolve), { once: true })
    list.scrollTop = ${place === "top" ? "0" : "list.scrollHeight"}
  })`);

async function openLog(page: Page, path: string, open: (page: Page) => Promise<void>) {
  const send = await mockLog(page);
  await page.goto(path);
  await open(page);
  await expect(page.getByText("Entry 29 ")).toBeVisible();
  expect(await page.evaluate(scrolls)).toBe(true);
  return send;
}

const button = (page: Page) => page.getByRole("button", { name: "New messages" });

const places = [
  {
    name: "the Agents page",
    path: "/agents",
    open: async (page: Page) => {
      await page
        .getByRole("main")
        .getByRole("button", { name: /Ticket #41 Add plan model/ })
        .click();
    },
  },
  {
    name: "the side panel",
    path: "/workstreams/owner/shop/12",
    open: async (page: Page) => {
      if (page.viewportSize()!.width < 768) {
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
      await page
        .getByRole("button", { name: /^implementer devin · swe-1.5 · Sep \d+,/ })
        .filter({ visible: true })
        .click();
    },
  },
];
const devices = [
  { name: "a desktop", size: { width: 1280, height: 720 } },
  { name: "a phone", size: { width: 390, height: 844 } },
];

for (const { name: place, path, open } of places) {
  for (const { name: device, size } of devices) {
    test.describe(`the agent log in ${place} on ${device}`, () => {
      test.use({ viewport: size });

      test("opens at the last entry and keeps the header in view", async ({ page }) => {
        await openLog(page, path, open);
        await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
        const phonePage = path === "/agents" && size.width < 768;
        await expect(
          phonePage
            ? page.getByRole("banner").getByRole("button", { name: "Back" })
            : path === "/agents"
              ? page.getByRole("main").getByRole("button", { name: "Back" })
              : page.locator('[data-slot="tabs-content"]').getByRole("button", { name: "Back" }),
        ).toBeInViewport();
        await expect(button(page)).toBeHidden();
      });

      test("follows a new entry while the Owner is at the end", async ({ page }) => {
        const send = await openLog(page, path, open);
        await send(30);
        await expect(page.getByText("Entry 30 ")).toBeVisible();
        await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
        await expect(button(page)).toBeHidden();
      });

      test("stays in place and shows the button when the Owner scrolled up", async ({ page }) => {
        const send = await openLog(page, path, open);
        await scrollTo(page, "top");
        await expect.poll(() => page.evaluate(gap)).toBeGreaterThan(100);
        await send(30);
        await expect(button(page)).toBeVisible();
        expect(await page.evaluate(`${log}.scrollTop`)).toBe(0);
        await expect(page.getByText("Entry 0 ")).toBeInViewport();
      });

      test("moves to the end and removes the button when the Owner clicks it", async ({ page }) => {
        const send = await openLog(page, path, open);
        await scrollTo(page, "top");
        await send(30);
        await button(page).click();
        await expect(button(page)).toBeHidden();
        await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
        await expect(page.getByText("Entry 30 ")).toBeInViewport();
        await send(31);
        await expect(page.getByText("Entry 31 ")).toBeInViewport();
      });

      test("removes the button when the Owner scrolls to the end", async ({ page }) => {
        const send = await openLog(page, path, open);
        await scrollTo(page, "top");
        await send(30);
        await expect(button(page)).toBeVisible();
        await scrollTo(page, "end");
        await expect(button(page)).toBeHidden();
        await send(31);
        await expect(page.getByText("Entry 31 ")).toBeInViewport();
      });
    });
  }
}

for (const { name: device, size } of devices) {
  test.describe(`the agent log in the Agents tab on ${device}`, () => {
    test.use({ viewport: size });

    test("has no card and fills the height of the tab", async ({ page }) => {
      await openLog(page, places[1].path, places[1].open);
      const tab = page.locator('[data-slot="tabs-content"]').filter({ visible: true });
      await expect(tab.locator('[data-slot="card"]')).toHaveCount(0);
      await expect(tab.getByRole("button", { name: "Back" })).toBeVisible();
      await expect(tab.locator(".line-clamp-2")).toContainText("implementer");
      const note = tab.getByText("Read only. The Owner talks only to the Lead.");
      const tabBox = await boxOf(tab);
      const noteBox = await boxOf(note);
      const padding = 8;
      expect(tabBox.y + tabBox.height - (noteBox.y + noteBox.height)).toBeGreaterThanOrEqual(0);
      expect(tabBox.y + tabBox.height - (noteBox.y + noteBox.height)).toBeLessThanOrEqual(
        padding + 1,
      );
      expect(await tab.evaluate((el) => el.scrollHeight <= el.clientHeight)).toBe(true);
      expect(
        await page.evaluate(
          `document.documentElement.scrollHeight <= document.documentElement.clientHeight`,
        ),
      ).toBe(true);
    });
  });
}

for (const { name: place, path, open } of places) {
  test(`the agent log in ${place} shows Connecting… until its stream opens`, async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 720 });
    await mockLog(page);
    let stream: Route | undefined;
    await page.route("**/api/agents/*/transcript/events", (route) => {
      stream = route;
    });
    await page.goto(path);
    await open(page);
    const badge = page.getByText("Connecting…").filter({ visible: true });
    await expect(badge).toBeVisible();
    const list = page
      .getByText("Read only. The Owner talks only to the Lead.")
      .filter({ visible: true })
      .locator("xpath=preceding-sibling::div/ul");
    const placeOfList = await list.boundingBox();

    await page.unroute("**/api/agents/*/transcript/events");
    await stream!.continue();
    await expect(page.getByText("Entry 29 ")).toBeVisible();
    await expect(badge).toBeHidden();
    // The list is empty while the stream is down, so only its top and its width stay.
    expect(await list.boundingBox()).toMatchObject({
      x: placeOfList!.x,
      y: placeOfList!.y,
      width: placeOfList!.width,
    });
  });
}

const retryDelay = 3000;

for (const { name: place, path, open } of places) {
  test(`the agent log in ${place} opens its stream again after the server refuses it`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 720 });
    const send = await mockLog(page);
    const times: number[] = [];
    await page.route("**/api/agents/*/transcript/events", (route) => {
      times.push(Date.now());
      return times.length === 1 ? route.fulfill({ status: 503 }) : route.continue();
    });
    await page.goto(path);
    await open(page);
    const badge = page.getByText("Connecting…").filter({ visible: true });
    await expect(badge).toBeVisible();
    await expect(page.getByText("Entry 29 ")).toBeVisible();
    await expect(badge).toBeHidden();
    expect(times).toHaveLength(2);
    expect(times[1] - times[0]).toBeGreaterThanOrEqual(retryDelay - 500);
    await send(30);
    await expect(page.getByText("Entry 30 ")).toBeVisible();
  });
}

test("the agent log sends no request of its stream after it closes while a retry waits", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await mockLog(page);
  let requests = 0;
  await page.route("**/api/agents/*/transcript/events", (route) => {
    requests++;
    return route.fulfill({ status: 503 });
  });
  await page.goto("/agents");
  await page
    .getByRole("main")
    .getByRole("button", { name: /Ticket #41 Add plan model/ })
    .click();
  await expect.poll(() => requests).toBe(1);
  await page.getByRole("main").getByRole("button", { name: "Back" }).click();
  await expect(page.getByText("Read only. The Owner talks only to the Lead.")).toBeHidden();
  await page.waitForTimeout(retryDelay + 1000);
  expect(requests).toBe(1);
});

test("the Transcript of a session that waits for a low load has a button that starts the check", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const requests: string[] = [];
  let answer: { status: number; body: string } = { status: 204, body: "" };
  await page.route(/\/api\/agents\/\d+\/start-check$/, (route) => {
    requests.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`);
    return route.fulfill(answer);
  });
  await withLowLoad(page, async () => {
    await page.goto("/agents");
    await main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click();
    const start = main.getByRole("button", { name: "Start the check now" });

    answer = {
      status: 409,
      body: JSON.stringify({ error: "The check of session 3 does not wait for a low load." }),
    };
    await start.click();
    await expect(
      main.getByText("The check of session 3 does not wait for a low load."),
    ).toBeVisible();

    answer = { status: 204, body: "" };
    await start.click();
    await expect(main.getByText("does not wait for a low load.")).toBeHidden();
  });
  expect(requests).toHaveLength(2);
  expect(requests[0]).toMatch(/^POST \/api\/agents\/\d+\/start-check$/);
});

test("the Transcript of a session that does not wait for a low load has no start button", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/agents");
  await main.getByRole("button", { name: /Ticket #39/ }).click();
  await expect(main.getByRole("button", { name: "Start the check now" })).toBeHidden();
});

async function openPanelTranscript(page: Page, name = /^implementer devin · swe-1.5 · Sep \d+,/) {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  const panel = page.getByRole("complementary");
  await panel.getByRole("button", { name }).filter({ visible: true }).click();
  return panel;
}

async function boxOf(locator: Locator) {
  const box = await locator.boundingBox();
  expect(box).not.toBeNull();
  return box!;
}

async function expectLeftOf(back: Locator, title: Locator) {
  const backBox = await boxOf(back);
  const titleBox = await boxOf(title);
  expect(backBox.x + backBox.width).toBeLessThanOrEqual(titleBox.x);
}

test.describe("the back button on a desktop", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("of the transcript in the side panel is on the left of the title", async ({ page }) => {
    const panel = await openPanelTranscript(page);
    const back = panel.getByRole("button", { name: "Back" });
    const title = panel.locator(".line-clamp-2");
    await expect(title).toContainText("implementer");
    await expectLeftOf(back, title);
    await back.click();
    await expect(
      panel.getByRole("button", { name: /^implementer devin · swe-1.5 · Sep \d+,/ }),
    ).toBeVisible();
    await expect(back).toBeHidden();
  });

  test("of the transcript on the Agents page is on the left of the title", async ({ page }) => {
    await page.goto("/agents");
    const main = page.getByRole("main");
    await main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click();
    const back = main.getByRole("button", { name: "Back" });
    await expectLeftOf(back, main.getByRole("heading", { name: /^implementer/ }));
    await back.click();
    await expect(main.getByRole("button", { name: /Ticket #41 Add plan model/ })).toBeVisible();
    await expect(back).toBeHidden();
  });

  test("has the name Back and no text", async ({ page }) => {
    const panel = await openPanelTranscript(page);
    const back = panel.getByRole("button", { name: "Back", exact: true });
    await expect(back).toHaveText("");
    await expect(back.locator("svg")).toBeVisible();
  });

  for (const { page: subpage, title, parent, heading } of [
    {
      page: "/settings/checkup/tools",
      title: "Tools",
      parent: "/settings/checkup",
      heading: "Checkup",
    },
    {
      page: "/settings/memory/plants/garden",
      title: "Memory of plants/garden",
      parent: "/settings/memory",
      heading: "Memory",
    },
  ]) {
    test(`of ${title} is on the left of the title and opens its parent`, async ({ page }) => {
      await page.goto(subpage);
      const main = page.getByRole("main");
      const back = main.getByRole("button", { name: "Back" });
      await expectLeftOf(back, main.getByRole("heading", { name: title, level: 2 }));
      await back.click();
      await expect(page).toHaveURL(parent);
      await expect(main.getByRole("heading", { name: heading })).toBeVisible();
    });
  }

  for (const path of [
    "/github",
    "/devices",
    "/settings/checkup",
    "/settings/memory",
    "/agents",
    "/usage",
  ]) {
    test(`is not on ${path}`, async ({ page }) => {
      await page.goto(path);
      await expect(page.getByRole("main").getByRole("heading").first()).toBeVisible();
      await expect(page.getByRole("main").getByRole("button", { name: "Back" })).toBeHidden();
    });
  }

  test("of the transcript in the side panel shows the start button under the title row and a long title", async ({
    page,
  }) => {
    await withLowLoad(page, async () => {
      const panel = await openPanelTranscript(page, /waits for a low load/);
      const title = panel.locator(".line-clamp-2");
      await expect(title).toContainText("implementer");
      const titleBox = await boxOf(title);
      expect(titleBox.width).toBeGreaterThan(100);
      const startBox = await boxOf(panel.getByRole("button", { name: "Start the check now" }));
      expect(startBox.y).toBeGreaterThanOrEqual(titleBox.y + titleBox.height);
    });
  });
});

// The log of a long session has the rows 1 to total.rows. A page has 100 rows, as the server gives.
async function mockPages(page: Page, total: { rows: number }, requests: (number | undefined)[]) {
  await mockLog(page);
  await page.route(/\/api\/agents\/\d+\/transcript(\?.*)?$/, (route) => {
    const before = new URL(route.request().url()).searchParams.get("before");
    requests.push(before ? Number(before) : undefined);
    const last = before ? Number(before) - 1 : total.rows;
    const first = Math.max(1, last - 99);
    return route.fulfill({
      json: {
        data: {
          lines: Array.from({ length: last - first + 1 }, (_, n) => entry(first + n)),
          older: first > 1,
        },
      },
    });
  });
}

const openAgentLog = async (page: Page) => {
  await page.goto("/agents");
  await page
    .getByRole("main")
    .getByRole("button", { name: /Ticket #41 Add plan model/ })
    .click();
};

const lineIds = (page: Page) =>
  page
    .locator("li[data-line]")
    .evaluateAll((items) => items.map((item) => Number(item.getAttribute("data-line"))));

test("the agent log of a long session shows the newest rows and loads the older rows at the top", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  const requests: (number | undefined)[] = [];
  await mockPages(page, { rows: 250 }, requests);
  await openAgentLog(page);
  await expect(page.getByText("Entry 250 ")).toBeVisible();
  await expect(page.getByText("Entry 151 ")).toHaveCount(1);
  await expect(page.getByText("Entry 150 ")).toHaveCount(0);
  expect(requests).toEqual([undefined]);

  await page.evaluate(
    `new Promise((resolve) => {
      const list = document.querySelector('li[data-line]').parentElement
      list.addEventListener('scroll', () => requestAnimationFrame(resolve), { once: true })
      list.scrollTop = 0
    })`,
  );
  const first = page.locator('li[data-line="151"]');
  const listTop = (await page.locator("li[data-line]").first().locator("xpath=..").boundingBox())!
    .y;
  await expect(page.getByText("Entry 150 ")).toHaveCount(1);
  expect(requests).toEqual([undefined, 151]);
  expect(Math.abs((await first.boundingBox())!.y - listTop)).toBeLessThan(20);
  expect(await lineIds(page)).toHaveLength(200);
});

test("the agent log loads pages after a reconnect until it has the newest known row, and shows no row two times", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  const requests: (number | undefined)[] = [];
  const total = { rows: 250 };
  await mockPages(page, total, requests);
  await openAgentLog(page);
  await expect(page.getByText("Entry 250 ")).toBeVisible();

  total.rows = 350;
  await page.evaluate(`window.sources.at(-1).dispatchEvent(new Event('open'))`);

  await expect(page.getByText("Entry 350 ")).toBeVisible();
  expect(requests).toEqual([undefined, undefined, 251]);
  const ids = await lineIds(page);
  expect(ids).toHaveLength(200);
  expect(new Set(ids).size).toBe(200);
  expect(ids.at(0)).toBe(151);
  expect(ids.at(-1)).toBe(350);
});

test("a live event with a longer raw text changes the raw text of a row with a body of 300 characters", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await mockLog(page);
  const body = "b".repeat(300);
  const row = (raw: string) => ({ ...entry(5), body, raw });
  const before = `{"text":"${body}"}`;
  const after = `{"text":"${body} and more text"}`;
  await page.route(/\/api\/agents\/\d+\/transcript(\?.*)?$/, (route) =>
    route.fulfill({ json: { data: { lines: [row(before)], older: false } } }),
  );
  await openAgentLog(page);
  const item = page.locator('li[data-line="5"]');
  await item.getByRole("button", { name: "Raw" }).click();
  await expect(item.locator("pre").last()).toHaveText(before);

  await page.evaluate(
    `window.sources.at(-1).dispatchEvent(new MessageEvent('transcript', { data: ${JSON.stringify(
      JSON.stringify(row(after)),
    )} }))`,
  );

  await expect(item.locator("pre").last()).toHaveText(after);
});
