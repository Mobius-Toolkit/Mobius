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
  close?: () => Promise<void>,
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
    await close?.();
  }
}

// Sets the queue reason of the live Implementer session of #41.
const setQueueReason = async (page: Page, reason: string) => {
  const response = await page.request.put(
    `/e2e/agents/41/queue-reason?reason=${encodeURIComponent(reason)}`,
  );
  expect(response.ok()).toBe(true);
};

const queueReasons = [
  "runs .mobius/check",
  "waits for a check slot",
  "waits for a low load",
  /paused until Sep 28, 12:00\sPM/,
  "no free Implementer slot (2/2)",
];

// The tests have no DOM types, so the check is a script.
const wide = (selector: string) =>
  `(document.querySelector('${selector}')?.scrollWidth ?? 0) > (document.querySelector('${selector}')?.clientWidth ?? 0)`;

test("screenshots", async ({ page }) => {
  const main = page.getByRole("main");
  // The day separator shows the year of a message that is not in the current year.
  await page.clock.setFixedTime("2026-10-15T12:00:00Z");
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
  await screenshot(page, "chat-no-organization", "/chat", (device) => [
    device === "desktop" ? release : page.getByLabel("Upgrade available"),
    main.getByText("Mobius reads the repositories from GitHub."),
  ]);
  await page.setViewportSize(viewports.desktop);
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
  await expect(page).toHaveURL("/chat");
  // The side bar of a desktop and the Settings page of a phone have the same upgrade controls, and only one of them shows.
  const shown = (name: string) => page.getByRole("button", { name }).filter({ visible: true });
  const drain = page.getByText("Upgrade waits for 2 agents").filter({ visible: true });
  // The server starts the drain after the chats of the fake agents end.
  await expect(drain).toBeVisible({ timeout: 60_000 });
  // The frame shows its data after the live connection opens. Only the side bar of a desktop shows the Workstreams.
  // A phone shows the upgrade controls only on the Settings page, and the organization switch only on the Workstreams
  // and Inbox pages.
  const frame = (
    device: string,
    upgrade: Locator,
    phone: { upgrade?: boolean; organizations?: boolean } = {},
  ) => {
    const desktop = device === "desktop";
    return [
      ...(desktop || phone.upgrade ? [upgrade] : []),
      ...(desktop ? [] : [page.getByLabel("Upgrade available")]),
      ...(desktop || phone.organizations
        ? [page.getByLabel("Work in another organization").filter({ visible: true })]
        : []),
      page
        .locator('nav a[href="/inbox"]')
        .filter({ visible: true })
        .getByText("2", { exact: true }),
      ...(desktop ? [page.locator("nav").first().getByText("needs you")] : []),
    ];
  };
  // Each test that opens the Triager chat of owner marks its messages as seen. Thus the screenshots get a fixed count.
  await page.route("/api/unread", async (route) => {
    const response = await route.fetch();
    const { data } = (await response.json()) as { data: { organization: string }[] };
    const others = data.filter((chat) => chat.organization !== "owner");
    await route.fulfill({
      json: {
        data: [...others, { organization: "owner", repository: "", workstream: 0, count: 1 }],
      },
    });
  });
  await page.route("/api/chat/seen", (route) => route.fulfill({ status: 204 }));
  const chatCount = page
    .locator("nav a[href='/chat']")
    .filter({ visible: true })
    .getByText("1", { exact: true });
  const workstreamsReady = (device: string) => [
    chatCount,
    ...frame(device, drain, { organizations: true }),
    main.getByText("Seasonal prices"),
    main.getByRole("img", { name: "Autopilot" }),
    main.getByRole("img", { name: "Agent running" }),
    main.getByRole("img", { name: "Ready to merge" }),
    main.getByText("done"),
    main.getByText("needs you"),
  ];
  await screenshot(page, "workstreams", "/workstreams", workstreamsReady);
  // The answer 502 is not an event stream, so the page shows Connecting… and tries again each 3 s.
  await screenshot(
    page,
    "connecting",
    "/workstreams",
    (device) => [
      ...workstreamsReady(device),
      page.getByRole("status").filter({ hasText: "Connecting…" }),
    ],
    async (device) => {
      for (const locator of workstreamsReady(device)) {
        await expect(locator).toBeVisible();
      }
      await page.route("/api/events", (route) => route.fulfill({ status: 502 }));
      await page.evaluate("window.dispatchEvent(new Event('online'))");
    },
    () => page.unroute("/api/events"),
  );
  const chatReady = (device: string) => [
    ...frame(device, drain),
    main.getByText("#42 and #45 wait for your decision."),
    main.getByRole("link", { name: "PR #44" }),
    main.getByRole("link", { name: "Answer" }),
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
  await screenshot(
    page,
    "chat-typing",
    "/workstreams/owner/shop/12",
    (device) => [...chatReady(device), main.getByRole("button", { name: "Send", exact: true })],
    () => main.getByLabel("Message to the Lead").fill("Show the prices of the roses first."),
  );
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { writing: boolean } };
    body.data.writing = true;
    await route.fulfill({ response, json: body });
  });
  await screenshot(page, "chat-writing", "/workstreams/owner/shop/12", (device) => [
    ...chatReady(device),
    main.getByRole("button", { name: "Stop the reply" }),
  ]);
  await page.unroute("**/api/chat?*");
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
  await setQueueReason(page, "waits for a low load");
  await screenshot(
    page,
    "chat-agents",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain),
      page
        .getByText(/Sep \d+, \d\d:\d\d [AP]M · Mobius prepares an upgrade/)
        .filter({ visible: true }),
      ...queueReasons.map((reason) => page.getByText(reason).filter({ visible: true })),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Agents" }).click();
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
        await page.getByRole("banner").getByRole("button", { name: "Agents" }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
    },
  );
  await screenshot(
    page,
    "chat-tasks-closed",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain),
      page.getByText("#45 Pick the plan limits").filter({ visible: true }),
      page.getByText("waits for CI").filter({ visible: true }),
      page.getByText("waits for Lead").filter({ visible: true }),
      page.getByText("#36 Rename the plan table").filter({ visible: true }),
      page.getByText("#37 Remove the old plan page").filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Agents" }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
      await page.getByLabel("Show closed tasks").filter({ visible: true }).click();
    },
  );
  // The Inbox of the organization plants has no item, so the frame has no Inbox count.
  await screenshot(
    page,
    "chat-tasks-start",
    "/workstreams/plants/garden/19",
    (device) => [
      ...(device === "desktop"
        ? [
            drain,
            page.getByLabel("Work in another organization"),
            page.locator('nav a[href="/workstreams/plants/garden/19"]'),
          ]
        : [page.getByLabel("Upgrade available")]),
      page.getByRole("button", { name: "Start #70" }).filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Agents" }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
    },
  );
  await screenshot(page, "chat-all-tasks-closed", "/workstreams/owner/shop/13", (device) => [
    ...frame(device, drain),
    main.getByText("All tasks are closed."),
    main.getByText("Change the prices for each season."),
  ]);
  await screenshot(page, "chat-triager", "/chat", (device) => [
    chatCount,
    ...frame(device, drain),
    main.getByText("Sell gift cards in the shop."),
  ]);
  await screenshot(page, "inbox", "/inbox", (device) => [
    chatCount,
    ...frame(device, drain, { organizations: true }),
    main.getByRole("tab", { name: "To do 2" }),
    main.getByText("#45 needs a decision"),
    main.getByText("Integrate loyalty plans ·"),
    main.getByText("antigravity reached a usage limit."),
    main.getByText(/· paused until Sep 28, 12:00\sPM/),
  ]);
  await screenshot(page, "inbox-activity", "/inbox/activity", (device) => [
    chatCount,
    ...frame(device, drain, { organizations: true }),
    main.getByText('Dispatched "Pick the plan limits"'),
    main.getByRole("button", { name: "Integrate loyalty plans" }),
  ]);
  await page.unroute("/api/unread");
  await page.unroute("/api/chat/seen");

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
  await setQueueReason(page, "");
  await screenshot(
    page,
    "transcript-panel",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain),
      page.getByText("The plan prices are in cents now.").filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Agents" }).click();
      }
      await page
        .getByRole("button", { name: /^implementer devin · swe-1.5 · Sep \d+, \d\d:\d\d [AP]M$/ })
        .filter({ visible: true })
        .click();
    },
  );
  await setQueueReason(page, "waits for a low load");
  await screenshot(page, "agents", "/agents", (device) => [
    ...frame(device, drain),
    main.getByText(/Sep \d+, \d\d:\d\d [AP]M · Mobius prepares an upgrade/),
    ...queueReasons.map((reason) => main.getByText(reason)),
  ]);
  await setQueueReason(page, "");
  await screenshot(
    page,
    "transcript",
    "/agents",
    (device) => [...frame(device, drain), main.getByText("The plan prices are in cents now.")],
    () => main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click(),
  );

  await setQueueReason(page, "waits for a low load");
  await screenshot(
    page,
    "transcript-start-check",
    "/agents",
    (device) => [
      ...frame(device, drain),
      main.getByRole("button", { name: "Start the check now" }),
    ],
    () => main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click(),
  );
  await setQueueReason(page, "");

  await page.setViewportSize(viewports.desktop);
  await shown("Cancel upgrade").click();
  await screenshot(
    page,
    "upgrade",
    "/settings",
    (device) => [
      ...frame(device, release, { upgrade: true }),
      page.getByText("Show the release changes in a modal before the upgrade (#320)"),
    ],
    () => shown("Upgrade v0.1.4").click(),
  );
  await page.route("/ui-version", (route) => route.fulfill({ body: "a new build" }));
  await screenshot(page, "new-version", "/settings", (device) => [
    ...frame(device, release, { upgrade: true }),
    shown("New version"),
  ]);
  await page.unroute("/ui-version");

  await screenshot(page, "settings", "/settings", (device) => [
    ...frame(device, release, { upgrade: true }),
    main.getByRole("link", { name: "Devices" }),
  ]);
  await screenshot(
    page,
    "organizations",
    "/workstreams",
    (device) => [
      ...frame(device, release, { organizations: true }),
      page.getByRole("menuitemradio", { name: "plants" }),
    ],
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
  const logOut = main
    .getByRole("listitem")
    .filter({ hasText: "This device" })
    .getByRole("button", { name: "Log out" });
  // The route holds the request until the screenshot is done, and then it answers with an error, so the server keeps
  // the login.
  let endLogOut!: () => void;
  await screenshot(
    page,
    "devices-log-out",
    "/devices",
    (device) => [...frame(device, release), logOut.locator('[data-slot="spinner"]')],
    async () => {
      const ends = new Promise<void>((resolve) => (endLogOut = resolve));
      await page.route("**/api/devices/*", async (route) => {
        await ends;
        await route.fulfill({ status: 500, json: { error: "The server failed." } });
      });
      await logOut.click();
    },
    async () => {
      endLogOut();
      await expect(logOut).toBeEnabled();
      await page.unroute("**/api/devices/*");
    },
  );
  await screenshot(page, "checkup", "/settings/checkup", (device) => [
    ...frame(device, release),
    main.getByRole("link", { name: "Tools" }),
    main.getByRole("heading", { name: "owner", exact: true }),
    main.getByRole("heading", { name: "plants", exact: true }),
    main.getByText("needs you").nth(3),
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
  await screenshot(page, "memory-repositories", "/settings/memory", (device) => [
    ...frame(device, release),
    main.getByRole("link", { name: "owner/shop" }),
  ]);
  await screenshot(page, "memory", "/settings/memory/owner/shop", (device) => [
    ...frame(device, release),
    page.getByText("Memory of owner/shop").filter({ visible: true }),
    main.getByText("+ Run make fmt before each commit and each push."),
    main.getByText("+ Write each message in Simplified Technical English."),
    main.getByText("- Run make fmt before each commit."),
    main.getByText("+ Wait for a condition with testkit.WaitFor."),
    main.getByText("A later version changed this part. Edit the file."),
    main.getByText("Add: three fix rounds repeated the same wait with a fixed sleep in tests"),
  ]);
  // The 7 days that end on the fixed date have the turns of three models in two repositories. devin has no cost.
  await screenshot(page, "usage", "/usage?group=model", (device) => [
    ...frame(device, release),
    main.getByRole("tab", { name: "Cost", selected: true }),
    main.getByRole("row", { name: /claude-opus-5-5 \d 23,000 6,400 330,000 37,000 \$5\.55$/ }),
    main.getByRole("row", { name: /swe-1\.5 3 .* —$/ }),
    main.locator(".recharts-bar-rectangle").first(),
    main.getByText("Oct 15", { exact: true }),
  ]);
  await screenshot(
    page,
    "usage-tokens",
    "/usage?tab=tokens&group=harness&role=%5B%22implementer%22%5D",
    (device) => [
      ...frame(device, release),
      main.getByRole("tab", { name: "Tokens", selected: true }),
      main.getByRole("row", { name: /devin 3 49,000 12,100 156,000 17,000 —$/ }),
      main.locator(".recharts-bar-rectangle").first(),
    ],
  );
  await page.setViewportSize(viewports.phone);
  await page.goto("/usage?group=model");
  await expect(main.getByRole("row", { name: /swe-1\.5/ })).toBeVisible();
  expect(await page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")).toBe(
    true,
  );

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
      ...(device === "desktop"
        ? [
            release,
            page.getByLabel("Work in another organization"),
            page.locator('nav a[href="/workstreams/plants/garden/25"]'),
          ]
        : [page.getByLabel("Upgrade available")]),
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
