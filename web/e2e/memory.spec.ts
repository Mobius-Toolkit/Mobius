import { expect, test, type Locator, type Page } from "@playwright/test";

const versionsOf = (main: Locator) =>
  main.getByRole("heading", { name: "History" }).locator("xpath=following-sibling::ul/li");

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
  await expect(plants.getByRole("link")).toHaveText(["plants/garden", "plants/mobius-skills"]);

  await plants.getByRole("link", { name: "plants/garden" }).click();
  await expect(page).toHaveURL("/settings/memory/plants/garden");
  await expect(page.getByText("Memory of plants/garden").filter({ visible: true })).toBeVisible();
});

test("the Owner edits the memory file in a dialog, sees the 200-line error, and reverts the edit", async ({
  page,
}) => {
  const main = page.locator("main");
  const dialog = page.getByRole("dialog", {
    name: "Edit Shared · plants/garden",
    includeHidden: true,
  });
  const field = dialog.getByLabel("Memory text");
  await page.goto("/settings/memory/plants/garden/shared");
  await expect(main.getByText("The memory file is empty.")).toBeVisible();
  await expect(main.getByText("The memory file has no version.")).toBeVisible();

  await main.getByRole("button", { name: "Edit" }).click();
  await expect(field).toBeEditable();
  await expect(main.getByText("Current text", { exact: true })).toBeVisible();
  await expect(main.locator("button", { hasText: "Edit" })).toBeVisible();
  await expect(main.getByText("The memory file is empty.")).toBeVisible();
  await field.fill("Water the roses in the morning.\n");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  await expect(main.getByText("Water the roses in the morning.").first()).toBeVisible();
  await expect(main.locator("ins", { hasText: "Water the roses in the morning." })).toBeVisible();
  await expect(main.getByText("owner", { exact: true })).toBeVisible();

  await main.getByRole("button", { name: "Edit" }).click();
  await expect(field).toBeEditable();
  await field.fill("Water the roses.\n".repeat(201));
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog.getByText("the maximum is 200")).toBeVisible();
  await expect(main.getByText("the maximum is 200")).toBeHidden();
  await expect(field).toHaveValue("Water the roses.\n".repeat(201));

  const discard = page.getByRole("dialog", { name: "Discard your changes?" });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(discard).toBeVisible();
  await discard.getByRole("button", { name: "Discard" }).click();
  await expect(dialog).toBeHidden();
  await expect(discard).toBeHidden();
  await expect(main.getByText("Water the roses in the morning.").first()).toBeVisible();

  await main.getByRole("button", { name: "Edit" }).click();
  await expect(field).toBeEditable();
  await expect(dialog.getByText("the maximum is 200")).toBeHidden();
  await field.fill("Water the roses at noon.\n");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  await expect(main.locator("ins", { hasText: "at noon" })).toBeVisible();
  await expect(main.getByText("A later version changed this part. Edit the file.")).toBeVisible();
  const reverts = main.getByRole("button", { name: "Revert" });
  await expect(reverts.first()).toBeEnabled();
  await expect(reverts.last()).toBeDisabled();

  await reverts.first().click();
  await expect(main.locator("del", { hasText: "at noon" })).toBeVisible();
  await expect(main.getByText("Water the roses in the morning.").first()).toBeVisible();

  await main.getByRole("button", { name: "Edit" }).click();
  await expect(field).toBeEditable();
  await field.fill("My text.\n");
  await page.evaluate(`(async () => {
    const url = "/api/repositories/plants/garden/memory/shared";
    const listed = await fetch(url).then((response) => response.json());
    await fetch(url, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: "Other text.\\n", base_version: listed.data.versions[0].id }),
    });
  })()`);
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(
    dialog.getByText("The memory file changed after the start of your edit."),
  ).toBeVisible();
  await expect(
    main.getByText("The memory file changed after the start of your edit."),
  ).toBeHidden();
  await expect(field).toHaveValue("My text.\n");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await page
    .getByRole("dialog", { name: "Discard your changes?" })
    .getByRole("button", { name: "Discard" })
    .click();
  await expect(dialog).toBeHidden();
});

// Radix adds the listeners of a dialog in an effect after the render that makes it the top layer, so wait for the open animation first.
const waitForOpen = async (dialog: Locator) => {
  await dialog.evaluate((element) => Promise.all(element.getAnimations().map((a) => a.finished)));
};

const clickOutside = async (page: Page, dialog: Locator) => {
  await waitForOpen(dialog);
  await page.mouse.click(2, 2);
};

// Closes the discard dialog without a discard, with the same kind of action that closed the edit dialog.
const closeDialogOf = async (close: string, page: Page, discard: Locator) => {
  const keep = discard.getByRole("button", { name: "Keep editing" });
  await expect(keep).toBeFocused();
  // Radix sets this style when it makes the dialog the top layer. Before that, it sends the Escape key to the layer below.
  await expect(discard).toHaveCSS("pointer-events", "auto");
  if (close === "Cancel") {
    await keep.click();
  } else if (close === "Escape") {
    await waitForOpen(discard);
    await page.keyboard.press("Escape");
  } else {
    await clickOutside(page, discard);
  }
};

for (const close of ["Cancel", "Escape", "Outside"]) {
  test(`${close} closes the edit dialog at once with no change, and asks to discard a change`, async ({
    page,
  }) => {
    const main = page.locator("main");
    const dialog = page.getByRole("dialog", {
      name: "Edit Shared · owner/shop",
      includeHidden: true,
    });
    const field = dialog.getByLabel("Memory text");
    const discard = page.getByRole("dialog", { name: "Discard your changes?" });
    const closeDialog = async () => {
      if (close === "Cancel") {
        await dialog.getByRole("button", { name: "Cancel" }).click();
      } else if (close === "Escape") {
        await page.keyboard.press("Escape");
      } else {
        await clickOutside(page, page.getByRole("dialog").last());
      }
    };
    let saves = 0;
    await page.route(
      (url) => url.pathname === "/api/repositories/owner/shop/memory/shared",
      (route) => {
        if (route.request().method() === "PUT") {
          saves++;
        }
        return route.fallback();
      },
    );
    await page.goto("/settings/memory/owner/shop/shared");
    const current = main.locator("section").first().locator("div.rounded-lg");
    await expect(current).toContainText("Run make fmt before each commit and each push.");

    await main.getByRole("button", { name: "Edit" }).click();
    await expect(field).toBeEditable();
    await closeDialog();
    await expect(dialog).toBeHidden();
    await expect(discard).toBeHidden();

    await main.getByRole("button", { name: "Edit" }).click();
    await expect(field).toBeEditable();
    const text = await field.inputValue();
    await field.fill(`${text}Another line.\n`);
    await closeDialog();
    await expect(discard).toBeVisible();
    await expect(dialog).toBeVisible();

    await closeDialogOf(close, page, discard);
    await expect(discard).toBeHidden();
    await expect(dialog).toBeVisible();
    await expect(field).toHaveValue(`${text}Another line.\n`);
    await expect(page.getByRole("dialog", { name: "Edit Shared · owner/shop" })).toBeVisible();

    await closeDialog();
    await discard.getByRole("button", { name: "Discard" }).click();
    await expect(dialog).toBeHidden();
    await expect(discard).toBeHidden();
    await expect(current).not.toContainText("Another line.");
    expect(saves).toBe(0);
  });
}

test("the edit dialog cannot close while a save runs", async ({ page }) => {
  const main = page.locator("main");
  const dialog = page.getByRole("dialog", {
    name: "Edit Shared · owner/shop",
    includeHidden: true,
  });
  const field = dialog.getByLabel("Memory text");
  const discard = page.getByRole("dialog", { name: "Discard your changes?" });
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  let saving!: () => void;
  const started = new Promise<void>((resolve) => (saving = resolve));
  await page.route(
    (url) => url.pathname === "/api/repositories/owner/shop/memory/shared",
    async (route) => {
      if (route.request().method() !== "PUT") {
        return route.fallback();
      }
      saving();
      await held;
      return route.fallback();
    },
  );
  await page.goto("/settings/memory/owner/shop/shared");
  await main.getByRole("button", { name: "Edit" }).click();
  await expect(field).toBeEditable();
  await field.fill("Water the roses.\n".repeat(201));
  await dialog.getByRole("button", { name: "Save" }).click();
  await started;

  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeDisabled();
  await page.keyboard.press("Escape");
  await clickOutside(page, dialog);
  await expect(dialog).toBeVisible();
  await expect(discard).toBeHidden();

  release();
  await expect(dialog.getByText("the maximum is 200")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeEnabled();
  await expect(field).toHaveValue("Water the roses.\n".repeat(201));
  await page.keyboard.press("Escape");
  await discard.getByRole("button", { name: "Discard" }).click();
  await expect(dialog).toBeHidden();
});

test("the edit dialog has the full width and sits at the bottom of a phone viewport", async ({
  page,
}) => {
  const main = page.locator("main");
  const dialog = page.getByRole("dialog", {
    name: "Edit Shared · owner/shop",
    includeHidden: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings/memory/owner/shop/shared");
  await main.getByRole("button", { name: "Edit" }).click();
  await expect(dialog.getByLabel("Memory text")).toBeEditable();

  await expect
    .poll(async () => {
      const box = await dialog.boundingBox();
      return { x: box!.x, width: box!.width, bottom: box!.y + box!.height, top: box!.y };
    })
    .toMatchObject({ x: 0, width: 390, bottom: 844 });
  expect((await dialog.boundingBox())!.y).toBeGreaterThanOrEqual(32);
});

const text = (...lines: string[]) => lines.map((line) => `${line}\n`).join("");

type Scrollable = { scrollWidth: number; clientWidth: number };

const longReason = "Change: the long line names the edit of a test";

test("a changed line shows its removed words and its added words, and the unchanged words have no mark", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/settings/memory/owner/shop/shared");

  const commit = main
    .getByRole("heading", { name: "History" })
    .locator("xpath=..")
    .locator("p", { hasText: "Run make fmt before each commit and each push." });
  await expect(commit.locator("ins")).toHaveText([
    "and each push",
    "Write each message in Simplified Technical English.",
  ]);
  await expect(commit.locator("del")).toHaveCount(0);

  const long = versionsOf(main).filter({ hasText: longReason });
  await expect(long.locator("del")).toHaveText(["change", "fix round"]);
  await expect(long.locator("ins")).toHaveText(["edit", "Implementer"]);
});

test("a hunk has one unchanged line above and below the change, and a separator comes between hunks", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.route(
    (url) => url.pathname === "/api/repositories/owner/shop/memory/shared",
    (route) =>
      route.fulfill({
        json: {
          data: {
            older: false,
            versions: [
              {
                id: 2,
                author: "owner",
                reason: "",
                revert_problem: "",
                revertible: true,
                time: "2026-09-28T09:00:00Z",
                text: text("a1", "a2", "a3 new", "a4", "a5", "a6", "a7", "a8 new", "a9"),
                before: text("a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"),
              },
              {
                id: 1,
                author: "owner",
                reason: "",
                revert_problem: "",
                revertible: true,
                time: "2026-09-28T08:00:00Z",
                text: text("a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"),
                before: "",
              },
            ],
          },
        },
      }),
  );
  await page.goto("/settings/memory/owner/shop/shared");

  const newest = versionsOf(main).first();
  const hunks = newest.locator("p");
  await expect(hunks).toHaveText(["a2 a3 new a4", "a7 a8 new a9"]);
  await expect(newest.getByRole("separator")).toHaveCount(1);
  await expect(hunks.first().locator("ins")).toHaveText(["new"]);
  await expect(hunks.last().locator("ins")).toHaveText(["new"]);
});

test("a memory text with a heading and a list shows as Markdown, and a hunk of an added list item marks the item", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.route(
    (url) => url.pathname === "/api/repositories/owner/shop/memory/shared",
    (route) =>
      route.fulfill({
        json: {
          data: {
            older: false,
            versions: [
              {
                id: 1,
                author: "owner",
                reason: "",
                revert_problem: "",
                revertible: true,
                time: "2026-09-28T09:00:00Z",
                text: text("## Rules", "", "- Run make fmt.", "- Use pnpm."),
                before: text("## Rules", "", "- Run make fmt."),
              },
            ],
          },
        },
      }),
  );
  await page.goto("/settings/memory/owner/shop/shared");

  const current = main.locator("section").first().locator("div.rounded-lg");
  await expect(current.getByRole("heading", { level: 2, name: "Rules" })).toBeVisible();
  await expect(current.getByRole("listitem")).toHaveText(["Run make fmt.", "Use pnpm."]);

  const added = versionsOf(main).first().locator("li", { hasText: "Use pnpm." });
  await expect(added.locator("ins")).toHaveText(["Use pnpm."]);
  await expect(versionsOf(main).first().locator("ins")).toHaveCount(1);
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

const version = (id: number, reason: string, memory: string, before: string) => ({
  id,
  author: "owner",
  reason,
  revert_problem: "",
  revertible: true,
  time: `2026-09-28T0${id}:00:00Z`,
  text: memory,
  before,
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
      (url) => url.pathname === "/api/repositories/plants/garden/memory/shared",
      (route) =>
        route.fulfill({
          json: {
            data: {
              older: false,
              versions: [
                version(
                  3,
                  longSentence,
                  wideText("The last line changed."),
                  wideText("The last line."),
                ),
                version(
                  2,
                  "The reason of this version is short.",
                  wideText("The last line."),
                  text("Keep this file short."),
                ),
                version(1, "", text("Keep this file short."), ""),
              ],
            },
          },
        }),
    );
    await page.goto("/settings/memory/plants/garden/shared");
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
    const right = mainBox!.x + mainBox!.width + 1;
    const currentBox = await main
      .locator("section")
      .first()
      .locator("div.rounded-lg")
      .boundingBox();
    expect(currentBox!.x + currentBox!.width).toBeLessThanOrEqual(right);

    const hunk = versionsOf(main).nth(1).locator("div.rounded-lg");
    await expect(hunk.locator("ins", { hasText: longUrl })).toBeVisible();
    for (const box of await hunk.all()) {
      const hunkBox = await box.boundingBox();
      expect(hunkBox!.x + hunkBox!.width).toBeLessThanOrEqual(right);
    }
  });
}

test("the Owner loads the older versions of the memory file in pages", async ({ page }) => {
  const main = page.getByRole("main");
  const url = "/api/repositories/plants/garden/memory/shared";
  const saved: number = await page.evaluate(`(async () => {
    const count = (await fetch("${url}").then((response) => response.json())).data.versions.length;
    for (let i = 1; i <= 22; i++) {
      const listed = await fetch("${url}").then((response) => response.json());
      await fetch("${url}", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: "Paged version " + i + "\\n", base_version: listed.data.versions[0].id }),
      });
    }
    return count;
  })()`);

  await page.goto("/settings/memory/plants/garden/shared");
  const items = versionsOf(main);
  const older = main.getByRole("button", { name: "Show older versions" });
  await expect(items).toHaveCount(20);
  await expect(older).toBeVisible();
  await expect(items.last().locator("del")).toHaveText("2");
  await expect(items.last().locator("ins")).toHaveText("3");

  await older.click();
  await expect(items).toHaveCount(saved + 22);
  await expect(older).toBeHidden();
  await expect(items.nth(20).locator("del")).toHaveText("1");
  await expect(items.nth(20).locator("ins")).toHaveText("2");
});

const memoryOf = async (page: Page, repository: string, file: string) =>
  page.evaluate(
    async ({ repository: repo, file: name }) => {
      const listed = await fetch(`/api/repositories/${repo}/memory/${name}`).then((response) =>
        response.json(),
      );
      return listed.data.versions[0]?.text ?? "";
    },
    { repository, file },
  );

const saveMemoryOf = async (page: Page, repository: string, file: string, value: string) => {
  await page.evaluate(
    async ({ repository: repo, file: name, text: content }) => {
      const url = `/api/repositories/${repo}/memory/${name}`;
      const listed = await fetch(url).then((response) => response.json());
      await fetch(url, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: content, base_version: listed.data.versions[0]?.id ?? 0 }),
      });
    },
    { repository, file, text: value },
  );
};

test("the file list shows each memory file with its line text", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/settings/memory/owner/shop");

  const row = (title: string) => main.getByRole("link").filter({ hasText: title });
  await expect(row("Shared")).toContainText("5 lines");
  await expect(row("Implementer")).toContainText("2 lines");
  await expect(row("Lead")).toContainText("Empty");
  await expect(main.getByRole("link")).toHaveCount(8);
});

test("a tap on a role file opens its page", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/settings/memory/owner/shop");
  await main.getByRole("link", { name: /Implementer/ }).click();

  await expect(page).toHaveURL("/settings/memory/owner/shop/implementer");
  await expect(page.getByText("Implementer · owner/shop").filter({ visible: true })).toBeVisible();
  await expect(main.getByText("Use pnpm for the frontend.").first()).toBeVisible();
  await expect(main.getByRole("heading", { name: "History" })).toBeVisible();
});

test("Back on the file page and then on the file list returns to the repository list", async ({
  page,
}) => {
  const back = page.getByRole("button", { name: "Back" }).filter({ visible: true });
  await page.goto("/settings/memory");
  await page.getByRole("link", { name: "owner/shop" }).click();
  await expect(page).toHaveURL("/settings/memory/owner/shop");
  await page.getByRole("link", { name: /Shared/ }).click();
  await expect(page).toHaveURL("/settings/memory/owner/shop/shared");

  await back.click();
  await expect(page).toHaveURL("/settings/memory/owner/shop");
  await back.click();
  await expect(page).toHaveURL("/settings/memory");
});

test("an edit of a role file saves and the shared file does not change", async ({ page }) => {
  const main = page.getByRole("main");
  const dialog = page.getByRole("dialog", {
    name: "Edit Implementer · owner/shop",
    includeHidden: true,
  });
  const original = await memoryOf(page, "owner/shop", "implementer");
  const shared = await memoryOf(page, "owner/shop", "shared");
  await page.goto("/settings/memory/owner/shop/implementer");

  await main.getByRole("button", { name: "Edit" }).click();
  const field = dialog.getByLabel("Memory text");
  await expect(field).toBeEditable();
  await field.fill("Water the roses in the morning.\n");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  await expect(main.getByText("Water the roses in the morning.").first()).toBeVisible();
  expect(await memoryOf(page, "owner/shop", "shared")).toBe(shared);

  await saveMemoryOf(page, "owner/shop", "implementer", original);
});

test("a revert on a role file page changes only that file", async ({ page }) => {
  const main = page.getByRole("main");
  const original = await memoryOf(page, "owner/shop", "implementer");
  await saveMemoryOf(page, "owner/shop", "implementer", "First line.\n");
  await saveMemoryOf(page, "owner/shop", "implementer", "First line.\nSecond line.\n");
  const shared = await memoryOf(page, "owner/shop", "shared");
  await page.goto("/settings/memory/owner/shop/implementer");

  const current = main.locator("section").first().locator("div.rounded-lg");
  await expect(current).toContainText("Second line.");
  const reverts = main.getByRole("button", { name: "Revert" });
  await expect(reverts.first()).toBeEnabled();
  await reverts.first().click();
  await expect(current).toContainText("First line.");
  await expect(current).not.toContainText("Second line.");
  expect(await memoryOf(page, "owner/shop", "shared")).toBe(shared);

  await saveMemoryOf(page, "owner/shop", "implementer", original);
});
