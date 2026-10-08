import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

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
    const entry = main.getByRole("button").filter({ hasText: reason });
    await expect(entry.getByText(badge, { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(dot);
  }
  const running = main.getByRole("button", { name: /Ticket #41 Add plan model/ });
  await expect(running.locator("span.rounded-full")).toHaveClass(/bg-green-600/);
  await expect(running.getByText("queued")).toBeHidden();
});

test("the Agents tab of a Workstream shows the state of each agent with a queue reason", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  const tree = page.getByRole("complementary");
  for (const { reason, badge, dot } of states) {
    const entry = tree.getByRole("button").filter({ hasText: reason });
    await expect(entry.getByText(badge, { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(dot);
  }
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
    await expect(page.getByRole("main").getByRole("button", { name: "Agents" })).toBeHidden();
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
      if (this.url.endsWith('/api/events') && !window.sources.includes(this)) {
        window.sources.push(this);
      }
      return add.call(this, type, listener, options);
    };
  `);
  let session = 0;
  await page.route(/\/api\/agents\/\d+\/transcript$/, (route) => {
    session = Number(/agents\/(\d+)\//.exec(route.request().url())?.[1]);
    return route.fulfill({
      json: { data: Array.from({ length: 30 }, (_, n) => ({ ...entry(n), session })) },
    });
  });
  return (id: number) =>
    page.evaluate(
      `window.sources.forEach((source) => source.dispatchEvent(new MessageEvent('transcript', { data: ${JSON.stringify(
        JSON.stringify({ ...entry(id), session }),
      )} })))`,
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
        await page.getByRole("banner").getByRole("button", { name: "Agents" }).click();
      }
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
            : page.getByRole("button", { name: "Agents", exact: true }).filter({ visible: true }),
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
