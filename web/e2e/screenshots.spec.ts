import { expect, test, type Locator, type Page } from "@playwright/test";
import { png } from "./images.js";

const viewports = {
  desktop: { width: 1280, height: 800 },
  phone: { width: 390, height: 844 },
};

// Writes screenshots/<name>-desktop.png and screenshots/<name>-phone.png.
async function screenshot(
  page: Page,
  name: string,
  path: string,
  ready: (device: string) => Locator | Locator[],
  open?: (device: string) => Promise<void>,
) {
  for (const [device, size] of Object.entries(viewports)) {
    await page.setViewportSize(size);
    await page.goto(path);
    await open?.(device);
    for (const locator of [ready(device)].flat()) {
      await expect(locator).toBeVisible();
    }
    await page.screenshot({
      path: `screenshots/${name}-${device}.png`,
      animations: "disabled",
    });
  }
}

// The tests have no DOM types, so the check is a script.
const wide = (selector: string) =>
  `(document.querySelector('${selector}')?.scrollWidth ?? 0) > (document.querySelector('${selector}')?.clientWidth ?? 0)`;

test("screenshots", async ({ page }) => {
  const main = page.getByRole("main");
  await page.addInitScript(`window.SpeechRecognition = class extends EventTarget {
    start() {}
    stop() {}
    abort() {}
  }`);

  await screenshot(page, "login", "/github", () => page.getByLabel("Access password"));
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
  await screenshot(page, "github-connect", "/github", () => page.getByLabel("App name"));

  const createApp = async (account: string, slug: string) => {
    await page.getByLabel("Account or organization").fill(account);
    await page.getByLabel("App name").fill(`Mobius ${account}`);
    await page.getByRole("button", { name: "Create the App" }).click();
    await expect(page.getByText(`Install ${slug} on your repositories`)).toBeVisible();
  };
  await createApp("owner", "mobius-test");
  const release = page.getByText("v0.1.4").filter({ visible: true }).first();
  // The server adds the repositories after the second App, so Mobius knows no organization.
  await screenshot(page, "new-workstream-no-organization", "/workstreams/new", () => [
    release,
    main.getByText("Mobius reads the repositories from GitHub."),
  ]);
  await page.goto("/github");
  await createApp("plants", "mobius-second");
  // The organization switch shows when the poll has the repositories of both Apps.
  await expect(async () => {
    await page.reload();
    await expect(page.getByRole("button", { name: "owner" })).toBeVisible({
      timeout: 1000,
    });
  }).toPass();

  await page.goto("/");
  await expect(page).toHaveURL("/workstreams");
  // The side bar and the header of the phone have the same controls, and only one of them shows.
  const shown = (name: string) => page.getByRole("button", { name }).filter({ visible: true });
  const drain = page.getByText("Upgrade waits for 2 agents").filter({ visible: true });
  // The server starts the drain after the chats of the fake agents end.
  await expect(drain).toBeVisible({ timeout: 60_000 });
  // The frame shows its data after the live connection opens. Only the side bar of a desktop shows the Workstreams.
  const frame = (device: string, upgrade: Locator) => [
    upgrade,
    page.getByLabel("Work in another organization").filter({ visible: true }),
    page.locator('a[href="/inbox"]').filter({ visible: true }).getByText("2", { exact: true }),
    ...(device === "desktop" ? [page.locator("nav").first().getByText("needs you")] : []),
  ];
  await screenshot(page, "workstreams", "/workstreams", (device) => [
    ...frame(device, drain),
    main.getByText("Seasonal prices"),
    main.getByText("done"),
    main.getByText("needs you"),
  ]);
  const chatReady = (device: string) => [
    ...frame(device, drain),
    main.getByText("#42 and #45 wait for your decision."),
    main.getByRole("link", { name: "PR #44" }),
    ...(device === "desktop"
      ? [page.getByRole("complementary").getByText("Lead chat session").first()]
      : []),
  ];
  await screenshot(page, "chat", "/workstreams/owner/shop/12", chatReady);
  await screenshot(
    page,
    "chat-listening",
    "/workstreams/owner/shop/12",
    (device) => [...chatReady(device), main.getByRole("button", { name: "Stop voice input" })],
    () => main.getByRole("button", { name: "Start voice input" }).click(),
  );
  const photos = [
    { name: "plan.png", mimeType: "image/png", buffer: await png(page, 200, 150, "#2563eb") },
    { name: "cart.png", mimeType: "image/png", buffer: await png(page, 200, 150, "#16a34a") },
  ];
  await screenshot(
    page,
    "chat-images",
    "/workstreams/owner/shop/12",
    (device) => [
      ...chatReady(device),
      main.getByRole("img", { name: "Image 1" }),
      main.getByRole("img", { name: "Image 2" }),
    ],
    async () => {
      await main.getByLabel("Message to the Lead").fill("This is the new plan page.");
      await main.locator("input[type=file]").setInputFiles(photos);
      for (const name of ["Image 1", "Image 2"]) {
        await expect(main.getByRole("img", { name })).toHaveJSProperty("naturalWidth", 200);
      }
    },
  );
  await screenshot(
    page,
    "chat-tasks",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain),
      page.getByText("#45 Pick the plan limits").filter({ visible: true }),
      page.getByText("waits for CI").filter({ visible: true }),
      page.getByText("waits for Lead").filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await main.getByRole("button", { name: "Agents" }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
    },
  );
  // The Inbox of the organization plants has no item, so the frame has no Inbox count.
  await screenshot(
    page,
    "chat-tasks-start",
    "/workstreams/plants/garden/19",
    (device) => [
      drain,
      page.getByLabel("Work in another organization").filter({ visible: true }),
      ...(device === "desktop"
        ? [page.locator('nav a[href="/workstreams/plants/garden/19"]')]
        : []),
      page.getByRole("button", { name: "Start #70" }).filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await main.getByRole("button", { name: "Agents" }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
    },
  );
  await screenshot(page, "chat-all-tasks-closed", "/workstreams/owner/shop/13", (device) => [
    ...frame(device, drain),
    main.getByText("All tasks are closed."),
    main.getByText("Change the prices for each season."),
  ]);
  await screenshot(page, "new-workstream", "/workstreams/new", (device) => [
    ...frame(device, drain),
    main.getByText("Sell gift cards in the shop."),
  ]);
  await screenshot(page, "inbox", "/inbox", (device) => [
    ...frame(device, drain),
    main.getByText("#45 needs a decision"),
    main.getByText("Integrate loyalty plans ·"),
    main.getByText("claude-code reached a usage limit."),
  ]);
  await screenshot(page, "activity", "/activity", (device) => [
    ...frame(device, drain),
    main.getByText('Dispatched "Pick the plan limits"'),
    main.getByRole("button", { name: "Integrate loyalty plans" }),
  ]);

  // The chat shows its last message, and the tree hides a stopped agent unless an agent below it runs.
  await page.setViewportSize(viewports.desktop);
  await page.goto("/workstreams/owner/shop/12");
  await expect(main.getByText("#42 and #45 wait for your decision.")).toBeInViewport();
  const tree = page.getByRole("complementary");
  const stopped = tree.getByText("stopped", { exact: true });
  await expect(stopped).toHaveCount(1);
  await tree.getByRole("switch", { name: "Show stopped agents" }).click();
  await expect(stopped).toHaveCount(2);

  // A wide message scrolls inside the message. The page does not scroll to the side.
  await page.setViewportSize({ width: 375, height: 667 });
  await page.goto("/workstreams/owner/shop/12");
  await expect(main.getByText("What is the state of the plans?")).toBeVisible();
  expect(
    await page.evaluate(
      `${wide("main pre")} && ${wide("main table")} && document.documentElement.scrollWidth <= window.innerWidth`,
    ),
  ).toBe(true);
  await screenshot(page, "agents", "/agents", (device) => [
    ...frame(device, drain),
    main.getByText("Mobius prepares an upgrade"),
  ]);
  await screenshot(
    page,
    "transcript",
    "/agents",
    (device) => [...frame(device, drain), main.getByText("The plan prices are in cents now.")],
    () => main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click(),
  );

  await shown("Cancel upgrade").click();
  await screenshot(
    page,
    "upgrade",
    "/workstreams",
    (device) => [
      ...frame(device, release),
      page.getByText("Show the release changes in a modal before the upgrade (#320)"),
    ],
    () => shown("Upgrade v0.1.4").click(),
  );
  await page.route("/ui-version", (route) => route.fulfill({ body: "a new build" }));
  await screenshot(page, "new-version", "/workstreams", (device) => [
    ...frame(device, release),
    shown("New version"),
  ]);
  await page.unroute("/ui-version");

  await screenshot(page, "settings", "/settings", (device) => [
    ...frame(device, release),
    main.getByRole("link", { name: "Devices" }),
  ]);
  await screenshot(
    page,
    "organizations",
    "/settings",
    (device) => [...frame(device, release), page.getByRole("menuitemradio", { name: "plants" })],
    () => page.getByRole("button", { name: "owner" }).click(),
  );
  await screenshot(page, "github", "/github", (device) => [
    ...frame(device, release),
    main.getByText("plants/garden"),
  ]);
  await screenshot(page, "devices", "/devices", (device) => [
    ...frame(device, release),
    main.getByText("This device"),
  ]);
  await screenshot(page, "checkup", "/settings/checkup", (device) => [
    ...frame(device, release),
    main.getByRole("link", { name: "Tools" }),
    main.getByRole("heading", { name: "owner", exact: true }),
    main.getByRole("heading", { name: "plants", exact: true }),
  ]);
  await screenshot(page, "checkup-tools", "/settings/checkup/tools", (device) => [
    ...frame(device, release),
    main.getByText("2.1.284 (Claude Code)"),
  ]);
  await screenshot(page, "checkup-permissions", "/settings/checkup/owner/permissions", (device) => [
    ...frame(device, release),
    main.getByText("workflows: write"),
  ]);
  await screenshot(page, "checkup-labels", "/settings/checkup/owner/labels", (device) => [
    ...frame(device, release),
    main.getByText("wrong color: #ededed"),
  ]);

  // The note closes a Workstream whose tasks are all closed.
  await page.setViewportSize(viewports.desktop);
  await page.goto("/workstreams/owner/shop/13");
  await main.getByRole("button", { name: "Close Workstream" }).click();
  await expect(page).toHaveURL("/workstreams");
  await expect(main.getByText("Seasonal prices")).toBeHidden();

  // Opening a chat of plants saves plants as the organization, so this screenshot comes last.
  const pictures = main.getByRole("img", { name: /^Picture \d of message/ });
  await screenshot(
    page,
    "chat-history-images",
    "/workstreams/plants/garden/25",
    (device) => [
      release,
      page.getByLabel("Work in another organization").filter({ visible: true }),
      ...(device === "desktop"
        ? [page.locator('nav a[href="/workstreams/plants/garden/25"]')]
        : []),
      main.getByText("This is the new plan page."),
      pictures.last(),
    ],
    async () => {
      await expect(pictures).toHaveCount(3);
      for (let position = 0; position < 3; position++) {
        await expect(pictures.nth(position)).toHaveJSProperty("naturalWidth", 200);
      }
      await expect(pictures.last()).toBeInViewport();
    },
  );
});
