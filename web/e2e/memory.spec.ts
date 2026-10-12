import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("the Owner sees the repositories of each organization in a section and opens one", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/settings/memory");
  await expect(page.locator("#content").getByRole("button", { name: "owner" })).toHaveCount(0);

  const owner = main
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "owner", exact: true }) });
  const plants = main
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "plants", exact: true }) });
  await expect(owner.getByRole("link")).toHaveText(["owner/shop"]);
  await expect(plants.getByRole("link")).toHaveText(["plants/garden"]);

  await plants.getByRole("link", { name: "plants/garden" }).click();
  await expect(page).toHaveURL("/settings/memory/plants/garden");
  await expect(page.getByText("Memory of plants/garden").filter({ visible: true })).toBeVisible();
});

test("the Owner edits the memory file, sees the 200-line error, and reverts the edit", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/settings/memory/plants/garden");
  await expect(main.getByText("The memory file is empty.")).toBeVisible();
  await expect(main.getByText("The memory file has no version.")).toBeVisible();

  await main.getByRole("button", { name: "Edit" }).click();
  await main.getByLabel("Memory text").fill("Water the roses in the morning.\n");
  await main.getByRole("button", { name: "Save" }).click();
  await expect(main.getByText("Water the roses in the morning.").first()).toBeVisible();
  await expect(
    main.locator('[data-kind="added"]', { hasText: "Water the roses in the morning." }),
  ).toBeVisible();
  await expect(main.getByText("owner", { exact: true })).toBeVisible();

  await main.getByRole("button", { name: "Edit" }).click();
  await main.getByLabel("Memory text").fill("Water the roses.\n".repeat(201));
  await main.getByRole("button", { name: "Save" }).click();
  await expect(main.getByText("the maximum is 200")).toBeVisible();
  await main.getByRole("button", { name: "Cancel" }).click();
  await expect(main.getByText("the maximum is 200")).toBeHidden();

  await main.getByRole("button", { name: "Edit" }).click();
  await main.getByLabel("Memory text").fill("Water the roses at noon.\n");
  await main.getByRole("button", { name: "Save" }).click();
  await expect(main.locator("ins", { hasText: "at noon" })).toBeVisible();
  await expect(main.getByText("A later version changed this part. Edit the file.")).toBeVisible();
  const reverts = main.getByRole("button", { name: "Revert" });
  await expect(reverts.first()).toBeEnabled();
  await expect(reverts.last()).toBeDisabled();

  await reverts.first().click();
  await expect(main.locator("del", { hasText: "at noon" })).toBeVisible();
  await expect(main.getByText("Water the roses in the morning.").first()).toBeVisible();

  await main.getByRole("button", { name: "Edit" }).click();
  await main.getByLabel("Memory text").fill("My text.\n");
  await page.evaluate(`(async () => {
    const url = "/api/repositories/plants/garden/memory";
    const listed = await fetch(url).then((response) => response.json());
    await fetch(url, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: "Other text.\\n", base_version: listed.data[0].id }),
    });
  })()`);
  await main.getByRole("button", { name: "Save" }).click();
  await expect(
    main.getByText("The memory file changed after the start of your edit."),
  ).toBeVisible();
  await expect(main.getByLabel("Memory text")).toHaveValue("My text.\n");
});

const text = (...lines: string[]) => lines.map((line) => `${line}\n`).join("");

type Scrollable = { scrollWidth: number; clientWidth: number; scrollLeft: number };

const longReason = "Change: the long line names the edit of a test";

test("a changed line shows its removed words and its added words, and the unchanged words have no mark", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/settings/memory/owner/shop");

  const commit = main.locator('[data-kind="changed"]', {
    hasText: "Run make fmt before each commit and each push.",
  });
  await expect(commit.locator("ins")).toHaveText(["and each push"]);
  await expect(commit.locator("del")).toHaveCount(0);

  const long = main.locator("li").filter({ hasText: longReason }).locator('[data-kind="changed"]');
  await expect(long.locator("del")).toHaveText(["change", "fix round"]);
  await expect(long.locator("ins")).toHaveText(["edit", "Implementer"]);
});

test("a hunk has one unchanged line above and below the change, and a separator comes between hunks", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.route(
    (url) => url.pathname === "/api/repositories/owner/shop/memory",
    (route) =>
      route.fulfill({
        json: {
          data: [
            {
              id: 2,
              author: "owner",
              reason: "",
              revert_problem: "",
              revertible: true,
              time: "2026-09-28T09:00:00Z",
              text: text("a1", "a2", "a3 new", "a4", "a5", "a6", "a7", "a8 new", "a9"),
            },
            {
              id: 1,
              author: "owner",
              reason: "",
              revert_problem: "",
              revertible: true,
              time: "2026-09-28T08:00:00Z",
              text: text("a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"),
            },
          ],
        },
      }),
  );
  await page.goto("/settings/memory/owner/shop");

  const newest = main.locator("li").first();
  await expect(newest.locator("pre")).toHaveCount(2);
  await expect(newest.getByRole("separator")).toHaveCount(1);
  const first = newest.locator("pre").first().locator("[data-kind]");
  await expect(first).toHaveText(["a2", "a3 new", "a4"]);
  await expect(first.nth(1)).toHaveAttribute("data-kind", "changed");
  const last = newest.locator("pre").last().locator("[data-kind]");
  await expect(last).toHaveText(["a7", "a8 new", "a9"]);
});

test("the background of a long line reaches the end of the text after a scroll to the right", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/settings/memory/owner/shop");

  const hunk = main.locator("li").filter({ hasText: longReason }).locator("pre");
  const line = hunk.locator('[data-kind="changed"]');
  await expect(line).toBeVisible();
  const scroll = await hunk.evaluate((el: Scrollable) => {
    const overflows = el.scrollWidth > el.clientWidth;
    el.scrollLeft = el.scrollWidth;
    return { overflows, width: el.scrollWidth };
  });
  expect(scroll.overflows).toBe(true);

  const hunkBox = await hunk.boundingBox();
  const lineBox = await line.boundingBox();
  expect(lineBox!.x + lineBox!.width).toBeGreaterThanOrEqual(hunkBox!.x + hunkBox!.width - 2);
  expect(lineBox!.width).toBeGreaterThanOrEqual(scroll.width - 1);
});

test("the Owner reads a hunk in the full screen view and closes it", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/settings/memory/owner/shop");

  const item = main.locator("li").filter({ hasText: longReason });
  await item.getByRole("button", { name: "Full screen" }).click();
  const dialog = page.getByRole("dialog");
  const line = dialog.locator('[data-kind="changed"]');
  await expect(line).toBeVisible();
  const pre = dialog.locator("pre");
  expect(await pre.evaluate((el: Scrollable) => el.scrollWidth <= el.clientWidth)).toBe(true);
  const box = await line.boundingBox();
  expect(box!.height).toBeGreaterThan(40);

  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();

  await item.getByRole("button", { name: "Full screen" }).click();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
});

const wideCell = (column: number) => `column ${column} has a long cell text that is wide`;
const wideRow = (cells: string[]) => `| ${cells.join(" | ")} |`;
const wideTable = [
  wideRow([1, 2, 3, 4, 5, 6].map((column) => `heading ${column} of the wide table`)),
  wideRow([1, 2, 3, 4, 5, 6].map(() => "---")),
  wideRow([1, 2, 3, 4, 5, 6].map(wideCell)),
];
const longUrl = `https://example.com/${"abcdefghij".repeat(18)}`;
const longCommand = `docker run --rm ${"--env VARIABLE=value ".repeat(9)}image:latest`;
const longSentence = "This sentence is long and has many words. ".repeat(12).trim();
const wideText = (tail: string) =>
  text(
    "Keep this file short.",
    longSentence,
    longUrl,
    "```sh",
    longCommand,
    "```",
    ...wideTable,
    tail,
  );

const version = (id: number, reason: string, memory: string) => ({
  id,
  author: "owner",
  reason,
  revert_problem: "",
  revertible: true,
  time: `2026-09-28T0${id}:00:00Z`,
  text: memory,
});

for (const viewport of [
  { name: "desktop", width: 1280, height: 800 },
  { name: "phone", width: 390, height: 844 },
]) {
  test(`a wide memory file does not widen the page in the ${viewport.name} viewport`, async ({
    page,
  }) => {
    const main = page.getByRole("main");
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    await page.route(
      (url) => url.pathname === "/api/repositories/plants/garden/memory",
      (route) =>
        route.fulfill({
          json: {
            data: [
              version(3, longSentence, wideText("The last line changed.")),
              version(2, "The reason of this version is short.", wideText("The last line.")),
              version(1, "", text("Keep this file short.")),
            ],
          },
        }),
    );
    await page.goto("/settings/memory/plants/garden");
    await expect(main.getByText("The last line changed.").first()).toBeVisible();

    const root = await page
      .locator("html")
      .evaluate((el: Scrollable) => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }));
    expect(root.scrollWidth).toBeLessThanOrEqual(root.clientWidth);

    const content = await page
      .locator("#content")
      .evaluate((el: Scrollable) => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }));
    expect(content.scrollWidth).toBeLessThanOrEqual(content.clientWidth);

    const mainBox = await main.boundingBox();
    const currentBox = await main.locator("pre").first().boundingBox();
    expect(currentBox!.x + currentBox!.width).toBeLessThanOrEqual(mainBox!.x + mainBox!.width + 1);

    const hunk = main.locator("li").nth(1).locator("pre");
    await expect(hunk.locator(`[data-kind="added"]`, { hasText: longUrl })).toBeVisible();
    const overflow = await hunk.evaluate((el: Scrollable) => ({
      scrollWidth: el.scrollWidth,
      clientWidth: el.clientWidth,
    }));
    expect(overflow.scrollWidth).toBeGreaterThan(overflow.clientWidth);
  });
}
