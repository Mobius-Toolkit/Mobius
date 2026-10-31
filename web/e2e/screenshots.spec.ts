import { expect, test, type Locator, type Page } from "@playwright/test";
import {
  ask,
  askDraft,
  askStore,
  briefTitle,
  cardOf,
  checksQuestion,
  createTitle,
  draftQuestion,
  draftWorkstream,
  dropColumn,
  editBrief,
  idle,
  leadTitle,
  leadToLeadTitle,
  messageLeadOfTwelve,
  restoreBrief,
  seen,
  sendToLead,
  settle,
  stopUnknown,
  storeQuestion,
  unknownTitle,
  waitText,
  withdrawDraft,
} from "./actions.js";
import { waitForEvergreen } from "./evergreen.js";
import { png } from "./images.js";
import { longTaskTitle, routeLongTask, tasksUrl } from "./long-task.js";
import { countSamples, fakeMicrophone } from "./microphone.js";
import { lines, turnAllOn, turnOff } from "./switches.js";
import { boardTasks, mergingTask, refusedTask, routeTasks, tasksRoute } from "./tasks.js";
import {
  inboxRoute,
  coordinatorActionItem,
  coordinatorFailedItem,
  coordinatorQuestion,
  leadActionItem,
  longTitle,
  longTitleItem,
  longWordQuestionItem,
  longWordTitle,
  routeInboxItems,
  stoppedItems,
  todoItems,
} from "./stops.js";

const viewports = {
  desktop: { width: 1280, height: 800 },
  phone: { width: 390, height: 844 },
};

// Writes screenshots/<name>-desktop.png and screenshots/<name>-phone.png.
// The phone image comes from a browser with a coarse pointer, as on a real phone.
async function screenshot(
  page: Page,
  name: string,
  path: string,
  ready: (device: string) => Locator | Locator[],
  open?: (device: string) => Promise<void>,
  close?: () => Promise<void>,
) {
  const session = await page.context().newCDPSession(page);
  for (const [device, size] of Object.entries(viewports)) {
    await session.send("Emulation.setTouchEmulationEnabled", { enabled: device === "phone" });
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
  await session.send("Emulation.setTouchEmulationEnabled", { enabled: false });
  await session.detach();
}

const trayToggle = (page: Page, count: number, expanded: boolean) =>
  page.getByRole("button", { name: `Waits for you (${count})`, expanded });

// The desktop image and the phone image share the browser storage, so each step sets the choice before it opens the page.
const setTray = (page: Page, choice: "collapsed" | "expanded") =>
  page.evaluate((value) => localStorage.setItem("tray", value), choice);

const fixedTime = new Date("2026-10-15T12:00:00Z");

const noticeOf = (page: Page, text: string) => page.getByRole("status").filter({ hasText: text });

// The page clock stops 0.5 s after the fixed time, so the notice stays open for the screenshot. The close step lets the
// clock run again and sets the fixed time of the test again.
const holdNotice = async (page: Page, notice: Locator) => {
  await expect(notice).toBeVisible();
  await page.clock.pauseAt(new Date(fixedTime.getTime() + 500));
};

const releaseNotice = async (page: Page) => {
  await page.clock.resume();
  await page.clock.setFixedTime(fixedTime);
};

// A notice closes 4 s after it opens. The clock jumps past it, so that the screenshot shows no notice.
const skipNotice = async (page: Page, notice: Locator) => {
  await expect(notice).toBeVisible();
  await page.clock.fastForward(5000);
  await expect(notice).toBeHidden();
};

// The chat scrolls to its end by itself, and the end is the only stable scroll.
const showChatEnd = (page: Page) =>
  expect
    .poll(() =>
      page
        .locator("[data-message]")
        .last()
        .evaluate((el) => {
          const list = el.parentElement;
          if (!list) {
            return false;
          }
          list.scrollTop = list.scrollHeight;
          return list.scrollHeight - list.scrollTop - list.clientHeight < 1;
        }),
    )
    .toBe(true);

// Sets the queue reason of the live Implementer session of #41.
const setQueueReason = async (page: Page, reason: string) => {
  const response = await page.request.put(
    `/e2e/agents/41/queue-reason?reason=${encodeURIComponent(reason)}`,
  );
  expect(response.ok()).toBe(true);
};

// The engine adds a record with the real time to the chat for each action of the Owner.
const fixRecordTimes = async (page: Page) => {
  expect((await page.request.post("/e2e/approvals/times/fix")).ok()).toBe(true);
};

const queueReasons = [
  "runs .mobius/check",
  "waits for a check slot",
  "waits for a low load",
  /· paused until Sep 28, 12:00\sPM/,
  "no free Implementer slot (2/2)",
];

// A log that waits for its stream shows Connecting…, and the stream is open when the log has its first entries.
const logConnected = async (page: Page) => {
  await expect(
    page.getByText("The plan prices are in cents now.").filter({ visible: true }),
  ).toBeVisible();
  await expect(page.getByText("Connecting…").filter({ visible: true })).toBeHidden();
};

const decide = async (page: Page, panel: Locator, decision: "approve" | "reject") => {
  const answered = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new RegExp(`/api/actions/\\d+/${decision}$`).test(response.url()),
  );
  await panel.getByRole("button", { name: decision === "approve" ? "Approve" : "Reject" }).click();
  expect((await answered).ok()).toBe(true);
};

// The tests have no DOM types, so the check is a script.
const storedDrafts = `new Promise((resolve) => {
  const open = indexedDB.open("mobius");
  open.onsuccess = () => {
    const count = open.result.transaction("drafts").objectStore("drafts").count();
    count.onsuccess = () => resolve(count.result);
  };
})`;

const storedAudio = `new Promise((resolve) => {
  const open = indexedDB.open("mobius");
  open.onsuccess = () => {
    const count = open.result.transaction("voice").objectStore("voice").count();
    count.onsuccess = () => resolve(count.result);
  };
})`;

const removedText = (scope: Locator) => scope.locator("del");
const removedSwitch = (scope: Locator) => scope.getByRole("switch", { name: "Show removed text" });
const addedText = (scope: Locator) => scope.locator("ins");

const wide = (selector: string) =>
  `(document.querySelector('${selector}')?.scrollWidth ?? 0) > (document.querySelector('${selector}')?.clientWidth ?? 0)`;

test("screenshots", async ({ page }) => {
  test.setTimeout(300_000);
  const main = page.getByRole("main");
  // The day separator shows the year of a message that is not in the current year.
  await page.clock.setFixedTime(fixedTime);
  await page.addInitScript(fakeMicrophone);
  await page.addInitScript(countSamples);

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
  await waitForEvergreen(page);
  // The frame shows its data after the live connection opens. Only the side bar of a desktop shows the Workstreams.
  // A phone shows the upgrade controls only on the Settings page, and the organization switch only on the Workstreams
  // and Inbox pages.
  const frame = (
    device: string,
    upgrade: Locator,
    phone: { upgrade?: boolean; organizations?: boolean } = {},
    inboxCount = "1",
  ) => {
    const desktop = device === "desktop";
    return [
      ...(desktop || phone.upgrade ? [upgrade] : []),
      // The Settings page shows the voice language after it reads it.
      ...(phone.upgrade
        ? [page.locator('main a[href="/settings/voice"]').getByText("English")]
        : []),
      ...(desktop ? [] : [page.getByLabel("Upgrade available")]),
      ...(desktop || phone.organizations
        ? [page.getByLabel("Work in another organization").filter({ visible: true })]
        : []),
      page
        .locator('nav a[href="/inbox"]')
        .filter({ visible: true })
        .getByText(inboxCount, { exact: true }),
      page.getByText(/^antigravity paused until Sep 28, 12:00\sPM$/),
      ...(desktop
        ? [
            page.locator("nav").first().getByText("needs you"),
            page.locator("nav").first().getByText("Early renewals"),
          ]
        : []),
    ];
  };
  // Each test that opens the Coordinator chat of owner marks its messages as seen. Thus the screenshots get a fixed count.
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
    main.getByRole("heading", { name: "Evergreen" }),
    main.getByText("Seasonal prices"),
    main.getByRole("img", { name: "Autopilot: Start tasks" }),
    main.getByRole("img", { name: "Agent running" }),
    main.getByRole("img", { name: "Ready to merge" }),
    main.getByText("done"),
    main.getByText("needs you"),
  ];
  // The route gives two Workstreams the merge levels. The step after the "workstreams" screenshot removes it.
  const levelsRoute = "**/api/workstreams";
  await page.route(levelsRoute, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data: { repository: string; number: number; autopilot: string }[];
    };
    for (const workstream of body.data) {
      if (workstream.repository === "owner/shop" && workstream.number === 12) {
        workstream.autopilot = "merge";
      }
      if (workstream.repository === "owner/shop" && workstream.number === 13) {
        workstream.autopilot = "start_and_merge";
      }
    }
    await route.fulfill({ response, json: body });
  });
  await screenshot(page, "workstreams", "/workstreams", (device) => [
    ...workstreamsReady(device),
    main.getByRole("img", { name: "Autopilot: Merge only" }),
    main.getByRole("img", { name: "Autopilot: Start and merge" }),
  ]);
  await page.unroute(levelsRoute);
  await routeTasks(page, boardTasks);
  await screenshot(page, "tasks", "/tasks", (device) => [
    chatCount,
    ...frame(device, drain, { organizations: true }),
    main.getByRole("region", { name: "Queued" }).getByText("Add a retry to the order sync"),
    main.getByRole("link", { name: "PR #44" }),
    main.getByText("fix 1 · review 2"),
    main.getByRole("region", { name: "Working" }),
  ]);
  await page.unroute(tasksRoute);
  await routeTasks(page, [...boardTasks, mergingTask, refusedTask]);
  await screenshot(
    page,
    "tasks-merging",
    "/tasks",
    (device) => [
      chatCount,
      ...frame(device, drain, { organizations: true }),
      main.getByRole("region", { name: "Waiting" }).getByText("merging", { exact: true }),
      main.getByText("Required status check is expected."),
    ],
    async () => {
      const waiting = main.getByRole("region", { name: "Waiting" });
      await expect(waiting.getByText("merging", { exact: true })).toBeAttached();
      await expect(main.getByText("Required status check is expected.")).toBeAttached();
      await waiting.evaluate((el) => el.scrollIntoView({ block: "nearest", inline: "start" }));
    },
  );
  await page.unroute(tasksRoute);
  // The open menu hides the other elements from the accessibility tree, so the open step waits for them.
  await screenshot(
    page,
    "organization-menu",
    "/workstreams",
    () => page.getByRole("menuitemradio", { name: "plants" }),
    async (device) => {
      for (const locator of workstreamsReady(device)) {
        await expect(locator).toBeVisible();
      }
      await shown("owner").click();
    },
    () => page.keyboard.press("Escape"),
  );
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
  // The server gives one Inbox item to the chat of owner/shop#12. The route adds the stopped issues #41 and #42.
  await routeInboxItems(page, stoppedItems);
  const trayRow = main
    .getByRole("listitem")
    .filter({ hasText: "Add plan model" })
    .filter({ hasText: "The agent asked a question" });
  const chatReady = (device: string) => [
    ...frame(device, drain, {}, "3"),
    main.getByText("#42 and #45 wait for your decision."),
    trayRow,
    main.getByRole("button", { name: "Answer" }).first(),
    page.getByRole("button", { name: "Autopilot: Off" }).filter({ visible: true }),
    ...(device === "desktop"
      ? [page.getByRole("complementary").getByText("#45 Pick the plan limits")]
      : []),
  ];
  // A phone hides the tray while the message field has the focus.
  const typingReady = (device: string) =>
    chatReady(device).filter((locator) => device === "desktop" || locator !== trayRow);
  await screenshot(page, "chat", "/workstreams/owner/shop/12", chatReady);
  await page.route("**/api/inbox/201/answer", (route) =>
    route.fulfill({
      json: { data: { url: "https://github.com/owner/shop/issues/41#issuecomment-1" } },
    }),
  );
  const answered = noticeOf(page, "Sent the answer to the Lead of #12");
  await screenshot(
    page,
    "notice-answer",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain, {}, "2"),
      main.getByText("#42 and #45 wait for your decision."),
      page.getByRole("button", { name: "Autopilot: Off" }).filter({ visible: true }),
      answered,
    ],
    async (device) => {
      for (const locator of chatReady(device)) {
        await expect(locator).toBeVisible();
      }
      await trayRow.getByRole("button", { name: "Answer" }).click();
      await page
        .getByRole("dialog")
        .getByRole("textbox", { name: "Your answer" })
        .fill("Add the model first.");
      await page.getByRole("dialog").getByRole("button", { name: "Send" }).click();
      await expect(page.getByRole("dialog")).toBeHidden();
      await holdNotice(page, answered);
    },
    () => releaseNotice(page),
  );
  await page.unroute("**/api/inbox/201/answer");
  const briefBubble = main.getByRole("region", { name: "Brief" });
  // The Inbox of the organization plants has no item, so the frame has no Inbox count.
  const briefFrame = (device: string, workstream: number) =>
    device === "desktop"
      ? [
          drain,
          page.getByLabel("Work in another organization"),
          page.locator(`nav a[href="/workstreams/plants/garden/${workstream}"]`),
        ]
      : [page.getByLabel("Upgrade available")];
  await screenshot(page, "chat-brief-empty", "/workstreams/plants/garden/18", (device) => [
    ...briefFrame(device, 18),
    briefBubble.getByText("Cut the old canes in March."),
    main.getByText("No messages. Write to start a chat session."),
  ]);
  const records = ["You started #1127.", "You set the Autopilot level to Start tasks."];
  await screenshot(
    page,
    "chat-records",
    "/workstreams/plants/garden/12",
    (device) => [
      ...briefFrame(device, 12),
      main.getByText("Which roses sell best?"),
      ...records.map((text) => main.locator("[data-message]", { hasText: text })),
    ],
    async () => {
      await expect(main.locator("[data-message]", { hasText: records[1] })).toBeVisible();
      await showChatEnd(page);
    },
  );
  // The chat of plants/garden#12 sets the organization plants, and /chat shows the Coordinator chat of plants.
  const dismissRecord = main.locator("[data-message]", { hasText: 'You dismissed "Which unit?".' });
  await screenshot(
    page,
    "coordinator-records",
    "/chat",
    () => [main.getByText("Start a Workstream for gift cards."), dismissRecord],
    async () => {
      await expect(dismissRecord).toBeVisible();
      await showChatEnd(page);
    },
  );
  // The chat of plants/garden#60 has 50 messages in pages of 20. The scroll to the top loads the first page, and the
  // Brief shows above its first message. The chat is at that top again after the Brief shows.
  const scrollBriefTop = () =>
    page.evaluate(`(() => {
      const list = document.querySelector('[data-message]')?.parentElement
      if (!list) {
        return false
      }
      list.scrollTop = 0
      return list.scrollTop === 0 && list.querySelector('section[aria-label="Brief"]') !== null
    })()`);
  await screenshot(
    page,
    "chat-brief-top",
    "/workstreams/plants/garden/60",
    (device) => [
      ...briefFrame(device, 60),
      briefBubble.getByText("Sort the seeds by month."),
      main.getByText("Seed note 1.", { exact: false }).first(),
    ],
    async () => {
      await expect.poll(scrollBriefTop).toBe(true);
    },
  );
  await screenshot(
    page,
    "chat-brief-all",
    "/workstreams/plants/garden/19",
    (device) => [
      ...briefFrame(device, 19),
      briefBubble.getByRole("button", { name: "Show less" }),
      briefBubble.getByText("The roses have no bark at their stems."),
    ],
    async () => {
      await briefBubble.getByRole("button", { name: "Show all" }).click();
    },
  );
  await screenshot(
    page,
    "chat-brief-tab",
    "/workstreams/plants/garden/19",
    (device) => [
      ...briefFrame(device, 19),
      page.getByRole("tab", { name: "Brief" }).filter({ visible: true }),
      page
        .getByRole("tabpanel", { name: "Brief" })
        .filter({ visible: true })
        .getByText("Put bark on the beds."),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Brief" }).filter({ visible: true }).click();
    },
  );
  // The nav shows the Workstream rows before it reads the "needs you" list and the unread counts, the organization dot
  // comes from the counts of the other organizations, and the pause banner comes from its own read, so wait for each
  // element of the final state.
  const navRow = (number: number) =>
    page.locator(`nav a[href="/workstreams/plants/garden/${number}"]`);
  const needsYou = (number: number) => navRow(number).getByText("needs you", { exact: true });
  const unreadOf = (number: number, count: number) =>
    navRow(number).getByText(`${count}`, { exact: true });
  await screenshot(page, "chat-brief-button", "/workstreams/plants/garden/18", (device) =>
    device === "desktop"
      ? [
          page.getByRole("button", { name: "Brief" }).filter({ visible: true }),
          ...[30, 20, 19].map(needsYou),
          chatCount,
          unreadOf(60, 35),
          ...[17, 16, 15, 14].map((number) => unreadOf(number, 7)),
          page.getByLabel("Work in another organization").filter({ visible: true }),
          page.getByText(/^antigravity paused until Sep 28, 12:00\sPM$/),
        ]
      : [page.getByRole("banner").getByRole("heading", { name: "Prune the roses" })],
  );
  // The open menu and the open sheet hide the other elements from the accessibility tree, so the open step waits for them.
  await screenshot(
    page,
    "autopilot-levels",
    "/workstreams/owner/shop/12",
    () =>
      [
        "You start the tasks and merge them.",
        "Mobius starts the ready tasks. You merge.",
        "You start the tasks. Mobius merges after the Lead approves.",
        "Mobius starts the ready tasks and merges after the Lead approves.",
      ].map((text) => page.getByText(text, { exact: true })),
    async (device) => {
      for (const locator of chatReady(device)) {
        await expect(locator).toBeVisible();
      }
      await page.getByRole("button", { name: "Autopilot: Off" }).filter({ visible: true }).click();
    },
    () => page.keyboard.press("Escape"),
  );
  // The route gives owner/shop#12 the level "Start and merge". The route ends after the screenshot.
  const triggerRoute = "**/api/workstreams";
  await page.route(triggerRoute, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data: { repository: string; number: number; autopilot: string }[];
    };
    for (const workstream of body.data) {
      if (workstream.repository === "owner/shop" && workstream.number === 12) {
        workstream.autopilot = "start_and_merge";
      }
    }
    await route.fulfill({ response, json: body });
  });
  await screenshot(page, "autopilot-trigger", "/workstreams/owner/shop/12", () =>
    page.getByRole("button", { name: "Autopilot: Start and merge" }).filter({ visible: true }),
  );
  await page.unroute(triggerRoute);
  await screenshot(
    page,
    "chat-listening",
    "/workstreams/owner/shop/12",
    (device) => [...chatReady(device), main.getByRole("button", { name: "Stop voice input" })],
    () => main.getByRole("button", { name: "Start voice input" }).click(),
  );
  await page.route("**/api/voice/recordings", (route) =>
    route.fulfill({ json: { data: { id: "r1" } } }),
  );
  await page.route("**/api/voice/recordings/r1/chunks?*", (route) =>
    route.fulfill({
      json: {
        data: { committed: "Show the prices of the roses first.", draft: "Then the tulips" },
      },
    }),
  );
  await page.route("**/api/voice/recordings/r1/finish", (route) =>
    route.fulfill({ json: { data: { text: "Show the prices of the roses first." } } }),
  );
  await screenshot(
    page,
    "chat-live",
    "/workstreams/owner/shop/12",
    (device) => [...chatReady(device), main.getByRole("button", { name: "Stop voice input" })],
    async () => {
      await main.getByRole("button", { name: "Start voice input" }).click();
      await expect(main.getByLabel("Message to the Lead")).toHaveValue(
        "Show the prices of the roses first. Then the tulips",
      );
    },
    async () => {
      await main.getByRole("button", { name: "Stop voice input" }).click();
      await expect(main.getByRole("button", { name: "Start voice input" })).toBeVisible();
      await main.getByLabel("Message to the Lead").fill("");
    },
  );
  await page.route("**/api/voice/recordings/r1/finish", () => {});
  await screenshot(
    page,
    "chat-voice-send-wait",
    "/workstreams/owner/shop/12",
    (device) => [
      ...chatReady(device),
      main.getByRole("button", { name: "Converting the voice input" }),
      main.getByRole("button", { name: "Send", exact: true }).locator("[data-slot=spinner]"),
    ],
    async () => {
      await main.getByRole("button", { name: "Start voice input" }).click();
      await expect(main.getByLabel("Message to the Lead")).toHaveValue(
        "Show the prices of the roses first. Then the tulips",
      );
      await main.getByRole("button", { name: "Send", exact: true }).click();
    },
    async () => {
      await page.reload();
      await main.getByRole("button", { name: "Discard" }).click();
      await expect.poll(() => page.evaluate(storedAudio)).toBe(0);
      await main.getByLabel("Message to the Lead").fill("");
      await expect.poll(() => page.evaluate(storedDrafts)).toBe(0);
    },
  );
  await page.unroute("**/api/voice/recordings");
  await page.unroute("**/api/voice/recordings/r1/chunks?*");
  await page.unroute("**/api/voice/recordings/r1/finish");
  await page.route("**/api/voice/transcriptions", () => {});
  await screenshot(
    page,
    "chat-converting",
    "/workstreams/owner/shop/12",
    (device) => [
      ...chatReady(device),
      main.getByRole("button", { name: "Converting the voice input" }),
    ],
    async () => {
      await main.getByRole("button", { name: "Start voice input" }).click();
      await expect.poll(() => page.evaluate("samples")).toBeGreaterThan(0);
      await main.getByRole("button", { name: "Stop voice input" }).click();
    },
    async () => {
      await page.reload();
      await main.getByRole("button", { name: "Discard" }).click();
      await expect.poll(() => page.evaluate(storedAudio)).toBe(0);
    },
  );
  await page.unroute("**/api/voice/transcriptions");
  await page.route("**/api/voice/recordings", (route) =>
    route.fulfill({ status: 500, json: { error: "failed" } }),
  );
  await page.route("**/api/voice/transcriptions", (route) =>
    route.fulfill({ status: 503, json: { error: "The speech model is still starting" } }),
  );
  await screenshot(
    page,
    "chat-voice-error",
    "/workstreams/owner/shop/12",
    (device) => [
      ...chatReady(device),
      main.getByText("The speech model is still starting"),
      main.getByRole("button", { name: "Send again" }),
      main.getByRole("button", { name: "Discard" }),
    ],
    async () => {
      await main.getByRole("button", { name: "Start voice input" }).click();
      await expect.poll(() => page.evaluate("samples")).toBeGreaterThan(0);
      await main.getByRole("button", { name: "Stop voice input" }).click();
    },
    async () => {
      await main.getByRole("button", { name: "Discard" }).click();
      await expect.poll(() => page.evaluate(storedAudio)).toBe(0);
    },
  );
  await page.unroute("**/api/voice/recordings");
  await page.unroute("**/api/voice/transcriptions");
  await screenshot(
    page,
    "chat-typing",
    "/workstreams/owner/shop/12",
    (device) => [...typingReady(device), main.getByRole("button", { name: "Send", exact: true })],
    () => main.getByLabel("Message to the Lead").fill("Show the prices of the roses first."),
  );
  await main.getByLabel("Message to the Lead").fill("");
  await expect.poll(() => page.evaluate(storedDrafts)).toBe(0);
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { writing: boolean; stoppable: boolean } };
    body.data.writing = true;
    body.data.stoppable = true;
    await route.fulfill({ response, json: body });
  });
  const stopEnabled = () =>
    expect(main.getByRole("button", { name: "Stop the reply" })).toBeEnabled();
  await screenshot(
    page,
    "chat-writing",
    "/workstreams/owner/shop/12",
    (device) => [...chatReady(device), main.getByRole("button", { name: "Stop the reply" })],
    stopEnabled,
  );
  await page.unroute("**/api/chat?*");
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data: {
        writing: boolean;
        stoppable: boolean;
        messages: { author: string; deliveredAt: string | null }[];
      };
    };
    body.data.writing = true;
    body.data.stoppable = true;
    for (const message of body.data.messages) {
      if (message.author === "Owner") {
        message.deliveredAt = null;
      }
    }
    await route.fulfill({ response, json: body });
  });
  const storedIcon = main.getByRole("img", { name: "Stored, waits for the agent" }).last();
  await screenshot(
    page,
    "chat-waiting",
    "/workstreams/owner/shop/12",
    (device) => [
      ...chatReady(device),
      storedIcon,
      main.getByText("The message waits for the Lead."),
      main.getByRole("button", { name: "Stop the reply" }),
    ],
    async () => {
      await stopEnabled();
      await storedIcon.scrollIntoViewIfNeeded();
    },
  );
  await page.unroute("**/api/chat?*");
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { writing: boolean; pausedUntil: string } };
    body.data.writing = true;
    body.data.pausedUntil = "2026-09-28T12:00:00Z";
    await route.fulfill({ response, json: body });
  });
  await screenshot(page, "chat-paused", "/workstreams/owner/shop/12", (device) => [
    ...chatReady(device),
    main.getByText(/waits for the usage limit until/),
  ]);
  await page.unroute("**/api/chat?*");
  const limitNotice =
    "claude-code reached a usage limit. Mobius sends the prompt again at 2026-09-28 12:00 UTC.";
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data: { writing: boolean; pausedUntil: string; messages: Record<string, unknown>[] };
    };
    body.data.writing = true;
    body.data.pausedUntil = "2026-09-28T12:00:00Z";
    const fields = {
      action: null,
      browserId: "",
      deliveredAt: null,
      images: 0,
      inboxItems: [],
      organization: "owner",
      repository: "owner/shop",
      stoppedAt: null,
      workstream: 12,
    };
    body.data.messages.push(
      {
        ...fields,
        id: 1_000_000,
        author: "Owner",
        text: "Show the prices of the roses first.",
        time: "2026-09-28T09:40:00Z",
      },
      {
        ...fields,
        id: 1_000_001,
        author: "Mobius",
        text: limitNotice,
        time: "2026-09-28T09:40:04Z",
      },
    );
    await route.fulfill({ response, json: body });
  });
  const ownerNote = main.locator("[data-message]", {
    hasText: "Show the prices of the roses first.",
  });
  const limitEnd = main.getByText(/^claude-code reached a usage limit\./);
  await screenshot(
    page,
    "chat-usage-limit",
    "/workstreams/owner/shop/12",
    (device) => [
      ...chatReady(device),
      ownerNote.getByRole("img", { name: "Stored, waits for the agent" }),
      limitEnd,
    ],
    async () => {
      await expect(limitEnd).toBeVisible();
      await ownerNote.evaluate((el) => el.scrollIntoView({ block: "start" }));
    },
  );
  await page.unroute("**/api/chat?*");
  const stoppedRow = main.getByRole("listitem").filter({ hasText: "Add plan model" });
  const composer = main.locator("form");
  // The next device opens the page again, and it must not find a draft of the last device.
  const clearComposer = async () => {
    const remove = composer.getByRole("button", { name: /^Remove / });
    while ((await remove.count()) > 0) {
      await remove.first().click();
    }
    await main.getByLabel("Message to the Lead").fill("");
    await expect.poll(() => page.evaluate(storedDrafts)).toBe(0);
  };
  await screenshot(page, "chat-stop-reason", "/workstreams/owner/shop/12", (device) => [
    ...chatReady(device),
    stoppedRow.getByRole("button", { name: "Answer" }),
  ]);
  await screenshot(
    page,
    "chat-stop-details",
    "/workstreams/owner/shop/12",
    () => page.getByRole("dialog", { name: "Add plan model" }),
    () => stoppedRow.getByRole("button", { name: "Answer" }).click(),
    () => page.keyboard.press("Escape"),
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
      ...typingReady(device),
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
    clearComposer,
  );
  await screenshot(
    page,
    "chat-inbox-item",
    "/workstreams/owner/shop/12",
    (device) => [
      ...chatReady(device),
      composer.getByText("Add plan model"),
      composer.getByText("The agent asked a question"),
      composer.getByRole("button", { name: "Remove #41" }),
      composer.getByRole("img", { name: "Image 1" }),
    ],
    async () => {
      await stoppedRow.getByRole("button", { name: "Chat" }).click();
      await composer.getByRole("button", { name: "Remove #41" }).waitFor();
      await composer.locator("input[type=file]").setInputFiles(photos[0]);
      await expect(composer.getByRole("img", { name: "Image 1" })).toHaveJSProperty(
        "naturalWidth",
        200,
      );
    },
    clearComposer,
  );
  await page.unroute(inboxRoute);
  await routeInboxItems(page, [longTitleItem]);
  await screenshot(page, "chat-stop-long-title", "/workstreams/owner/shop/12", (device) => [
    ...frame(device, drain, {}, "2"),
    main.getByText(longTitle),
    main.getByRole("link", { name: "#409" }),
  ]);
  await page.unroute(inboxRoute);
  await page.route(inboxRoute, (route) =>
    route.fulfill({ json: { data: [longTitleItem, leadActionItem] } }),
  );
  await screenshot(
    page,
    "chat-waits",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain, {}, "2"),
      page.getByRole("heading", { name: "Waits for you (2)" }),
      main.getByRole("listitem").filter({ hasText: longTitle }),
      main.getByRole("listitem").filter({ hasText: leadActionItem.title }),
    ],
    async () => {
      const waits = page.getByRole("heading", { name: "Waits for you (2)" });
      await expect(waits).toBeVisible();
      await expect(main.getByRole("listitem").filter({ hasText: longTitle })).toBeVisible();
      await expect(
        main.getByRole("listitem").filter({ hasText: leadActionItem.title }),
      ).toBeVisible();
      await showChatEnd(page);
    },
  );
  await page.unroute(inboxRoute);
  await page.route(inboxRoute, (route) =>
    route.fulfill({ json: { data: [longTitleItem, leadActionItem] } }),
  );
  await setTray(page, "collapsed");
  await screenshot(
    page,
    "chat-waits-collapsed",
    "/workstreams/owner/shop/12",
    (device) => [...frame(device, drain, {}, "2"), trayToggle(page, 2, false)],
    async () => {
      await expect(trayToggle(page, 2, false)).toBeVisible();
      await expect(page.getByRole("list", { name: "Pending actions" })).toBeHidden();
      await showChatEnd(page);
    },
  );
  await setTray(page, "expanded");
  await screenshot(
    page,
    "chat-waits-typing",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain, {}, "2"),
      page.getByLabel("Message to the Lead"),
      ...(device === "desktop" ? [trayToggle(page, 2, true)] : []),
    ],
    async (device) => {
      await showChatEnd(page);
      await page.getByLabel("Message to the Lead").focus();
      await expect(page.getByLabel("Message to the Lead")).toBeFocused();
      await expect(trayToggle(page, 2, true)).toBeVisible({ visible: device === "desktop" });
    },
  );
  await setTray(page, "expanded");
  await page.unroute(inboxRoute);
  await page.route(inboxRoute, (route) =>
    route.fulfill({ json: { data: [coordinatorQuestion, coordinatorActionItem] } }),
  );
  await screenshot(
    page,
    "chat-coordinator-waits",
    "/chat",
    () => [
      trayToggle(page, 2, true),
      main.getByRole("listitem").filter({ hasText: coordinatorQuestion.title }),
      main.getByRole("listitem").filter({ hasText: coordinatorActionItem.title }),
    ],
    async () => {
      await expect(trayToggle(page, 2, true)).toBeVisible();
      await showChatEnd(page);
    },
  );
  await setTray(page, "collapsed");
  await screenshot(
    page,
    "chat-coordinator-waits-collapsed",
    "/chat",
    () => trayToggle(page, 2, false),
    async () => {
      await expect(trayToggle(page, 2, false)).toBeVisible();
      await showChatEnd(page);
    },
  );
  await setTray(page, "expanded");
  await page.unroute(inboxRoute);
  await page.route(inboxRoute, (route) =>
    route.fulfill({ json: { data: [coordinatorFailedItem] } }),
  );
  await screenshot(
    page,
    "chat-coordinator-failed",
    "/chat",
    () => [
      trayToggle(page, 1, true),
      main.getByRole("listitem").filter({ hasText: coordinatorFailedItem.title }),
      main.getByRole("button", { name: "Send again" }),
      main.getByRole("button", { name: "Dismiss" }),
    ],
    async () => {
      await expect(trayToggle(page, 1, true)).toBeVisible();
      await showChatEnd(page);
    },
  );
  await setTray(page, "expanded");
  await page.unroute(inboxRoute);
  await page.route(inboxRoute, (route) => route.fulfill({ json: { data: todoItems } }));
  await screenshot(
    page,
    "chat-waits-todo",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain, {}, "2"),
      trayToggle(page, 2, true),
      main.getByText("Todo · mandatory"),
      main.getByText("Todo · optional"),
    ],
    async () => {
      await expect(trayToggle(page, 2, true)).toBeVisible();
      await expect(main.getByText("Todo · mandatory")).toBeVisible();
      await expect(main.getByText("Todo · optional")).toBeVisible();
      await showChatEnd(page);
    },
  );
  await page.evaluate(() => localStorage.removeItem("tray"));
  await page.unroute(inboxRoute);
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
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
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
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
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
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
      await page.getByLabel("Show closed tasks").filter({ visible: true }).click();
    },
  );
  await routeLongTask(page);
  await screenshot(
    page,
    "chat-tasks-long-title",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain),
      page.getByText(longTaskTitle, { exact: false }).filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
    },
  );
  await page.unroute(tasksUrl);
  const openDetails = async (device: string) => {
    if (device === "phone") {
      await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
    }
    await page.getByRole("tab", { name: "Details" }).filter({ visible: true }).click();
  };
  const closeButton = page
    .getByRole("tabpanel", { name: "Details" })
    .getByRole("button", { name: "Close" });
  const created = page.getByText("Sep 28, 2026").filter({ visible: true });
  const evergreenSwitch = page
    .getByRole("tabpanel", { name: "Details" })
    .getByRole("switch", { name: "Evergreen" });
  await screenshot(
    page,
    "chat-details",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain),
      created,
      page.getByText("Completed tasks").filter({ visible: true }),
      closeButton,
      evergreenSwitch,
    ],
    openDetails,
  );
  const sidebar = page.getByRole("navigation").first();
  const workstreamsLink = sidebar.getByRole("link", { name: "Workstreams" });
  const evergreenLink = sidebar.getByRole("link", { name: /^Integrate loyalty plans/ });
  const sidebarShowsEvergreen = (on: boolean) => async () => {
    if (await workstreamsLink.isVisible()) {
      await expect
        .poll(
          async () =>
            (await workstreamsLink.boundingBox())!.y > (await evergreenLink.boundingBox())!.y,
        )
        .toBe(on);
    }
  };
  await screenshot(
    page,
    "chat-details-evergreen",
    "/workstreams/owner/shop/12",
    (device) => [...frame(device, drain), created, evergreenSwitch],
    async (device) => {
      await openDetails(device);
      await evergreenSwitch.click();
      await expect(evergreenSwitch).toBeChecked();
      await sidebarShowsEvergreen(true)();
      await fixRecordTimes(page);
      await page.goto("/workstreams/owner/shop/12");
      await openDetails(device);
      await expect(evergreenSwitch).toBeChecked();
    },
    async () => {
      await evergreenSwitch.click();
      await expect(evergreenSwitch).not.toBeChecked();
      await sidebarShowsEvergreen(false)();
      await fixRecordTimes(page);
    },
  );
  await screenshot(
    page,
    "chat-details-maintenance",
    "/workstreams/owner/shop/14",
    (device) => [
      ...frame(device, drain),
      evergreenSwitch,
      page
        .getByText("Mobius manages this Workstream, so this value cannot change.")
        .filter({ visible: true }),
    ],
    async (device) => {
      await waitForEvergreen(page);
      await openDetails(device);
      await expect(evergreenSwitch).toBeChecked();
      await expect(evergreenSwitch).toBeDisabled();
    },
  );
  await screenshot(
    page,
    "chat-details-close",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain),
      page.getByRole("dialog").getByText("#41 Add plan model"),
      page.getByRole("dialog").getByText("#12 Integrate loyalty plans"),
    ],
    async (device) => {
      await openDetails(device);
      await expect(created).toBeVisible();
      await closeButton.click();
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
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
    },
  );
  const otherRepositoryRoute = "**/api/workstreams/plants/garden/19/tasks";
  await page.route(otherRepositoryRoute, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: object[] };
    body.data.push({
      number: 5,
      title: "Pick the apples",
      state: "open",
      depth: 0,
      repository: "plants/orchard",
      otherRepository: true,
      url: "https://github.com/plants/orchard/issues/5",
      blockedBy: [],
    });
    await route.fulfill({ response, json: body });
  });
  await screenshot(
    page,
    "chat-tasks-other-repository",
    "/workstreams/plants/garden/19",
    (device) => [
      ...(device === "desktop"
        ? [
            drain,
            page.getByLabel("Work in another organization"),
            page.locator('nav a[href="/workstreams/plants/garden/19"]'),
          ]
        : [page.getByLabel("Upgrade available")]),
      page.getByText("plants/orchard#5 Pick the apples").filter({ visible: true }),
      page.getByRole("button", { name: "Start plants/orchard#5" }).filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
    },
  );
  await page.unroute(otherRepositoryRoute);
  const stopRoute = "**/api/workstreams/plants/garden/19/tasks";
  await page.route(stopRoute, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: object[] };
    body.data.push({
      number: 74,
      title: "Prune the hedge",
      state: "working",
      depth: 0,
      repository: "plants/garden",
      otherRepository: false,
      url: "https://github.com/plants/garden/issues/74",
      blockedBy: [],
    });
    await route.fulfill({ response, json: body });
  });
  await screenshot(
    page,
    "chat-tasks-stop",
    "/workstreams/plants/garden/19",
    (device) => [
      ...(device === "desktop"
        ? [
            drain,
            page.getByLabel("Work in another organization"),
            page.locator('nav a[href="/workstreams/plants/garden/19"]'),
          ]
        : [page.getByLabel("Upgrade available")]),
      page.getByText("#74 Prune the hedge").filter({ visible: true }),
      page.getByRole("button", { name: "Stop #74" }).filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
    },
  );
  await page.unroute(stopRoute);
  await screenshot(page, "chat-all-tasks-closed", "/workstreams/owner/shop/13", (device) => [
    ...frame(device, drain),
    main.getByText("All tasks are closed."),
    main.getByText("Change the prices for each season."),
  ]);
  await screenshot(page, "chat-coordinator", "/chat", (device) => [
    chatCount,
    ...frame(device, drain),
    main.getByText("Sell gift cards in the shop."),
  ]);
  // The Inbox of the server has one item. The route adds one item of each other kind.
  let mergeState = { merging: false, mergeRefusal: "" };
  await page.route("/api/inbox", async (route) => {
    const response = await route.fetch();
    const { data } = (await response.json()) as { data: object[] };
    const link = "https://github.com/owner/shop";
    const item = {
      dismissedAt: null,
      questions: [],
      organization: "owner",
      repository: "owner/shop",
      workstreamRepository: "owner/shop",
      workstream: 12,
      time: "2026-09-28T09:30:00Z",
      title: "",
      reason: "",
      link: "",
      pullRequest: 0,
      pullRequestUrl: "",
      reportUrl: "",
    };
    await route.fulfill({
      json: {
        data: [
          ...data,
          {
            ...item,
            id: 101,
            issue: 42,
            kind: "stopped",
            title: "Let customers change plans",
            reason: "The implementer session hung",
            text: "Mobius stopped the implementer session 7. It had no activity after 3 retries. The session worked on #42.",
            link: `${link}/issues/42`,
          },
          {
            ...item,
            id: 102,
            issue: 41,
            kind: "lost",
            title: "Add plan model",
            reason: "Mobius lost the task",
            text: "Mobius lost the state of this task. Add mobius:ready to start again.",
            link: `${link}/issues/41`,
          },
          {
            ...item,
            id: 103,
            issue: 30,
            kind: "ready for review",
            title: "Name the plan tiers",
            reason: "PR #44 is ready to merge",
            text: 'Pull request #44 of #30 "Name the plan tiers" is ready for review.',
            link: `${link}/issues/30`,
            pullRequest: 44,
            pullRequestUrl: `${link}/pull/44`,
            reportUrl: `${link}/pull/44#issuecomment-1`,
            ...mergeState,
          },
          {
            ...item,
            id: 100,
            issue: 40,
            kind: "todo",
            title: "MOBIUS_SMTP_HOST",
            reason: "mandatory · Deployment needs",
            text: "env_var: MOBIUS_SMTP_HOST",
            link: `${link}/issues/40`,
            pullRequest: 1110,
            pullRequestUrl: `${link}/pull/1110`,
            todo: {
              kind: "env_var",
              name: "MOBIUS_SMTP_HOST",
              path: "internal/config/config.go",
              line: 42,
              mandatory: true,
              reason: "The server reads it at the start and has no default.",
              note: "",
            },
          },
          {
            ...item,
            id: 105,
            issue: 43,
            kind: "todo",
            title: "Run the plan import",
            reason: "optional · Deployment needs",
            text: "manual_step: Run the plan import",
            link: `${link}/issues/43`,
            pullRequest: 1110,
            pullRequestUrl: `${link}/pull/1110`,
            todo: {
              kind: "manual_step",
              name: "Run the plan import",
              path: "docs/install.md",
              line: 12,
              mandatory: false,
              reason: "The old plans need the import once.",
              note: "",
            },
          },
          {
            ...item,
            id: 104,
            issue: 12,
            kind: "Lead failed",
            title: `Lead of #12 Integrate loyalty plans did not get: "Start the work on the plan tiers."`,
            reason: "The Lead did not get your message",
            text: "Start the work on the plan tiers.",
          },
        ],
      },
    });
  });
  const inboxReady = (device: string) => [
    chatCount,
    ...frame(device, drain, { organizations: true }, "7"),
    main.getByRole("tab", { name: "To do 7" }),
    main.getByText("Integrate loyalty plans", { exact: true }),
    main.getByText("Let customers change plans"),
    main.getByText(
      `Lead of #12 Integrate loyalty plans did not get: "Start the work on the plan tiers."`,
    ),
    main.getByText("The Lead did not get your message"),
    main.getByText("MOBIUS_SMTP_HOST"),
    main.getByText("Run the plan import"),
    main.getByText("Todo · mandatory"),
    main.getByText("Todo · optional"),
  ];
  await screenshot(page, "inbox", "/inbox", inboxReady);
  const todoRow = main.getByRole("listitem").filter({ hasText: "MOBIUS_SMTP_HOST" });
  await screenshot(page, "inbox-todo", "/inbox", (device) => [
    ...inboxReady(device),
    todoRow.getByRole("button", { name: "Complete" }),
    todoRow.getByRole("link", { name: "Open PR" }),
  ]);
  // Radix hides the page behind the panel from the role queries, so the open step waits for the page first.
  const openPanel = (button: string, index: number) => async (device: string) => {
    for (const locator of inboxReady(device)) {
      await expect(locator).toBeVisible();
    }
    await main.getByRole("button", { name: button }).nth(index).click();
  };
  await screenshot(
    page,
    "inbox-details",
    "/inbox",
    () => [
      page.getByRole("dialog").getByText("is ready for review."),
      page.getByRole("dialog").getByRole("link", { name: "Approval report" }),
    ],
    openPanel("Details", 2),
  );
  await screenshot(
    page,
    "todo-dialog",
    "/inbox",
    () => [
      page.getByRole("dialog").getByText("Mandatory env var"),
      page.getByRole("dialog").getByText("File: internal/config/config.go:42"),
      page.getByRole("dialog").getByText("Mobius merges after you complete this todo."),
      page.getByRole("dialog").getByRole("textbox", { name: "Note" }),
    ],
    openPanel("Complete", 0),
  );
  await screenshot(
    page,
    "inbox-answer",
    "/inbox",
    () => [
      page.getByRole("dialog").getByText("#45 needs a decision: one seat limit"),
      page.getByRole("dialog").getByRole("textbox", { name: "Your answer" }),
    ],
    openPanel("Answer", 0),
  );
  await screenshot(
    page,
    "inbox-answer-input",
    "/inbox",
    () => page.getByRole("dialog").getByRole("textbox", { name: "Your answer" }),
    async (device) => {
      await openPanel("Answer", 0)(device);
      await page.getByRole("dialog").getByRole("textbox", { name: "Your answer" }).click();
    },
  );
  const readyRow = main.getByRole("listitem").filter({ hasText: "Name the plan tiers" });
  const merged = noticeOf(page, "Started the merge of PR #44");
  await screenshot(page, "inbox-merge", "/inbox", (device) => [
    ...inboxReady(device),
    readyRow.getByRole("button", { name: "Merge" }),
  ]);
  await page.route("**/api/inbox/103/merge", (route) =>
    route.fulfill({ json: { data: { merging: true } } }),
  );
  await screenshot(
    page,
    "inbox-merging",
    "/inbox",
    () => readyRow.getByRole("button", { name: "Merging" }),
    async (device) => {
      for (const locator of inboxReady(device)) {
        await expect(locator).toBeVisible();
      }
      await readyRow.getByRole("button", { name: "Merge" }).click();
      await skipNotice(page, merged);
    },
  );
  await screenshot(
    page,
    "notice-merge",
    "/inbox",
    () => [merged, readyRow.getByRole("button", { name: "Merging" })],
    async (device) => {
      for (const locator of inboxReady(device)) {
        await expect(locator).toBeVisible();
      }
      await readyRow.getByRole("button", { name: "Merge" }).click();
      await holdNotice(page, merged);
    },
    () => releaseNotice(page),
  );
  await page.unroute("**/api/inbox/103/merge");
  mergeState = {
    merging: false,
    mergeRefusal: "Pull request #44 is not mergeable: the check run Build failed.",
  };
  await screenshot(page, "inbox-merge-refusal", "/inbox", (device) => [
    ...inboxReady(device),
    readyRow.getByText(mergeState.mergeRefusal),
    readyRow.getByRole("button", { name: "Merge" }),
  ]);
  await page.unroute("/api/inbox");
  await page.route("**/api/ci-stops", (route) =>
    route.fulfill({
      json: {
        data: [
          {
            repository: "owner/shop",
            branch: "main",
            workflow: "Continuous integration of the checkout service",
            runUrl: "https://github.com/owner/shop/actions/runs/1",
            issue: 30,
            issueUrl: "https://github.com/owner/shop/issues/30",
          },
        ],
      },
    }),
  );
  await screenshot(page, "ci-stop", "/workstreams", (device) => [
    ...frame(device, drain, { organizations: true }),
    page.getByText("Dispatch stopped: CI fails on main"),
    page.getByRole("link", { name: "#30" }),
  ]);
  expect(await page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")).toBe(
    true,
  );
  await page.unroute("**/api/ci-stops");
  await screenshot(page, "inbox-activity", "/inbox/activity", (device) => [
    chatCount,
    ...frame(device, drain, { organizations: true }),
    main.getByText('Dispatched "Pick the plan limits"'),
    main.getByRole("button", { name: "Integrate loyalty plans" }),
  ]);
  await page.unroute("/api/unread");
  await page.unroute("/api/chat/seen");

  // The chat shows its last message, and the agent list hides each stopped agent.
  await page.setViewportSize(viewports.desktop);
  await page.goto("/workstreams/owner/shop/12");
  await expect(main.getByText("#42 and #45 wait for your decision.")).toBeInViewport();
  const agents = page.getByRole("complementary");
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  const stopped = agents.getByText("stopped", { exact: true });
  await expect(stopped).toHaveCount(0);
  await agents.getByRole("switch", { name: "Show stopped agents" }).click();
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
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
      await page
        .getByRole("button", { name: /^implementer devin · swe-1.5 · Sep \d+, \d\d:\d\d [AP]M$/ })
        .filter({ visible: true })
        .click();
      await logConnected(page);
    },
  );
  await setQueueReason(page, "waits for a low load");
  await screenshot(
    page,
    "transcript-panel-start-check",
    "/workstreams/owner/shop/12",
    (device) => [
      ...frame(device, drain),
      page.getByRole("button", { name: "Start the check now" }).filter({ visible: true }),
    ],
    async (device) => {
      if (device === "phone") {
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
      await page
        .getByRole("button", {
          name: /^implementer devin · swe-1.5 · Sep \d+, \d\d:\d\d [AP]M · waits for a low load/,
        })
        .filter({ visible: true })
        .click();
      await logConnected(page);
    },
  );
  await screenshot(page, "agents", "/agents", (device) => [
    ...frame(device, drain),
    main.getByText(/Sep \d+, \d\d:\d\d [AP]M · Mobius prepares an upgrade/),
    ...queueReasons.map((reason) => main.getByText(reason)),
  ]);
  await setQueueReason(page, "Autopilot paused: no task starts");
  await screenshot(page, "agents-start-paused", "/agents", (device) => [
    ...frame(device, drain),
    main.getByText(/· Autopilot paused: no task starts/),
  ]);
  await setQueueReason(page, "");
  await screenshot(
    page,
    "transcript",
    "/agents",
    (device) => [...frame(device, drain), main.getByText("The plan prices are in cents now.")],
    async () => {
      await main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click();
      await logConnected(page);
    },
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
    async () => {
      await main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click();
      await logConnected(page);
    },
  );
  await setQueueReason(page, "");

  await page.route("**/api/agents/*/transcript/events", () => {});
  await screenshot(
    page,
    "transcript-connecting",
    "/agents",
    (device) => [...frame(device, drain), main.getByText("Connecting…")],
    () => main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click(),
  );
  await page.unroute("**/api/agents/*/transcript/events");

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
  const longWord =
    "TestAStopOfATaskGivesItsReasonOnlyToThatTaskAndNotToATaskOfAnotherRepositoryWithTheSameNumber";
  const longChange = `Fix the CI failure of ${longWord} (#1221)`;
  await page.route("/api/release/changes", (route) =>
    route.fulfill({
      json: { data: ["Show the release changes in a modal", longChange, "Fix the memory scroll"] },
    }),
  );
  await screenshot(
    page,
    "upgrade-long-word",
    "/settings",
    (device) => [...frame(device, release, { upgrade: true }), page.getByText(longChange)],
    () => shown("Upgrade v0.1.4").click(),
  );
  await page.unroute("/api/release/changes");
  await page.route("/ui-version", (route) => route.fulfill({ body: "a new build" }));
  await screenshot(page, "new-version", "/settings", (device) => [
    ...frame(device, release, { upgrade: true }),
    shown("New version"),
  ]);
  await page.unroute("/ui-version");

  await screenshot(page, "settings", "/settings", (device) => [
    ...frame(device, release, { upgrade: true }),
    main.getByRole("link", { name: "Devices" }),
    main.getByText("All on", { exact: true }),
    main.getByText("v0.1.0", { exact: true }),
    main.getByText("v0.1.3", { exact: true }),
  ]);
  await screenshot(page, "settings-switches", "/settings/switches", (device) => [
    ...frame(device, release),
    ...["Start", "Merge", "Repairs"].map((name) => main.getByRole("switch", { name })),
    main.getByText("Mobius starts the tasks of CI breaks, check timeouts and CI failures."),
  ]);
  // The held work comes after the switch is off, because turning a switch on deletes the held work of the switch.
  await turnOff(page, "start", [38, 39, 45]);
  await turnOff(page, "merge", [42]);
  await screenshot(page, "settings-switches-off", "/settings", (device) => [
    ...frame(device, release, { upgrade: true }),
    page.getByText(lines.start, { exact: true }),
    page.getByText(lines.merge, { exact: true }),
    page.getByRole("button", { name: "3 held" }),
    page.getByRole("button", { name: "1 held" }),
    main.getByText("Start off, Merge off", { exact: true }),
  ]);
  await screenshot(
    page,
    "paused-held",
    "/settings",
    (device) => [
      ...frame(device, release, { upgrade: true }),
      page.getByRole("dialog", { name: lines.start }).getByText("#38 Show the plan prices"),
      page.getByRole("dialog", { name: lines.start }).getByText("#39 Send the plan receipts"),
      page.getByRole("dialog", { name: lines.start }).getByText("#45 Pick the plan limits"),
    ],
    () => page.getByRole("button", { name: "3 held" }).click(),
  );
  await turnAllOn(page);
  await screenshot(
    page,
    "settings-switches-error",
    "/settings",
    (device) => [
      ...frame(device, release, { upgrade: true }),
      main.getByText("All on", { exact: true }),
      main.getByRole("link", { name: /^Switches/ }).getByText("Error", { exact: true }),
    ],
    async () => {
      await page.goto("/settings/switches");
      await page.route(
        "**/api/switches/start",
        (route) => route.fulfill({ status: 500, json: { error: "The switch did not change." } }),
        { times: 1 },
      );
      const toggle = main.getByRole("switch", { name: "Start" });
      await expect(toggle).toBeChecked();
      const failed = page.waitForResponse(
        (res) => res.url().endsWith("/api/switches/start") && res.request().method() === "PUT",
      );
      await toggle.click();
      await failed;
      await expect(main.getByText("The switch did not change.")).toBeVisible();
      await page.getByRole("button", { name: "Back" }).filter({ visible: true }).click();
      await expect(page).toHaveURL("/settings");
      await page.unroute("**/api/switches/start");
    },
  );
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
  // An open sheet hides the page behind it from the accessibility tree.
  const roleRow = (name: string) =>
    page.getByRole("button", { name: new RegExp(`^${name}`), includeHidden: true });
  const roleSheet = page.getByRole("dialog", { name: "Reviewer" });
  const pickRole = async (label: string, value: string) => {
    await roleSheet.getByLabel(label, { exact: true }).click();
    await page.getByRole("menuitemradio", { name: value, exact: true }).click();
  };
  const openReviewer = async () => {
    await roleRow("Reviewer").click();
    await expect(roleSheet.getByLabel("Effort", { exact: true })).toBeVisible();
    await expect(roleSheet.getByRole("button", { name: "Save" })).toBeEnabled();
  };
  const writeRole = (method: "PUT" | "DELETE") =>
    page.waitForResponse(
      (res) => res.url().endsWith("/api/roles/judge") && res.request().method() === method,
    );
  await screenshot(page, "roles", "/settings/roles", (device) => [
    ...frame(device, release),
    roleRow("Lead"),
    roleRow("Diagnoser"),
  ]);
  await screenshot(
    page,
    "roles-sheet",
    "/settings/roles",
    (device) => [
      ...frame(device, release),
      roleSheet.getByRole("button", { name: "Save" }),
      roleSheet.getByLabel("Effort", { exact: true }),
    ],
    openReviewer,
  );
  await screenshot(
    page,
    "roles-model-list",
    "/settings/roles",
    (device) => [
      ...frame(device, release),
      page.getByRole("menuitemradio", { name: "sonnet", exact: true }),
      page.getByRole("menuitemradio", { name: "gemini-3-pro", exact: true }),
    ],
    async () => {
      await openReviewer();
      await roleSheet.getByLabel("Model", { exact: true }).click();
    },
  );
  await screenshot(
    page,
    "roles-refused",
    "/settings/roles",
    (device) => [
      ...frame(device, release),
      roleSheet.getByText("must not have the same harness and model"),
    ],
    async () => {
      await openReviewer();
      await pickRole("Harness", "devin");
      await pickRole("Model", "swe-1.5");
      const refused = page.waitForResponse(
        (res) => res.url().endsWith("/api/roles/reviewer") && res.request().method() === "PUT",
      );
      await roleSheet.getByRole("button", { name: "Save" }).click();
      expect((await refused).status()).toBe(422);
      await page.mouse.move(0, 0);
    },
  );
  await page.goto("/settings/roles");
  await roleRow("Judge").click();
  const judgeSheet = page.getByRole("dialog", { name: "Judge" });
  await expect(judgeSheet.getByLabel("Effort", { exact: true })).toBeVisible();
  await judgeSheet.getByLabel("Model", { exact: true }).click();
  await page.getByRole("menuitemradio", { name: "sonnet", exact: true }).click();
  const judgeSaved = writeRole("PUT");
  await judgeSheet.getByRole("button", { name: "Save" }).click();
  expect((await judgeSaved).ok()).toBe(true);
  await expect(judgeSheet).toBeHidden();
  await screenshot(page, "roles-ui", "/settings/roles", (device) => [
    ...frame(device, release),
    roleRow("Judge").getByText("sonnet"),
    roleRow("Judge").getByText("UI", { exact: true }),
  ]);
  await roleRow("Judge").click();
  const judgeReset = writeRole("DELETE");
  await judgeSheet.getByRole("button", { name: "Reset" }).click();
  expect((await judgeReset).ok()).toBe(true);
  await expect(roleRow("Judge").getByText("haiku")).toBeVisible();
  const voiceSection = (title: string) =>
    main.locator("section").filter({ has: page.getByRole("heading", { name: title }) });
  const voiceSearch = page.getByLabel("Search languages");
  const saveVoice = async (click: () => Promise<void>) => {
    const saved = page.waitForResponse(
      (res) => res.url().endsWith("/api/voice/language") && res.request().method() === "PUT",
    );
    await click();
    return saved;
  };
  const voiceLoaded = () =>
    expect(voiceSection("Suggested").getByRole("button").first()).toBeVisible();
  await screenshot(page, "voice", "/settings/voice", (device) => [
    ...frame(device, release),
    voiceSection("Suggested").locator('button[aria-current="true"]').getByText("English"),
    voiceSection("All languages").getByRole("button", { name: "Afrikaans" }),
  ]);
  await screenshot(
    page,
    "voice-selected",
    "/settings/voice",
    (device) => [
      ...frame(device, release),
      voiceSection("Suggested").locator('button[aria-current="true"]').getByText("German"),
    ],
    async () => {
      await voiceLoaded();
      await voiceSearch.fill("ger");
      const saved = await saveVoice(() => main.getByRole("button", { name: "German" }).click());
      expect((await saved).ok()).toBe(true);
      await voiceSearch.fill("");
      await page.mouse.move(0, 0);
    },
    async () => {
      const saved = await saveVoice(() =>
        voiceSection("Suggested").getByRole("button", { name: "English" }).click(),
      );
      expect((await saved).ok()).toBe(true);
    },
  );
  await screenshot(
    page,
    "voice-search",
    "/settings/voice",
    (device) => [...frame(device, release), main.getByRole("button", { name: "German" })],
    async () => {
      await voiceLoaded();
      await voiceSearch.fill("ger");
    },
  );
  await screenshot(
    page,
    "voice-error",
    "/settings/voice",
    (device) => [
      ...frame(device, release),
      page.getByText("Mobius could not save the language: database is locked."),
    ],
    async () => {
      await page.route("**/api/voice/language", (route) =>
        route.request().method() === "PUT"
          ? route.fulfill({ status: 500, json: { error: "database is locked" } })
          : route.continue(),
      );
      await voiceLoaded();
      const saved = await saveVoice(() =>
        voiceSection("Suggested").getByRole("button", { name: "Detect automatically" }).click(),
      );
      expect((await saved).status()).toBe(500);
      await page.mouse.move(0, 0);
    },
    () => page.unroute("**/api/voice/language"),
  );
  await screenshot(page, "checkup", "/settings/checkup", (device) => [
    ...frame(device, release),
    main.getByRole("link", { name: "Tools" }).getByText("needs you"),
    main.getByRole("heading", { name: "owner", exact: true }),
    main.getByRole("heading", { name: "plants", exact: true }),
    main.getByText("needs you").nth(3),
    main
      .locator("section")
      .filter({ hasText: "plants" })
      .getByRole("link", { name: "Workstreams" })
      .getByText("needs you"),
  ]);
  await screenshot(page, "checkup-tools", "/settings/checkup/tools", (device) => [
    ...frame(device, release),
    main.getByText("2.1.284 (Claude Code) · newest 2.1.300"),
    main.getByText("0.2.1-rc.4 · newest 0.2.1-rc.5"),
  ]);
  await screenshot(page, "checkup-permissions", "/settings/checkup/owner/permissions", (device) => [
    ...frame(device, release),
    main.getByText("workflows: write"),
  ]);
  await screenshot(page, "checkup-labels", "/settings/checkup/owner/labels", (device) => [
    ...frame(device, release),
    main.getByText("wrong color: #ededed"),
  ]);
  await screenshot(
    page,
    "checkup-workstreams",
    "/settings/checkup/plants/workstreams",
    (device) => [
      ...frame(device, release),
      main.getByRole("heading", { name: "plants/garden", exact: true }),
      main.getByText("Maintenance Workstream", { exact: true }),
      main.getByText("Skills Workstream", { exact: true }),
    ],
  );
  await screenshot(page, "memory-repositories", "/settings/memory", (device) => [
    ...frame(device, release),
    main.getByRole("link", { name: "owner/shop" }),
  ]);
  await screenshot(page, "memory-files", "/settings/memory/owner/shop", (device) => [
    ...frame(device, release),
    page.getByText("Memory of owner/shop").filter({ visible: true }),
    main.getByRole("link", { name: /Implementer/ }),
    main.getByText("2 lines").first(),
    main.getByText("Empty").first(),
  ]);
  await screenshot(page, "memory", "/settings/memory/owner/shop/shared", (device) => [
    ...frame(device, release),
    page.getByText("Shared · owner/shop").filter({ visible: true }),
    main.locator("ins", { hasText: "and each push" }),
    main.locator("del", { hasText: "fix round" }),
    main.locator("ins", { hasText: "Write each message in Simplified Technical English." }),
    main.locator("ins", { hasText: "Wait for a condition with testkit.WaitFor." }),
    main.getByText("A later version changed this part. Edit the file.").first(),
    main.getByText("Add: three fix rounds repeated the same wait with a fixed sleep in tests"),
  ]);
  await screenshot(page, "memory-role", "/settings/memory/owner/shop/implementer", (device) => [
    ...frame(device, release),
    page.getByText("Implementer · owner/shop").filter({ visible: true }),
    main.getByText("Use pnpm for the frontend.").first(),
    main.getByRole("heading", { name: "History" }),
  ]);
  const changedVersion = main
    .getByRole("heading", { name: "History" })
    .locator("xpath=following-sibling::ul/li")
    .filter({ hasText: "Change: the long line names the edit of a test" });
  await screenshot(
    page,
    "memory-diff",
    "/settings/memory/owner/shop/shared",
    (device) => [
      ...frame(device, release),
      changedVersion.locator("del", { hasText: "fix round" }),
    ],
    async () => {
      await expect(changedVersion.locator("ins", { hasText: "Implementer" })).toBeVisible();
      await changedVersion.evaluate((el) => el.scrollIntoView({ block: "start" }));
    },
  );
  const memoryDialog = page.getByRole("dialog", {
    name: "Edit Shared · owner/shop",
    includeHidden: true,
  });
  const memoryDiscard = page.getByRole("dialog", { name: "Discard your changes?" });
  const openMemoryEdit = async () => {
    await main.getByRole("button", { name: "Edit" }).click();
    await expect(memoryDialog.getByLabel("Memory text")).toBeEditable();
  };
  const tooLong = async () => {
    await openMemoryEdit();
    await memoryDialog.getByLabel("Memory text").fill("Water the roses.\n".repeat(201));
    await memoryDialog.getByRole("button", { name: "Save" }).click();
    await expect(memoryDialog.getByText("the maximum is 200")).toBeVisible();
    // The error row shrinks the field, and the offset that the field keeps from before can differ by one pixel.
    await memoryDialog.getByLabel("Memory text").evaluate((field) => {
      field.scrollTop = field.scrollHeight;
    });
  };
  await screenshot(
    page,
    "memory-edit",
    "/settings/memory/owner/shop/shared",
    (device) => [
      ...frame(device, release),
      memoryDialog.getByRole("button", { name: "Save" }),
      memoryDialog.getByText("Run make fmt before each commit and each push.").first(),
    ],
    openMemoryEdit,
  );
  await screenshot(
    page,
    "memory-edit-error",
    "/settings/memory/owner/shop/shared",
    (device) => [...frame(device, release), memoryDialog.getByText("the maximum is 200")],
    tooLong,
  );
  await screenshot(
    page,
    "memory-edit-discard",
    "/settings/memory/owner/shop/shared",
    (device) => [
      ...frame(device, release),
      memoryDiscard.getByRole("button", { name: "Keep editing" }),
      memoryDiscard.getByRole("button", { name: "Discard" }),
    ],
    async () => {
      await openMemoryEdit();
      const field = memoryDialog.getByLabel("Memory text");
      await field.fill(`${await field.inputValue()}Water the roses.\n`);
      await memoryDialog.getByRole("button", { name: "Cancel" }).click();
    },
    async () => {
      await memoryDiscard.getByRole("button", { name: "Discard" }).click();
      await expect(memoryDialog).toBeHidden();
    },
  );
  // The legend text wraps again when the font loads. With the final legend height, the legend wrapper is below the
  // day labels by its padding. A negative gap means that the plot area still has an older, smaller legend height.
  const legendGap = `(() => {
    const label = document.querySelector('.recharts-xAxis-tick-labels text');
    const legend = document.querySelector('.recharts-legend-wrapper');
    if (!label || !legend) return 1e9;
    return legend.getBoundingClientRect().top - label.getBoundingClientRect().bottom;
  })()`;
  const legendSettled = async () => {
    await expect(page.locator(".recharts-legend-wrapper")).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    await expect
      .poll(async () => {
        const gap = await page.evaluate<number>(legendGap);
        return gap >= 0 && gap < 12;
      })
      .toBe(true);
  };
  const usageDays = ["Oct 9", "Oct 10", "Oct 11", "Oct 12", "Oct 13", "Oct 14", "Oct 15"];
  // The 7 days that end on the fixed date have the turns of three models in two repositories. devin has no cost.
  await screenshot(
    page,
    "usage",
    "/usage?group=%5B%22model%22%5D",
    (device) => [
      ...frame(device, release),
      main.getByRole("tab", { name: "Cost", selected: true }),
      main.getByRole("row", { name: /opus \d 23,000 6,400 330,000 37,000 \$5\.55$/ }),
      main.getByRole("row", { name: /swe-1\.5 3 .* —$/ }),
      main.locator(".recharts-bar-rectangle").first(),
      ...usageDays.map((day) => main.getByText(day, { exact: true })),
    ],
    legendSettled,
  );
  await screenshot(
    page,
    "usage-model-effort",
    "/usage?group=%5B%22model%22%2C%22effort%22%5D",
    (device) => [
      ...frame(device, release),
      main.getByRole("columnheader", { name: "Model · Effort" }),
      main.getByRole("rowheader", { name: "opus · medium", exact: true }),
      main.getByRole("rowheader", { name: "sonnet · high", exact: true }),
      main.getByRole("rowheader", { name: "sonnet · low", exact: true }),
      main.getByRole("rowheader", { name: "swe-1.5 · —", exact: true }),
      main.locator(".recharts-bar-rectangle").first(),
      ...usageDays.map((day) => main.getByText(day, { exact: true })),
    ],
    legendSettled,
  );
  await screenshot(
    page,
    "usage-tokens",
    "/usage?tab=tokens&group=%5B%22harness%22%5D&role=%5B%22implementer%22%5D",
    (device) => [
      ...frame(device, release),
      main.getByRole("tab", { name: "Tokens", selected: true }),
      main.getByRole("row", { name: /devin 3 49,000 12,100 156,000 17,000 —$/ }),
      main.locator(".recharts-bar-rectangle").first(),
      ...usageDays.map((day) => main.getByText(day, { exact: true })),
    ],
  );
  // The engine writes the times of its own steps and states at the real time, which can be in the 7 days.
  expect((await page.request.post("/e2e/times/fix")).ok()).toBe(true);
  // The totals of the tab "Times" fill the first screen, so a second screenshot shows the chart and the table.
  const timesReady = (device: string) => [
    ...frame(device, release),
    main.getByRole("tab", { name: "Times", selected: true }),
    main.getByRole("row", { name: /devin Step: check 3 29m 50s 9m 57s$/ }),
    main.locator(".recharts-bar-rectangle").first(),
    ...usageDays.map((day) => main.getByText(day, { exact: true })),
  ];
  const timesPath = "/usage?tab=times&group=%5B%22harness%22%5D&stat=step:check";
  await screenshot(page, "usage-times", timesPath, timesReady);
  await screenshot(page, "usage-times-chart", timesPath, timesReady, async () => {
    await main.locator(".recharts-wrapper").scrollIntoViewIfNeeded();
  });
  const skillsFrame = (device: string) => [
    ...frame(device, release),
    main.getByRole("tab", { name: "Skills", selected: true }),
  ];
  await screenshot(page, "usage-skills", "/usage?tab=skills", (device) => [
    ...skillsFrame(device),
    main.getByRole("row", { name: /^mobius-local-state Mobius 5 4 4$/ }),
    main.getByRole("row", { name: /^write-issue Mobius 0 4 0$/ }),
    main.getByRole("row", { name: /^commit Harness 3 — 2$/ }),
    main.getByRole("row", { name: /^frontend-design Harness 2 — 2$/ }),
  ]);
  await screenshot(
    page,
    "usage-skills-role",
    "/usage?tab=skills&group=%5B%22role%22%5D",
    (device) => [
      ...skillsFrame(device),
      main.getByRole("columnheader", { name: "Role" }),
      main.getByRole("row", { name: /^mobius-local-state Mobius lead_chat 3 2 2$/ }),
      main.getByRole("row", { name: /^mobius-local-state Mobius implementer 2 2 2$/ }),
      main.getByRole("row", { name: /^write-issue Mobius lead_chat 0 2 0$/ }),
      main.getByRole("row", { name: /^write-issue Mobius implementer 0 2 0$/ }),
    ],
  );
  await screenshot(
    page,
    "usage-group-by",
    "/usage?group=%5B%22harness%22%2C%22role%22%5D",
    () => [],
    async (device) => {
      await main.getByRole("button", { name: "Group by: Harness · Role" }).click();
      if (device === "phone") {
        await expect(page.getByRole("checkbox", { name: "Harness", checked: true })).toBeVisible();
        await expect(page.getByRole("checkbox", { name: "Role", checked: true })).toBeVisible();
      } else {
        await expect(
          page.getByRole("menuitemcheckbox", { name: "Harness", checked: true }),
        ).toBeVisible();
        await expect(
          page.getByRole("menuitemcheckbox", { name: "Role", checked: true }),
        ).toBeVisible();
      }
    },
  );
  // The ended tasks end on Oct 10, Oct 11, Oct 12, Oct 13 and Oct 15 with the models sonnet and swe-1.5. Their first
  // state and their turns are before these days, so the other screens show only their working states.
  const qualityFrame = (device: string) => [
    ...frame(device, release),
    main.getByRole("tab", { name: "Quality", selected: true }),
  ];
  // The table is below the first screen, so the step scrolls the last row into the viewport. The row header is narrow,
  // so the scroll stays vertical. A full row would scroll the overflow box to the right on a phone.
  const showRow = (name: RegExp) => async () => {
    await main.getByRole("row", { name }).getByRole("rowheader").scrollIntoViewIfNeeded();
    await expect(main.getByRole("table").locator("..")).toHaveJSProperty("scrollLeft", 0);
  };
  await screenshot(
    page,
    "usage-quality",
    "/usage?tab=quality",
    (device) => [
      ...qualityFrame(device),
      main.getByRole("row", { name: /^All 5 60%/ }),
      ...usageDays.map((day) => main.getByText(day, { exact: true })),
    ],
    showRow(/^All 5 60%/),
  );
  await screenshot(
    page,
    "usage-quality-model",
    "/usage?tab=quality&group=%5B%22model%22%5D",
    (device) => [
      ...qualityFrame(device),
      main.getByRole("columnheader", { name: "Model" }),
      main.getByRole("row", { name: /^sonnet 3 100% 1\.3 2\.0/ }),
      main.getByRole("row", { name: /^swe-1\.5 2 0% 1\.5 0\.5/ }),
      main.locator(".recharts-legend-wrapper").getByText("sonnet", { exact: true }),
      main.locator(".recharts-legend-wrapper").getByText("swe-1.5", { exact: true }),
      ...usageDays.map((day) => main.getByText(day, { exact: true })),
    ],
    showRow(/^swe-1\.5 2 0% 1\.5 0\.5/),
  );
  await page.setViewportSize(viewports.phone);
  await page.goto("/usage?group=%5B%22model%22%5D");
  await expect(main.getByRole("row", { name: /swe-1\.5/ })).toBeVisible();
  expect(await page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")).toBe(
    true,
  );
  await page.goto("/usage?tab=skills&group=%5B%22role%22%5D");
  await expect(main.getByRole("row", { name: /^write-issue Mobius lead_chat/ })).toBeVisible();
  expect(await page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")).toBe(
    true,
  );

  // The fake Coordinator makes the actions with tool calls. The engine writes their rows at the real time.
  const waits = (count: number) => page.getByRole("heading", { name: `Waits for you (${count})` });
  const row = (title: string) => main.getByRole("listitem").filter({ hasText: title });
  const panel = page.getByRole("dialog");
  const note = panel.getByRole("textbox", { name: "Note for the Coordinator" });
  const fixActionTimes = async () => {
    await seen(page);
    expect((await page.request.post("/e2e/approvals/times/fix")).ok()).toBe(true);
  };
  const approvalReady = (device: string, count: string) => frame(device, release, {}, count);
  const answer = (title: string, text: string) => async () => {
    await row(title).getByRole("button", { name: "Answer" }).click();
    await note.fill(text);
  };
  const closePanel = async () => {
    await note.fill("");
    await page.keyboard.press("Escape");
  };
  // The chat scrolls to its end when it opens and when a message comes, so the end is the only stable place. The
  // fake Coordinator ends each of its calls with the message that gives the ID of the action.
  const showEnd = (text: string) => async () => {
    await expect(cardOf(page, text)).toBeVisible();
    await expect
      .poll(() =>
        cardOf(page, text).evaluate((el) => {
          const list = el.parentElement;
          if (
            !list ||
            !/Its ID is \d+, only for withdraw\.$/.test(list.lastElementChild?.textContent ?? "")
          ) {
            return false;
          }
          list.scrollTop = list.scrollHeight;
          return list.scrollHeight - list.scrollTop - list.clientHeight < 1;
        }),
      )
      .toBe(true);
  };

  await page.setViewportSize(viewports.desktop);
  await page.goto("/chat");
  await ask(page, dropColumn);
  await expect(waits(1)).toBeVisible({ timeout: 30_000 });
  await ask(page, draftWorkstream);
  await expect(waits(2)).toBeVisible({ timeout: 30_000 });
  await fixActionTimes();
  await screenshot(
    page,
    "action-tray",
    "/chat",
    (device) => [
      ...approvalReady(device, "3"),
      waits(2),
      cardOf(page, createTitle).getByText("Waits for the approval"),
      cardOf(page, createTitle).getByRole("button", { name: "Answer" }),
      cardOf(page, leadTitle).getByRole("button", { name: "Answer" }),
      row(leadTitle).getByRole("button", { name: "Answer" }),
    ],
    showEnd(createTitle),
  );
  await screenshot(
    page,
    "action-approve",
    "/chat",
    (device) => [
      ...approvalReady(device, "3"),
      panel.getByText("A Playwright test buys a gift card"),
      note,
      panel.getByRole("button", { name: "Approve" }),
    ],
    async () => {
      await showEnd(createTitle)();
      await answer(createTitle, "Go ahead. Start with the table.")();
    },
    closePanel,
  );
  await screenshot(
    page,
    "action-labels",
    "/chat",
    (device) => [
      ...approvalReady(device, "3"),
      panel.getByText("mobius:maintenance"),
      panel.getByText("mobius:evergreen"),
      panel.getByText("Gift cards", { exact: true }),
    ],
    async () => {
      await showEnd(createTitle)();
      await answer(createTitle, "Go ahead. Start with the table.")();
      await expect
        .poll(() =>
          panel.locator("div.overflow-y-auto").evaluate((el) => {
            const chip = [...el.querySelectorAll("span")].find(
              (span) => span.textContent === "mobius:maintenance",
            );
            chip?.scrollIntoView({ block: "center" });
            if (!chip) return false;
            const box = el.getBoundingClientRect();
            const found = chip.getBoundingClientRect();
            return found.top >= box.top && found.bottom <= box.bottom;
          }),
        )
        .toBe(true);
    },
    closePanel,
  );
  await screenshot(
    page,
    "action-reject",
    "/chat",
    (device) => [
      ...approvalReady(device, "3"),
      panel.getByText("Wait until that migration is in the main branch."),
      note,
      panel.getByRole("button", { name: "Reject" }),
    ],
    async () => {
      await showEnd(createTitle)();
      await answer(leadTitle, "Wait for the migration of #12.")();
    },
    closePanel,
  );
  await screenshot(page, "action-inbox", "/inbox", (device) => [
    ...frame(device, release, { organizations: true }, "3"),
    main.getByRole("tab", { name: "To do 3" }),
    row(createTitle),
    row(leadTitle),
  ]);

  // The Lead of #9999 does not exist.
  await page.goto("/chat");
  await ask(page, stopUnknown);
  await expect(waits(3)).toBeVisible({ timeout: 30_000 });
  await fixActionTimes();
  await screenshot(
    page,
    "action-failed",
    "/chat",
    (device) => [
      ...approvalReady(device, "4"),
      panel.getByRole("alert").getByText(/#9999 is not an open Workstream/),
      cardOf(page, unknownTitle).getByText("Failed", { exact: true }),
      cardOf(page, unknownTitle).getByText(/#9999 is not an open Workstream/),
    ],
    async () => {
      await answer(unknownTitle, "Go ahead.")();
      await panel.getByRole("button", { name: "Approve" }).click();
      await expect(panel.getByRole("alert")).toBeVisible();
      await page.mouse.move(0, 0);
      await idle(page);
      await showEnd(unknownTitle)();
    },
    closePanel,
  );
  await screenshot(
    page,
    "action-failed-card",
    "/chat",
    (device) => [
      ...approvalReady(device, "4"),
      cardOf(page, unknownTitle).getByText("Failed", { exact: true }),
      cardOf(page, unknownTitle).getByText(/#9999 is not an open Workstream/),
      cardOf(page, unknownTitle).getByRole("button", { name: "Answer" }),
    ],
    async () => {
      await expect(panel).toBeHidden();
      await showEnd(unknownTitle)();
      await expect
        .poll(() =>
          cardOf(page, unknownTitle).evaluate((el) => {
            el.scrollIntoView({ block: "center" });
            const box = el.parentElement?.getBoundingClientRect();
            const found = el.getBoundingClientRect();
            return !!box && found.top >= box.top && found.bottom <= box.bottom;
          }),
        )
        .toBe(true);
    },
  );
  await screenshot(
    page,
    "action-rejected",
    "/chat",
    (device) => [
      ...approvalReady(device, "3"),
      waits(2),
      cardOf(page, unknownTitle).getByText("Rejected", { exact: true }),
      cardOf(page, unknownTitle).getByRole("button", { name: "Details" }),
    ],
    async (device) => {
      if (device === "desktop") {
        await answer(unknownTitle, "Wait for the migration of #12.")();
        await panel.getByRole("button", { name: "Reject" }).click();
        await expect(panel).toBeHidden();
        await skipNotice(page, noticeOf(page, "Rejected: "));
      }
      const card = cardOf(page, unknownTitle);
      await expect(card.getByText("Rejected", { exact: true })).toBeVisible();
      await expect(waits(2)).toBeVisible();
      await showEnd(unknownTitle)();
    },
  );
  const unknownAction = await cardOf(page, unknownTitle).getAttribute("data-action");
  const setUnknownAction = async (state: string, message: string) => {
    const query = new URLSearchParams({ state, message });
    expect((await page.request.put(`/e2e/actions/${unknownAction}/state?${query}`)).ok()).toBe(
      true,
    );
  };
  await setUnknownAction("withdrawn", "");
  await screenshot(
    page,
    "action-withdrawn",
    "/chat",
    (device) => [
      ...approvalReady(device, "3"),
      waits(2),
      cardOf(page, unknownTitle).getByText("Withdrawn by the agent", { exact: true }),
      cardOf(page, unknownTitle).getByRole("button", { name: "Details" }),
    ],
    showEnd(unknownTitle),
  );
  await setUnknownAction("rejected", "Wait for the migration of #12.");

  // The fake Coordinator asks a question with options. The answer ends the question.
  await page.goto("/chat");
  await ask(page, askStore);
  await expect(waits(3)).toBeVisible({ timeout: 30_000 });
  await fixActionTimes();
  const group = (name: string) => panel.getByRole("group", { name });
  const option = (name: string, title: string) => group(name).getByRole("button", { name: title });
  await screenshot(
    page,
    "answer-options",
    "/chat",
    (device) => [
      ...approvalReady(device, "4"),
      group(storeQuestion).getByRole("button", { name: "SQLite", pressed: true }),
      group(checksQuestion).getByRole("button", { name: "Tests", pressed: true }),
      option(checksQuestion, "Types"),
    ],
    async () => {
      await row(storeQuestion).getByRole("button", { name: "Answer" }).click();
      await option(storeQuestion, "SQLite").click();
      await option(checksQuestion, "Tests").click();
      await option(checksQuestion, "Types").scrollIntoViewIfNeeded();
    },
    () => page.keyboard.press("Escape"),
  );
  await row(storeQuestion).getByRole("button", { name: "Answer" }).click();
  await panel.getByRole("textbox", { name: "Your answer" }).fill("Keep the old data.");
  await panel.getByRole("button", { name: "Send" }).click();
  await expect(panel).toBeHidden();

  // The cards of the questions. The end of the chat is the only stable scroll, and the Coordinator ends each turn with
  // its message.
  const questionCard = (text: string) =>
    page.locator("[data-question]").filter({ hasText: text }).last();
  const showQuestionEnd = (text: string) => async () => {
    await expect(questionCard(text)).toBeVisible();
    await idle(page);
    await expect
      .poll(() =>
        questionCard(text).evaluate((el) => {
          const list = el.parentElement;
          if (!list) {
            return false;
          }
          list.scrollTop = list.scrollHeight;
          return list.scrollHeight - list.scrollTop - list.clientHeight < 1;
        }),
      )
      .toBe(true);
  };
  await row(leadTitle).getByRole("button", { name: "Answer" }).click();
  await decide(page, panel, "reject");
  await expect(panel).toBeHidden();
  await settle(page);
  expect((await page.request.post("/e2e/approvals/times/fix")).ok()).toBe(true);

  const storeCard = "The shop needs a store for the plans.";
  await expect(questionCard(storeCard).getByText("Answered", { exact: true })).toBeVisible();
  await fixActionTimes();
  await screenshot(
    page,
    "question-answered",
    "/chat",
    (device) => [
      ...approvalReady(device, "1"),
      questionCard(storeCard).getByText("Answered", { exact: true }),
      questionCard(storeCard).getByText("Keep the old data."),
      questionCard(storeCard).getByRole("button", { name: "Details" }),
    ],
    showQuestionEnd(storeCard),
  );
  await screenshot(
    page,
    "question-details",
    "/chat",
    (device) => [
      ...approvalReady(device, "1"),
      panel.getByText(storeCard),
      group(storeQuestion),
      panel.getByText("Your answer", { exact: true }),
      panel.getByText("Keep the old data."),
    ],
    async () => {
      await showQuestionEnd(storeCard)();
      await questionCard(storeCard).getByRole("button", { name: "Details" }).click();
    },
    () => page.keyboard.press("Escape"),
  );

  // The fake Coordinator asks whether to draft the Workstream, and withdraws the question after the screenshot.
  await page.goto("/chat");
  await ask(page, askDraft);
  await expect(waits(1)).toBeVisible({ timeout: 30_000 });
  await fixActionTimes();
  await screenshot(
    page,
    "question-open",
    "/chat",
    (device) => [
      ...approvalReady(device, "2"),
      waits(1),
      questionCard(draftQuestion).getByText("Waits for your answer", { exact: true }),
      questionCard(draftQuestion).getByRole("button", { name: "Answer" }),
    ],
    showQuestionEnd(draftQuestion),
  );
  await ask(page, withdrawDraft);
  await expect(waits(1)).toBeHidden({ timeout: 30_000 });
  await fixActionTimes();
  await screenshot(
    page,
    "question-withdrawn",
    "/chat",
    (device) => [
      ...approvalReady(device, "1"),
      questionCard(draftQuestion).getByText("Withdrawn", { exact: true }),
    ],
    showQuestionEnd(draftQuestion),
  );

  // The title of the question has one long word. The sheet must wrap it and stay inside the screen.
  await page.unroute(inboxRoute);
  await page.route(inboxRoute, (route) =>
    route.fulfill({ json: { data: [longWordQuestionItem] } }),
  );
  await screenshot(
    page,
    "question-long-title",
    "/chat",
    () => [
      panel.getByText(longWordTitle),
      panel.getByText("The shop needs a store for the plans."),
      group("Which store?"),
      option("Which store?", "SQLite"),
      option("Which store?", "Postgres"),
    ],
    async () => {
      await row(longWordTitle).getByRole("button", { name: "Answer" }).click();
      await expect(panel).toBeVisible();
    },
    () => page.keyboard.press("Escape"),
  );
  await page.unroute(inboxRoute);

  // The fake Lead of #14 proposes a new Brief. The approval changes the Brief, and the last call sets it back.
  await page.goto("/workstreams/owner/shop/14");
  await sendToLead(page, editBrief);
  await expect(waits(1)).toBeVisible({ timeout: 30_000 });
  await fixActionTimes();
  const openBrief = async () => {
    await showEnd(briefTitle)();
    await row(briefTitle).getByRole("button", { name: "Answer" }).click();
    await expect(addedText(panel)).toHaveCount(2);
  };
  await screenshot(
    page,
    "action-brief-hidden",
    "/workstreams/owner/shop/14",
    (device) => [
      ...approvalReady(device, "2"),
      addedText(panel).first(),
      addedText(panel).last(),
      removedSwitch(panel),
    ],
    async () => {
      await openBrief();
      await expect(removedSwitch(panel)).not.toBeChecked();
      await expect(removedText(panel)).toHaveCount(0);
    },
    () => page.keyboard.press("Escape"),
  );
  await screenshot(
    page,
    "action-brief-diff",
    "/workstreams/owner/shop/14",
    (device) => [
      ...approvalReady(device, "2"),
      removedText(panel).first(),
      removedText(panel).last(),
      addedText(panel).first(),
      addedText(panel).last(),
      removedSwitch(panel),
    ],
    async () => {
      await openBrief();
      await removedSwitch(panel).click();
      await expect(removedSwitch(panel)).toBeChecked();
      await expect(removedText(panel)).toHaveCount(2);
    },
    () => page.keyboard.press("Escape"),
  );
  await row(briefTitle).getByRole("button", { name: "Answer" }).click();
  await decide(page, panel, "approve");
  await expect(panel).toBeHidden();
  await expect(waits(1)).toBeHidden();
  const briefCard = () => cardOf(page, briefTitle);
  const unreadBrief = () =>
    page.evaluate(`fetch("/api/unread")
      .then((response) => response.json())
      .then(({ data }) => data.some((chat) => chat.repository === "owner/shop" && chat.workstream === 14 && chat.count > 0))`);
  const showBriefCard = async () => {
    const card = briefCard();
    await expect(card.getByText("Done", { exact: true })).toBeVisible();
    await expect(card.getByRole("button", { name: "Details" })).toBeVisible();
    await expect.poll(unreadBrief).toBe(false);
    await expect
      .poll(() =>
        card.evaluate((el) => {
          const list = el.parentElement;
          if (!list) {
            return false;
          }
          list.scrollTop = list.scrollHeight;
          return list.scrollHeight - list.scrollTop - list.clientHeight < 1;
        }),
      )
      .toBe(true);
  };
  await page.goto("/workstreams/owner/shop/14");
  await expect(briefCard().getByText("Done", { exact: true })).toBeVisible();
  await fixActionTimes();
  await screenshot(
    page,
    "action-brief-done",
    "/workstreams/owner/shop/14",
    (device) => [
      ...approvalReady(device, "1"),
      briefCard().getByText("Done", { exact: true }),
      briefCard().getByRole("button", { name: "Details" }),
    ],
    showBriefCard,
  );
  const openBriefDetails = async () => {
    await showBriefCard();
    await briefCard().getByRole("button", { name: "Details" }).click();
    await expect(addedText(panel)).toHaveCount(2);
  };
  await screenshot(
    page,
    "action-brief-details",
    "/workstreams/owner/shop/14",
    (device) => [
      ...approvalReady(device, "1"),
      addedText(panel).first(),
      addedText(panel).last(),
      removedSwitch(panel),
    ],
    async () => {
      await openBriefDetails();
      await expect(removedSwitch(panel)).not.toBeChecked();
      await expect(removedText(panel)).toHaveCount(0);
    },
    () => page.keyboard.press("Escape"),
  );
  await screenshot(
    page,
    "action-brief-details-all",
    "/workstreams/owner/shop/14",
    (device) => [
      ...approvalReady(device, "1"),
      removedText(panel).first(),
      removedText(panel).last(),
      addedText(panel).first(),
      addedText(panel).last(),
      removedSwitch(panel),
    ],
    async () => {
      await openBriefDetails();
      await removedSwitch(panel).click();
      await expect(removedSwitch(panel)).toBeChecked();
      await expect(removedText(panel)).toHaveCount(2);
    },
    () => page.keyboard.press("Escape"),
  );
  await restoreBrief(page);
  await settle(page);

  // The fake Lead of #14 sends a message to the Lead of #12. The Owner rejects it after the screenshot.
  await page.goto("/workstreams/owner/shop/14");
  await sendToLead(page, messageLeadOfTwelve);
  await expect(waits(1)).toBeVisible({ timeout: 30_000 });
  await fixActionTimes();
  await screenshot(
    page,
    "action-lead-sender",
    "/workstreams/owner/shop/14",
    (device) => [
      ...approvalReady(device, "2"),
      panel.getByRole("link", { name: "#14 Early renewals" }),
      panel.getByText(waitText),
      panel.getByRole("textbox", { name: "Note for the Lead" }),
      panel.getByRole("button", { name: "Reject" }),
    ],
    async () => {
      await showEnd(leadToLeadTitle)();
      await row(leadToLeadTitle).getByRole("button", { name: "Answer" }).click();
    },
    () => page.keyboard.press("Escape"),
  );
  await row(leadToLeadTitle).getByRole("button", { name: "Answer" }).click();
  await decide(page, panel, "reject");
  await expect(panel).toBeHidden();
  await expect(waits(1)).toBeHidden();
  await settle(page);

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
  const withCard = main.locator("[data-message]", { hasText: "This is the new plan page." });
  await screenshot(
    page,
    "chat-history-inbox-item",
    "/workstreams/plants/garden/25",
    (device) => [
      ...(device === "desktop"
        ? [release, page.getByLabel("Work in another organization")]
        : [page.getByLabel("Upgrade available")]),
      withCard.getByRole("link", { name: /Integrate loyalty plans/ }),
      withCard.getByText("PR #44 is ready to merge"),
    ],
    async () => {
      await expect(pictures).toHaveCount(3);
      for (let position = 0; position < 3; position++) {
        await expect(pictures.nth(position)).toHaveJSProperty("naturalWidth", 200);
      }
      await withCard.evaluate((el) => el.scrollIntoView({ block: "start" }));
    },
  );
  const plantsFrame = (device: string, workstream: number) =>
    device === "desktop"
      ? [
          release,
          page.getByLabel("Work in another organization"),
          page.locator(`nav a[href="/workstreams/plants/garden/${workstream}"]`),
        ]
      : [page.getByLabel("Upgrade available")];
  // The chat tests of the later projects need the first unread message of these chats.
  await page.route("/api/chat/seen", (route) => route.fulfill({ status: 204 }));
  await screenshot(
    page,
    "chat-not-delivered",
    "/workstreams/plants/garden/14",
    (device) => [
      ...plantsFrame(device, 14),
      main.getByLabel("Not delivered, the agent failed, at 09:30"),
    ],
    async () => {
      await expect(main.getByText("It shows this crossed O icon. Should be a tick.")).toBeVisible();
      await showChatEnd(page);
    },
  );
  await screenshot(
    page,
    "chat-cancelled",
    "/workstreams/plants/garden/15",
    (device) => [
      ...plantsFrame(device, 15),
      main.getByLabel("Cancelled, sent again below, at 09:30"),
      main.getByText("Plan the tiers for the shop.").first(),
    ],
    async () => {
      await expect(main.getByText("Plan the tiers for the shop.")).toHaveCount(2);
      await showChatEnd(page);
    },
  );
  await page.unroute("/api/chat/seen");
});
