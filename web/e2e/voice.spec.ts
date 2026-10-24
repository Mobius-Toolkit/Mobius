import { expect, test, type Page } from "@playwright/test";
import { countSamples, fakeMicrophone, fakeWakeLock } from "./microphone.js";

const shop = "/workstreams/owner/shop/12";
const seasonal = "/workstreams/owner/shop/14";
const taken = "The recording stopped because another app took the microphone.";
const background = "The recording stopped because the app went to the background.";

test.beforeEach(async ({ page }) => {
  await page.addInitScript(fakeMicrophone);
  await page.addInitScript(countSamples);
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const answer = (page: Page, text: string) =>
  page.route("**/api/voice/transcriptions", (route) => route.fulfill({ json: { data: { text } } }));

const start = (page: Page) =>
  page.getByRole("main").getByRole("button", { name: "Start voice input", exact: true });
const stop = (page: Page) =>
  page.getByRole("main").getByRole("button", { name: "Stop voice input", exact: true });

// The first sample shows that the recorder runs and that the stop events have listeners.
const begin = async (page: Page) => {
  await start(page).click();
  await expect.poll(() => page.evaluate("samples")).toBeGreaterThan(0);
};

const track = (page: Page, event: string) =>
  page.evaluate(`microphones.at(-1).getAudioTracks()[0].dispatchEvent(new Event('${event}'))`);

const trackState = (page: Page) =>
  page.evaluate("microphones.map((stream) => stream.getAudioTracks()[0].readyState)");

// The history change opens the page with no reload, so the page keeps its recording.
const open = (page: Page, path: string) =>
  page.evaluate(
    `history.pushState({}, '', '${path}'); dispatchEvent(new PopStateEvent('popstate'))`,
  );

test("a recording adds the text of the answer to the message", async ({ page }) => {
  await answer(page, " red roses ");
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await input.fill("Plant");
  await begin(page);
  await stop(page).click();
  await expect(input).toHaveValue("Plant red roses");
  await expect(start(page)).toBeVisible();
  expect(await trackState(page)).toEqual(["ended"]);
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("the request has a WAV file with 16 kHz mono 16-bit samples", async ({ page }) => {
  let body: Buffer | null = null;
  let type: string | undefined;
  await page.route("**/api/voice/transcriptions", (route) => {
    body = route.request().postDataBuffer();
    type = route.request().headers()["content-type"];
    return route.fulfill({ json: { data: { text: "red roses" } } });
  });
  await page.goto(shop);
  await begin(page);
  await stop(page).click();
  await expect(page.getByLabel("Message to the Lead")).toHaveValue("red roses");
  expect(type).toBe("audio/wav");
  const wav = body!;
  expect(wav.toString("ascii", 0, 4)).toBe("RIFF");
  expect(wav.toString("ascii", 8, 12)).toBe("WAVE");
  expect(wav.readUInt16LE(20)).toBe(1);
  expect(wav.readUInt16LE(22)).toBe(1);
  expect(wav.readUInt32LE(24)).toBe(16000);
  expect(wav.readUInt16LE(34)).toBe(16);
  expect(wav.toString("ascii", 36, 40)).toBe("data");
  expect(wav.readUInt32LE(40)).toBe(wav.length - 44);
  expect(wav.length).toBeGreaterThan(44);
});

test("the text goes in at the cursor when the field has the focus", async ({ page }) => {
  await answer(page, "red");
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await input.fill("Plant roses");
  await input.focus();
  await input.evaluate((field: HTMLTextAreaElement) => field.setSelectionRange(5, 5));
  await begin(page);
  await stop(page).click();
  await expect(input).toHaveValue("Plant red roses");
});

test("the text goes in at the end when the field has no focus", async ({ page }) => {
  await answer(page, "red");
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await input.fill("Plant");
  await input.evaluate((field: HTMLTextAreaElement) => field.setSelectionRange(0, 0));
  await page.getByRole("main").click({ position: { x: 1, y: 1 } });
  await begin(page);
  await stop(page).click();
  await expect(input).toHaveValue("Plant red");
});

test("the field scrolls to the last words of the voice text", async ({ page }) => {
  await answer(page, "word ".repeat(300).trim());
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await stop(page).click();
  await expect(input).toHaveValue(/word$/);
  expect(
    await input.evaluate((field) => field.scrollTop + field.clientHeight >= field.scrollHeight - 2),
  ).toBe(true);
});

test("the converting state shows until the answer comes", async ({ page }) => {
  let requests = 0;
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/voice/transcriptions", async (route) => {
    requests++;
    await held;
    await route.fulfill({ json: { data: { text: "red roses" } } });
  });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await stop(page).click();
  const converting = page.getByRole("button", { name: "Converting the voice input" });
  await expect(converting).toBeDisabled();
  await expect(converting.locator("[data-slot=spinner]")).toBeVisible();
  await expect(start(page)).toHaveCount(0);
  await expect(stop(page)).toHaveCount(0);
  await converting.dispatchEvent("click");
  await expect(input).toHaveValue("");
  release();
  await expect(input).toHaveValue("red roses");
  await expect(start(page)).toBeVisible();
  expect(requests).toBe(1);
});

test("the button shows an icon, a name and its state", async ({ page }) => {
  await answer(page, "red");
  await page.goto(shop);
  const mic = start(page);
  await expect(mic).toHaveAttribute("aria-pressed", "false");
  await expect(mic).toHaveAttribute("title", "Start voice input");
  await expect(mic.locator("svg")).toHaveCount(1);
  await expect(mic).toHaveText("");
  await expect(mic).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");

  await mic.click();
  const recording = stop(page);
  await expect(recording).toHaveAttribute("aria-pressed", "true");
  await expect(recording).toHaveAttribute("title", "Stop voice input");
  await expect(recording.locator("svg")).toHaveCount(1);
  await expect(recording).toHaveText("");
  await expect(recording).not.toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
});

type Live = { committed: string; draft: string };

// The server keeps the recording r1. Each chunk gets the next answer of answers, and the last answer repeats. A number
// is the status of an error answer. finish is the answer of the end of the recording.
const liveServer = async (
  page: Page,
  answers: (Live | number)[],
  finish: { status?: number; text?: string } = {},
) => {
  const seen = { sequences: [] as string[], sizes: [] as number[], finishes: 0 };
  await page.route("**/api/voice/recordings", (route) =>
    route.fulfill({ json: { data: { id: "r1" } } }),
  );
  await page.route("**/api/voice/recordings/r1/chunks?*", (route) => {
    const request = route.request();
    seen.sequences.push(new URL(request.url()).searchParams.get("sequence") ?? "");
    seen.sizes.push(request.postDataBuffer()?.length ?? 0);
    const reply = answers[Math.min(seen.sequences.length, answers.length) - 1];
    return typeof reply === "number"
      ? route.fulfill({ status: reply, json: { error: "The draft failed" } })
      : route.fulfill({ json: { data: reply } });
  });
  await page.route("**/api/voice/recordings/r1/finish", (route) => {
    seen.finishes++;
    return route.fulfill(
      finish.status
        ? { status: finish.status, json: { error: finish.text } }
        : { json: { data: { text: finish.text } } },
    );
  });
  return seen;
};

// The clock stands still, so a tick of the live text comes only after tick.
const tick = (page: Page) => page.clock.runFor(2000);

test("the live text shows during the recording and the final text replaces it", async ({
  page,
}) => {
  await page.clock.install();
  const seen = await liveServer(
    page,
    [
      { committed: "", draft: "red" },
      { committed: "red", draft: "roses" },
    ],
    { text: "Red roses, please." },
  );
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await input.fill("Please");
  await begin(page);
  await tick(page);
  await expect(input).toHaveValue("Please red");
  await tick(page);
  await expect(input).toHaveValue("Please red roses");
  await stop(page).click();
  await expect(input).toHaveValue("Please Red roses, please.");
  await expect(start(page)).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(seen.sequences.map(Number)).toEqual(seen.sequences.map((_, i) => i));
  expect(seen.sequences.length).toBeGreaterThanOrEqual(2);
  expect(seen.sizes.every((size) => size > 0 && size % 2 === 0)).toBe(true);
  expect(seen.finishes).toBe(1);
});

test("text after the voice text stays when the live text changes", async ({ page }) => {
  await page.clock.install();
  await liveServer(
    page,
    [
      { committed: "", draft: "red" },
      { committed: "red", draft: "roses" },
    ],
    { text: "Red roses" },
  );
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await input.fill("Plant now");
  await input.focus();
  await input.evaluate((field: HTMLTextAreaElement) => field.setSelectionRange(5, 5));
  await begin(page);
  await tick(page);
  await expect(input).toHaveValue("Plant red now");
  await tick(page);
  await expect(input).toHaveValue("Plant red roses now");
  await stop(page).click();
  await expect(input).toHaveValue("Plant Red roses now");
});

test("a change of the field during the recording stops the live text", async ({ page }) => {
  await page.clock.install();
  const seen = await liveServer(
    page,
    [
      { committed: "", draft: "red" },
      { committed: "red", draft: "roses" },
    ],
    { text: "Red roses" },
  );
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await tick(page);
  await expect(input).toHaveValue("red");
  await input.fill("red!");
  await tick(page);
  await expect.poll(() => seen.sequences.length).toBe(2);
  await expect(input).toHaveValue("red!");
  await stop(page).click();
  await expect(input).toHaveValue("red! Red roses");
  await expect(start(page)).toBeVisible();
});

test("a failed chunk keeps the recording and the stop sends the file for the text", async ({
  page,
}) => {
  await page.clock.install();
  const seen = await liveServer(page, [{ committed: "", draft: "red" }, 500]);
  let files = 0;
  await page.route("**/api/voice/transcriptions", (route) => {
    files++;
    return route.fulfill({ json: { data: { text: "red roses" } } });
  });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await tick(page);
  await expect(input).toHaveValue("red");
  await tick(page);
  await expect.poll(() => seen.sequences.length).toBe(2);
  await tick(page);
  await expect(stop(page)).toBeVisible();
  expect(seen.sequences.length).toBe(2);
  await stop(page).click();
  await expect(input).toHaveValue("red roses");
  await expect(start(page)).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(files).toBe(1);
  expect(seen.finishes).toBe(0);
});

test("a failed conversion keeps the audio and Send again sends it", async ({ page }) => {
  await page.clock.install();
  const seen = await liveServer(page, [{ committed: "", draft: "red" }], {
    status: 503,
    text: "The speech model is still starting",
  });
  const files: Buffer[] = [];
  await page.route("**/api/voice/transcriptions", (route) => {
    files.push(route.request().postDataBuffer() ?? Buffer.alloc(0));
    return files.length === 1
      ? route.fulfill({ status: 502, json: { error: "The converter returned an error" } })
      : route.fulfill({ json: { data: { text: "red roses" } } });
  });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  const again = page.getByRole("button", { name: "Send again" });
  await begin(page);
  await tick(page);
  await expect(input).toHaveValue("red");
  await stop(page).click();
  await expect(page.getByRole("alert")).toHaveText("The speech model is still starting");
  await expect(again).toBeVisible();
  expect(files.length).toBe(0);
  expect(seen.finishes).toBe(1);

  await again.click();
  await expect(page.getByRole("alert")).toHaveText("The converter returned an error");
  await expect(again).toBeVisible();
  await expect(input).toHaveValue("red");

  await again.click();
  await expect(input).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(again).toHaveCount(0);
  expect(files.length).toBe(2);
  expect(files[0].subarray(0, 4).toString()).toBe("RIFF");
  expect(files[0].length).toBeGreaterThan(44);
  expect(files[1].equals(files[0])).toBe(true);
});

test("a recording that the server does not start still gets its text at the stop", async ({
  page,
}) => {
  await page.clock.install();
  let chunks = 0;
  await page.route("**/api/voice/recordings", (route) =>
    route.fulfill({ status: 500, json: { error: "failed" } }),
  );
  await page.route("**/api/voice/recordings/*/chunks?*", (route) => {
    chunks++;
    return route.abort();
  });
  await answer(page, "red roses");
  await page.goto("/chat");
  await begin(page);
  await tick(page);
  await stop(page).click();
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  expect(chunks).toBe(0);
});

for (const [status, message] of [
  [503, "The speech model is still downloading"],
  [502, "The converter returned an error"],
] as const) {
  test(`an answer ${status} of the end of the recording shows its message`, async ({ page }) => {
    await page.clock.install();
    let files = 0;
    await liveServer(page, [{ committed: "", draft: "red" }], { status, text: message });
    await page.route("**/api/voice/transcriptions", (route) => {
      files++;
      return route.fulfill({ json: { data: { text: "red roses" } } });
    });
    await page.goto("/chat");
    const input = page.getByLabel("Message to the Coordinator");
    await begin(page);
    await tick(page);
    await expect(input).toHaveValue("red");
    await stop(page).click();
    await expect(page.getByRole("alert")).toHaveText(message);
    await expect(input).toHaveValue("red");
    await expect(start(page)).toBeVisible();
    expect(files).toBe(0);
  });
}

for (const event of ["ended", "mute"]) {
  test(`a track ${event} stops the recording, sends the audio and shows the error`, async ({
    page,
  }) => {
    let requests = 0;
    await page.route("**/api/voice/transcriptions", (route) => {
      requests++;
      return route.fulfill({ json: { data: { text: "red roses" } } });
    });
    await page.goto("/chat");
    const input = page.getByLabel("Message to the Coordinator");
    await begin(page);
    await track(page, event);
    await expect(input).toHaveValue("red roses");
    await expect(page.getByText(taken)).toBeVisible();
    await expect(start(page)).toBeVisible();
    expect(requests).toBe(1);
    expect(await trackState(page)).toEqual(["ended"]);
  });
}

test("a hidden page stops the recording, sends the audio and shows the error", async ({ page }) => {
  await answer(page, "red roses");
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await page.evaluate(
    "Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); document.dispatchEvent(new Event('visibilitychange'))",
  );
  await expect(input).toHaveValue("red roses");
  await expect(page.getByText(background)).toBeVisible();
  await expect(start(page)).toBeVisible();
});

test("a page that hides while the microphone starts stops the recording", async ({ page }) => {
  await liveServer(page, [{ committed: "", draft: "" }], { text: "red roses" });
  await page.goto("/chat");
  await page.evaluate(
    "void (window.microphoneGate = new Promise((resolve) => (window.openMicrophone = resolve)))",
  );
  await start(page).click();
  await page.evaluate(
    "Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); document.dispatchEvent(new Event('visibilitychange')); window.openMicrophone()",
  );
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  await expect(page.getByText(background)).toBeVisible();
  await expect(start(page)).toBeVisible();
  expect(await trackState(page)).toEqual(["ended"]);
});

test("a recording of 10 minutes stops with no error", async ({ page }) => {
  await page.clock.install();
  await answer(page, "red roses");
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await page.clock.runFor(10 * 60 * 1000);
  await expect(input).toHaveValue("red roses");
  await expect(start(page)).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
});

for (const [status, message] of [
  [503, "The speech model is still downloading"],
  [502, "The converter returned an error"],
] as const) {
  test(`an answer ${status} shows its message`, async ({ page }) => {
    await page.route("**/api/voice/transcriptions", (route) =>
      route.fulfill({ status, json: { error: message } }),
    );
    await page.goto("/chat");
    await begin(page);
    await stop(page).click();
    await expect(page.getByRole("alert")).toHaveText(message);
    await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("");
    await expect(start(page)).toBeVisible();
  });
}

test("a failed request shows a network error", async ({ page }) => {
  await page.route("**/api/voice/transcriptions", (route) => route.abort());
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("alert")).toHaveText(
    "The recording did not reach the server. Check the connection and try again.",
  );
  await expect(start(page)).toBeVisible();
});

for (const [name, message] of [
  [
    "NotAllowedError",
    "The browser blocks the microphone. Allow the microphone in the browser settings.",
  ],
  ["NotFoundError", "The browser cannot find a microphone. Connect a microphone and try again."],
  ["AbortError", "The voice input did not start."],
] as const) {
  test(`${name} of the microphone shows its message`, async ({ page }) => {
    await page.goto("/chat");
    await page.evaluate(`window.microphoneError = '${name}'`);
    await start(page).click();
    await expect(page.getByRole("alert")).toHaveText(message);
    await expect(start(page)).toBeVisible();
  });
}

test("a new recording removes the error of the last run", async ({ page }) => {
  await answer(page, "red");
  await page.goto("/chat");
  await page.evaluate("window.microphoneError = 'NotAllowedError'");
  await start(page).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await page.evaluate("window.microphoneError = ''");
  await begin(page);
  await expect(page.getByRole("alert")).toHaveCount(0);
});

const unreachable = "The recording did not reach the server. Check the connection and try again.";
const again = (page: Page) => page.getByRole("button", { name: "Send again" });
const send = (page: Page) =>
  page.getByRole("main").getByRole("button", { name: "Send", exact: true });

// Answers the first request of the route never, and each later request with the text.
const hangFirst = async (page: Page, pattern: string, text: string) => {
  const seen = { requests: 0 };
  await page.route(pattern, (route) => {
    seen.requests++;
    return seen.requests === 1 ? undefined : route.fulfill({ json: { data: { text } } });
  });
  return seen;
};

test("a hung conversion ends after 6 minutes, and Send again gives the text", async ({ page }) => {
  await page.clock.install();
  await page.route("**/api/voice/recordings", (route) =>
    route.fulfill({ status: 500, json: { error: "failed" } }),
  );
  const seen = await hangFirst(page, "**/api/voice/transcriptions", "red roses");
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await stop(page).click();
  await expect.poll(() => seen.requests).toBe(1);
  await page.clock.runFor(6 * 60 * 1000 - 1000);
  await expect(page.getByRole("button", { name: "Converting the voice input" })).toBeVisible();
  await page.clock.runFor(1000);
  await expect(page.getByRole("alert")).toHaveText(unreachable);
  await expect(again(page)).toBeVisible();
  await expect(input).toHaveValue("");
  await again(page).click();
  await expect(input).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("a hung end of the recording shows the error and sends no file", async ({ page }) => {
  await page.clock.install();
  let files = 0;
  await liveServer(page, [{ committed: "", draft: "red" }]);
  const seen = await hangFirst(page, "**/api/voice/recordings/r1/finish", "");
  await page.route("**/api/voice/transcriptions", (route) => {
    files++;
    return route.fulfill({ json: { data: { text: "red roses" } } });
  });
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await expect.poll(() => seen.requests).toBe(1);
  await page.clock.runFor(6 * 60 * 1000);
  await expect(page.getByRole("alert")).toHaveText(unreachable);
  await expect(again(page)).toBeVisible();
  expect(files).toBe(0);
});

test("a hung chunk fails the live text after 30 seconds, and the stop sends the file", async ({
  page,
}) => {
  await page.clock.install();
  await liveServer(page, [{ committed: "", draft: "red" }]);
  let chunks = 0;
  await page.route("**/api/voice/recordings/r1/chunks?*", () => {
    chunks++;
  });
  await answer(page, "red roses");
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await expect
    .poll(async () => {
      await tick(page);
      return chunks;
    })
    .toBe(1);
  await page.clock.runFor(30 * 1000);
  await stop(page).click();
  await expect(input).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("a stopped network shows the error and Send again", async ({ page }) => {
  await page.route("**/api/voice/**", (route) => route.abort());
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("alert")).toHaveText(unreachable);
  await expect(again(page)).toBeVisible();
  await again(page).click();
  await expect(page.getByRole("alert")).toHaveText(unreachable);
  await expect(again(page)).toBeVisible();
});

test("an empty result shows an error and keeps the audio", async ({ page }) => {
  let texts = ["  ", "red roses"];
  await page.route("**/api/voice/transcriptions", (route) =>
    route.fulfill({ json: { data: { text: texts.shift() } } }),
  );
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await input.fill("Plant");
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("alert")).toHaveText("The recording has no text.");
  await expect(again(page)).toBeVisible();
  await expect(input).toHaveValue("Plant");
  await again(page).click();
  await expect(input).toHaveValue("Plant red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(again(page)).toHaveCount(0);
});

test("Send during a recording stops it, and the text goes into the empty field", async ({
  page,
}) => {
  let files = 0;
  await page.route("**/api/voice/transcriptions", (route) => {
    files++;
    return route.fulfill({ json: { data: { text: "red roses" } } });
  });
  await page.route("**/api/chat/messages", () => {});
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await input.fill("Add a plan");
  const sent = page.waitForRequest("**/api/chat/messages");
  await send(page).click();
  await sent;
  await expect(input).toHaveValue("red roses");
  await expect(start(page)).toBeVisible();
  expect(await trackState(page)).toEqual(["ended"]);
  expect(files).toBe(1);
});

test("Send during a conversion keeps it, and the text goes into the empty field", async ({
  page,
}) => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/voice/transcriptions", async (route) => {
    await held;
    await route.fulfill({ json: { data: { text: "red roses" } } });
  });
  await page.route("**/api/chat/messages", () => {});
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("button", { name: "Converting the voice input" })).toBeVisible();
  await input.fill("Add a plan");
  const sent = page.waitForRequest("**/api/chat/messages");
  await send(page).click();
  await sent;
  await expect(input).toHaveValue("");
  release();
  await expect(input).toHaveValue("red roses");
  await expect(start(page)).toBeVisible();
});

test("Send after a failed conversion keeps the error and Send again", async ({ page }) => {
  let files = 0;
  await page.route("**/api/voice/transcriptions", (route) =>
    ++files === 1
      ? route.fulfill({ status: 503, json: { error: "The speech model is still starting" } })
      : route.fulfill({ json: { data: { text: "red roses" } } }),
  );
  await page.route("**/api/chat/messages", () => {});
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await stop(page).click();
  await expect(again(page)).toBeVisible();
  await input.fill("Add a plan");
  const sent = page.waitForRequest("**/api/chat/messages");
  await send(page).click();
  await sent;
  await expect(input).toHaveValue("");
  await expect(page.getByRole("alert")).toHaveText("The speech model is still starting");
  await expect(again(page)).toBeVisible();
  await again(page).click();
  await expect(input).toHaveValue("red roses");
  await expect(again(page)).toHaveCount(0);
});

test("a switch during a recording keeps it and the text goes to the new field", async ({
  page,
}) => {
  await answer(page, "red roses");
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await open(page, seasonal);
  await expect(stop(page)).toBeVisible();
  expect(await trackState(page)).toEqual(["live"]);
  await stop(page).click();
  await expect(input).toHaveValue("red roses");
  await expect(start(page)).toBeVisible();
  expect(await page.evaluate("microphones.length")).toBe(1);
});

test("a switch during the conversion keeps it and the text goes to the new field", async ({
  page,
}) => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/voice/transcriptions", async (route) => {
    await held;
    await route.fulfill({ json: { data: { text: "red roses" } } });
  });
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("button", { name: "Converting the voice input" })).toBeVisible();
  await open(page, seasonal);
  await expect(page.getByRole("button", { name: "Converting the voice input" })).toBeVisible();
  release();
  await expect(input).toHaveValue("red roses");
});

test.describe("without a microphone API", () => {
  test("the button is not there without getUserMedia", async ({ page }) => {
    await page.addInitScript("navigator.mediaDevices.getUserMedia = undefined");
    await page.goto(shop);
    const main = page.getByRole("main");
    await expect(main.getByRole("button", { name: "Send" })).toBeVisible();
    await expect(main.getByRole("button", { name: /voice input/ })).toHaveCount(0);
  });

  test("the button is not there without AudioWorklet", async ({ page }) => {
    await page.addInitScript("delete window.AudioWorkletNode");
    await page.goto(shop);
    const main = page.getByRole("main");
    await expect(main.getByRole("button", { name: "Send" })).toBeVisible();
    await expect(main.getByRole("button", { name: /voice input/ })).toHaveCount(0);
  });
});

const heldLocks = (page: Page) =>
  page.evaluate("locks.filter((lock) => !lock.released).map((lock) => lock.type)");

const wakeLockTest = (name: string, run: (page: Page) => Promise<void>) =>
  test(name, async ({ page }) => {
    await page.addInitScript(fakeWakeLock);
    await answer(page, "red");
    await page.goto(shop);
    await run(page);
  });

wakeLockTest("a recording requests the screen wake lock", async (page) => {
  await start(page).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
});

wakeLockTest("the stop releases the screen wake lock", async (page) => {
  await start(page).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await stop(page).click();
  await expect(start(page)).toBeVisible();
  await expect.poll(() => heldLocks(page)).toEqual([]);
});

wakeLockTest("Send releases the screen wake lock", async (page) => {
  await page.route("**/api/chat/messages", () => {});
  await start(page).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await page.getByLabel("Message to the Lead").fill("Add a plan");
  await page.getByRole("main").getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(() => heldLocks(page)).toEqual([]);
});

wakeLockTest("a start error releases the screen wake lock", async (page) => {
  await page.evaluate("window.microphoneError = 'NotAllowedError'");
  await start(page).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect.poll(() => heldLocks(page)).toEqual([]);
});

wakeLockTest("a release of the lock during a recording requests it again", async (page) => {
  await start(page).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await page.evaluate("locks[0].release()");
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  expect(await page.evaluate("locks.length")).toBe(2);
  await expect(stop(page)).toBeVisible();
});

wakeLockTest("a release of the lock on a hidden page requests no lock", async (page) => {
  await begin(page);
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await page.evaluate(
    "Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); locks[0].release()",
  );
  await expect(stop(page)).toBeVisible();
  expect(await page.evaluate("locks.length")).toBe(1);
});

wakeLockTest("a refused wake lock request shows no error", async (page) => {
  await page.evaluate("window.wakeLockError = 'NotAllowedError'");
  await begin(page);
  await stop(page).click();
  await expect(page.getByLabel("Message to the Lead")).toHaveValue("red");
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("the recording works without navigator.wakeLock", async ({ page }) => {
  await page.addInitScript("delete Navigator.prototype.wakeLock");
  await answer(page, "red roses");
  await page.goto(shop);
  expect(await page.evaluate("navigator.wakeLock")).toBeUndefined();
  await begin(page);
  await stop(page).click();
  await expect(page.getByLabel("Message to the Lead")).toHaveValue("red roses");
});
