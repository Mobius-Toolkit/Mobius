import { expect, test, type Page, type Route } from "@playwright/test";
import { countSamples, fakeMicrophone, fakeWakeLock } from "./microphone.js";
import { coordinatorQuestion, routeInboxItems } from "./stops.js";

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
  await page.route("**/api/voice/conversions/*", conversionReply);
});

type Result = { text?: string; error?: string };

// The id of a conversion holds its result, so the route of the conversions needs no state.
const upload = (result: Result) => ({
  json: {
    data: {
      id: Buffer.from(JSON.stringify(result)).toString("base64url"),
      state: "converting",
      text: "",
      error: "",
    },
  },
});

// Answers the request for a conversion that upload made with the result of the conversion.
const conversionReply = (route: Route) => {
  const id = new URL(route.request().url()).pathname.split("/").at(-1)!;
  const { text = "", error = "" }: Result = JSON.parse(Buffer.from(id, "base64url").toString());
  return route.fulfill({
    json: { data: { id, state: error ? "failed" : "done", text, error } },
  });
};

// Holds the answer of each request for a conversion until release runs.
const holdConversions = async (page: Page) => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/voice/conversions/*", async (route) => {
    await held;
    await conversionReply(route);
  });
  return release;
};

const answer = (page: Page, text: string) =>
  page.route("**/api/voice/transcriptions", (route) => route.fulfill(upload({ text })));

const start = (page: Page) =>
  page.getByRole("main").getByRole("button", { name: "Start voice input", exact: true });
const stop = (page: Page) =>
  page.getByRole("main").getByRole("button", { name: "Stop voice input", exact: true });
const send = (page: Page) =>
  page.getByRole("main").getByRole("button", { name: "Send", exact: true });
const again = (page: Page) => page.getByRole("button", { name: "Send again" });
const discard = (page: Page) => page.getByRole("button", { name: "Discard" });
const voiceCard = (page: Page) =>
  page.getByRole("main").getByRole("group", { name: "Voice recording" });
const removeVoice = (page: Page) => page.getByRole("button", { name: "Remove voice" });

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

// The tests have no DOM types, so the script reads the drafts in the browser.
const storedDrafts = `new Promise((resolve) => {
  const open = indexedDB.open("mobius");
  open.onsuccess = () => {
    const all = open.result.transaction("drafts").objectStore("drafts").getAll();
    all.onsuccess = () =>
      resolve(
        all.result.map((draft) => ({
          text: draft.text,
          voice: draft.voice?.state ?? null,
          audio: Boolean(draft.voice?.audio),
          conversion: draft.voice?.conversion ?? "",
        })),
      );
  };
})`;

const drafts = (page: Page) => page.evaluate(storedDrafts);

test("a recording adds the text of the answer to the message", async ({ page }) => {
  await answer(page, " red roses ");
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await input.fill("Plant");
  await begin(page);
  await stop(page).click();
  await expect(input).toHaveValue("Plant red roses");
  await expect(removeVoice(page)).toBeVisible();
  await expect(start(page)).toBeEnabled();
  expect(await trackState(page)).toEqual(["ended"]);
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("the request has a WAV file with 16 kHz mono 16-bit samples", async ({ page }) => {
  let body: Buffer | null = null;
  let type: string | undefined;
  await page.route("**/api/voice/transcriptions", (route) => {
    body = route.request().postDataBuffer();
    type = route.request().headers()["content-type"];
    return route.fulfill(upload({ text: "red roses" }));
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

test("the converting card shows until the answer comes, and it blocks the voice button and Send", async ({
  page,
}) => {
  let requests = 0;
  const release = await holdConversions(page);
  await page.route("**/api/voice/transcriptions", (route) => {
    requests++;
    return route.fulfill(upload({ text: "red roses" }));
  });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await stop(page).click();
  await expect(voiceCard(page).getByText("Converting", { exact: true })).toBeVisible();
  await input.fill("Plant");
  await expect(start(page)).toBeDisabled();
  await expect(page.getByRole("main").getByRole("button", { name: "Send" })).toBeDisabled();
  await expect(input).toHaveValue("Plant");
  release();
  await expect(input).toHaveValue("Plant red roses");
  await expect(voiceCard(page).getByText("Converting")).toHaveCount(0);
  await expect(start(page)).toBeEnabled();
  await expect(page.getByRole("main").getByRole("button", { name: "Send" })).toBeEnabled();
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
// is the status of an error answer. finish is the result of the conversion at the end of the recording.
const liveServer = async (page: Page, answers: (Live | number)[], finish: Result = {}) => {
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
    return route.fulfill(upload(finish));
  });
  return seen;
};

// The clock stands still, so a tick of the live text comes only after tick.
const tick = (page: Page) => page.clock.runFor(2000);

// The page asks for a conversion each 2 seconds, so the tests with a clock run it until the card does not record or
// convert any more.
const settled = (page: Page, timeout?: number) =>
  expect
    .poll(
      async () => {
        await tick(page);
        return voiceCard(page)
          .getByText(/^(Recording|Converting)/)
          .count();
      },
      timeout ? { timeout, intervals: [10] } : {},
    )
    .toBe(0);

const live = (page: Page, text: string) => voiceCard(page).getByText(text, { exact: true });

test("the live text shows in the card during the recording, and the field keeps the text of the Owner", async ({
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
  await expect(voiceCard(page)).toContainText("Recording 0:00");
  await tick(page);
  await expect(live(page, "red")).toBeVisible();
  await expect(input).toHaveValue("Please");
  await tick(page);
  await expect(live(page, "red roses")).toBeVisible();
  await expect(input).toHaveValue("Please");
  await expect(voiceCard(page)).toContainText("Recording 0:04");
  await stop(page).click();
  await settled(page);
  await expect(input).toHaveValue("Please Red roses, please.");
  await expect(live(page, "red roses")).toHaveCount(0);
  await expect(removeVoice(page)).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(seen.sequences.map(Number)).toEqual(seen.sequences.map((_, i) => i));
  expect(seen.sequences.length).toBeGreaterThanOrEqual(2);
  expect(seen.sizes.every((size) => size > 0 && size % 2 === 0)).toBe(true);
  expect(seen.finishes).toBe(1);
});

test("the final text goes in at the cursor, and the text around it stays", async ({ page }) => {
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
  await tick(page);
  await expect(live(page, "red roses")).toBeVisible();
  await expect(input).toHaveValue("Plant now");
  await stop(page).click();
  await settled(page);
  await expect(input).toHaveValue("Plant Red roses now");
});

test("a failed chunk keeps the recording and the stop sends the file for the text", async ({
  page,
}) => {
  await page.clock.install();
  const seen = await liveServer(page, [{ committed: "", draft: "red" }, 500]);
  let files = 0;
  await page.route("**/api/voice/transcriptions", (route) => {
    files++;
    return route.fulfill(upload({ text: "red roses" }));
  });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await tick(page);
  await expect(live(page, "red")).toBeVisible();
  await tick(page);
  await expect.poll(() => seen.sequences.length).toBe(2);
  await tick(page);
  await expect(stop(page)).toBeVisible();
  expect(seen.sequences.length).toBe(2);
  await stop(page).click();
  await settled(page);
  await expect(input).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(files).toBe(1);
  expect(seen.finishes).toBe(0);
});

test("a failed conversion keeps the audio and Send again sends it", async ({ page }) => {
  await page.clock.install();
  const seen = await liveServer(page, [{ committed: "", draft: "red" }], {
    error: "The speech model is still starting",
  });
  const files: Buffer[] = [];
  await page.route("**/api/voice/transcriptions", (route) => {
    files.push(route.request().postDataBuffer() ?? Buffer.alloc(0));
    return files.length === 1
      ? route.fulfill(upload({ error: "The converter returned an error" }))
      : route.fulfill(upload({ text: "red roses" }));
  });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await tick(page);
  await expect(live(page, "red")).toBeVisible();
  await stop(page).click();
  await settled(page);
  await expect(voiceCard(page).getByRole("alert")).toHaveText("The speech model is still starting");
  await expect(again(page)).toBeVisible();
  await expect(input).toHaveValue("");
  expect(files.length).toBe(0);
  expect(seen.finishes).toBe(1);

  await again(page).click();
  await settled(page);
  await expect(voiceCard(page).getByRole("alert")).toHaveText("The converter returned an error");
  await expect(again(page)).toBeVisible();
  await expect(input).toHaveValue("");

  await again(page).click();
  await settled(page);
  await expect(input).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(again(page)).toHaveCount(0);
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
  await settled(page);
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  expect(chunks).toBe(0);
});

for (const message of [
  "The speech model is still downloading",
  "The converter returned an error",
]) {
  test(`the error "${message}" of the end of the recording shows its message`, async ({ page }) => {
    await page.clock.install();
    let files = 0;
    await liveServer(page, [{ committed: "", draft: "red" }], { error: message });
    await page.route("**/api/voice/transcriptions", (route) => {
      files++;
      return route.fulfill(upload({ text: "red roses" }));
    });
    await page.goto("/chat");
    const input = page.getByLabel("Message to the Coordinator");
    await begin(page);
    await tick(page);
    await expect(live(page, "red")).toBeVisible();
    await stop(page).click();
    await settled(page);
    await expect(page.getByRole("alert")).toHaveText(message);
    await expect(input).toHaveValue("");
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
      return route.fulfill(upload({ text: "red roses" }));
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
  await settled(page);
  await expect(input).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
});

for (const message of [
  "The speech model is still downloading",
  "The converter returned an error",
]) {
  test(`the error "${message}" of a conversion shows its message`, async ({ page }) => {
    await page.route("**/api/voice/transcriptions", (route) =>
      route.fulfill(upload({ error: message })),
    );
    await page.goto("/chat");
    await begin(page);
    await stop(page).click();
    await expect(page.getByRole("alert")).toHaveText(message);
    await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("");
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

const pending = (id: string) => ({
  json: { data: { id, state: "converting", text: "", error: "" } },
});

test("a conversion that ends after more than one request puts its text into the field", async ({
  page,
}) => {
  await page.clock.install();
  let requests = 0;
  await answer(page, "red roses");
  await page.route("**/api/voice/conversions/*", (route) =>
    ++requests < 3 ? route.fulfill(pending("c1")) : conversionReply(route),
  );
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await settled(page);
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(requests).toBe(3);
});

test("a failed request for a conversion does not end it", async ({ page }) => {
  await page.clock.install();
  let requests = 0;
  await answer(page, "red roses");
  await page.route("**/api/voice/conversions/*", (route) => {
    requests++;
    if (requests === 1) {
      return route.abort();
    }
    if (requests === 2) {
      return route.fulfill({ status: 524, contentType: "text/html", body: "<html>524</html>" });
    }
    return conversionReply(route);
  });
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await settled(page);
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(requests).toBe(3);
});

test("a 404 for a conversion uploads the kept audio again", async ({ page }) => {
  await page.clock.install();
  const files: Buffer[] = [];
  await page.route("**/api/voice/transcriptions", (route) => {
    files.push(route.request().postDataBuffer() ?? Buffer.alloc(0));
    return route.fulfill(files.length === 1 ? pending("lost") : upload({ text: "red roses" }));
  });
  await page.route("**/api/voice/conversions/lost", (route) =>
    route.fulfill({ status: 404, json: { error: "unknown recording" } }),
  );
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await settled(page);
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(files.length).toBe(2);
  expect(files[0].subarray(0, 4).toString()).toBe("RIFF");
  expect(files[1].equals(files[0])).toBe(true);
});

test("a 401 for a conversion ends the wait at once", async ({ page }) => {
  await page.clock.install();
  let requests = 0;
  await answer(page, "red roses");
  await page.route("**/api/voice/conversions/*", (route) => {
    requests++;
    return route.fulfill({ status: 401, json: { error: "login required" } });
  });
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await settled(page);
  await expect(page.getByRole("alert")).toHaveText("login required");
  await expect(again(page)).toBeVisible();
  expect(requests).toBe(1);
});

test("a 500 for each request for a conversion ends the wait after 16 minutes", async ({ page }) => {
  test.setTimeout(120_000);
  await page.clock.install();
  let requests = 0;
  await answer(page, "red roses");
  await page.route("**/api/voice/conversions/*", (route) => {
    requests++;
    return route.fulfill({ status: 500, contentType: "text/html", body: "<html>500</html>" });
  });
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await settled(page, 120_000);
  await expect(page.getByRole("alert")).toHaveText("The server answered 500.");
  await expect(again(page)).toBeVisible();
  expect(requests).toBeGreaterThanOrEqual(470);
});

test("a conversion that does not end is a failure after 16 minutes", async ({ page }) => {
  test.setTimeout(120_000);
  await page.clock.install();
  let requests = 0;
  await answer(page, "red roses");
  await page.route("**/api/voice/conversions/*", (route) => {
    requests++;
    return route.fulfill(pending("c1"));
  });
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await settled(page, 120_000);
  await expect(page.getByRole("alert")).toHaveText("The server did not convert the recording.");
  await expect(again(page)).toBeVisible();
  expect(requests).toBeGreaterThanOrEqual(470);
});

test("a non-JSON answer with the status 524 shows the status", async ({ page }) => {
  await page.route("**/api/voice/transcriptions", (route) =>
    route.fulfill({ status: 524, contentType: "text/html", body: "<html>error code: 524</html>" }),
  );
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("alert")).toHaveText("The server answered 524.");
  await expect(again(page)).toBeVisible();
});

// Answers the first request of the route never, and each later request with the result.
const hangFirst = async (page: Page, pattern: string, result: Result) => {
  const seen = { requests: 0 };
  await page.route(pattern, (route) => {
    seen.requests++;
    return seen.requests === 1 ? undefined : route.fulfill(upload(result));
  });
  return seen;
};

test("a hung upload ends after 6 minutes, and Send again gives the text", async ({ page }) => {
  await page.clock.install();
  await page.route("**/api/voice/recordings", (route) =>
    route.fulfill({ status: 500, json: { error: "failed" } }),
  );
  const seen = await hangFirst(page, "**/api/voice/transcriptions", { text: "red roses" });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await stop(page).click();
  await expect.poll(() => seen.requests).toBe(1);
  await page.clock.runFor(6 * 60 * 1000 - 1000);
  await expect(voiceCard(page).getByText("Converting", { exact: true })).toBeVisible();
  await page.clock.runFor(1000);
  await expect(page.getByRole("alert")).toHaveText(unreachable);
  await expect(again(page)).toBeVisible();
  await expect(input).toHaveValue("");
  await again(page).click();
  await settled(page);
  await expect(input).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("a hung end of the recording ends after 30 seconds, and the stop sends the file", async ({
  page,
}) => {
  await page.clock.install();
  let files = 0;
  await liveServer(page, [{ committed: "", draft: "red" }]);
  const seen = await hangFirst(page, "**/api/voice/recordings/r1/finish", {});
  await page.route("**/api/voice/transcriptions", (route) => {
    files++;
    return route.fulfill(upload({ text: "red roses" }));
  });
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await expect.poll(() => seen.requests).toBe(1);
  await page.clock.runFor(30 * 1000);
  await settled(page);
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(files).toBe(1);
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
  await settled(page);
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

test("Send again works after the network comes back", async ({ page }) => {
  const files: Buffer[] = [];
  await page.route("**/api/voice/**", (route) => route.abort());
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("alert")).toHaveText(unreachable);
  await page.unroute("**/api/voice/**");
  await page.route("**/api/voice/transcriptions", (route) => {
    files.push(route.request().postDataBuffer() ?? Buffer.alloc(0));
    return route.fulfill(upload({ text: "red roses" }));
  });
  await again(page).click();
  await expect(input).toHaveValue("red roses");
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(again(page)).toHaveCount(0);
  expect(files.length).toBe(1);
  expect(files[0].subarray(0, 4).toString()).toBe("RIFF");
});

test("a new recording shows no text of an earlier recording", async ({ page }) => {
  await page.clock.install();
  const seen = await liveServer(page, [{ committed: "", draft: "red" }], {
    error: "The speech model is still starting",
  });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await tick(page);
  await expect.poll(() => seen.sequences.length).toBe(1);
  await tick(page);
  await expect.poll(() => seen.sequences.length).toBe(2);
  await stop(page).click();
  await expect
    .poll(async () => {
      await tick(page);
      return again(page).isVisible();
    })
    .toBe(true);
  await discard(page).click();
  await start(page).click();
  await expect(input).toHaveValue("");
  await expect(voiceCard(page)).toContainText("Recording");
  await expect(voiceCard(page)).not.toContainText("red");
});

test("an empty result shows an error and keeps the audio", async ({ page }) => {
  const texts = ["  ", "red roses"];
  await page.route("**/api/voice/transcriptions", (route) =>
    route.fulfill(upload({ text: texts.shift() })),
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

// Gives the body of each message request.
const messages = async (page: Page) => {
  const bodies: string[] = [];
  await page.route("**/api/chat/messages", (route) => {
    bodies.push(route.request().postData() ?? "");
    return route.fulfill({ status: 204 });
  });
  return bodies;
};

test("Send during a recording stops it and sends nothing, and the second Send sends the edited text", async ({
  page,
}) => {
  let files = 0;
  await page.route("**/api/voice/transcriptions", (route) => {
    files++;
    return route.fulfill(upload({ text: "red roses" }));
  });
  // The request stays open, so the message stays in the queue and shows as a bubble.
  const bodies: string[] = [];
  await page.route("**/api/chat/messages", (route) => {
    bodies.push(route.request().postData() ?? "");
  });
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await input.fill("Add a plan");
  await send(page).click();
  await expect(start(page)).toBeVisible();
  expect(await trackState(page)).toEqual(["ended"]);
  await expect(removeVoice(page)).toBeVisible();
  await expect(input).toHaveValue("Add a plan red roses");
  expect(bodies).toEqual([]);
  expect(files).toBe(1);

  await input.fill("Add a red plan");
  await send(page).click();
  await expect.poll(() => bodies.length).toBe(1);
  expect(bodies[0]).toContain("Add a red plan");
  expect(bodies[0]).not.toContain("roses");
  expect(bodies[0]).toMatch(/name="voice"\r\n\r\ntrue/);
  await expect(input).toHaveValue("");
  await expect(voiceCard(page)).toHaveCount(0);
  const bubble = page.getByRole("main").locator("[data-queued]");
  await expect(bubble.getByText("Add a red plan")).toBeVisible();
  await expect(bubble.getByRole("img", { name: "Voice" })).toBeVisible();
  await expect.poll(() => drafts(page)).toEqual([]);
  expect(bodies.length).toBe(1);
});

test("a message with no voice card has the voice mark false", async ({ page }) => {
  const bodies = await messages(page);
  await page.goto(shop);
  await page.getByLabel("Message to the Lead").fill("Add a plan");
  await send(page).click();
  await expect.poll(() => bodies.length).toBe(1);
  expect(bodies[0]).toMatch(/name="voice"\r\n\r\nfalse/);
});

test("the Owner removes a ready card and the text stays", async ({ page }) => {
  await answer(page, "red roses");
  const bodies = await messages(page);
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await stop(page).click();
  await expect(input).toHaveValue("red roses");
  await removeVoice(page).click();
  await expect(voiceCard(page)).toHaveCount(0);
  await expect(input).toHaveValue("red roses");
  await send(page).click();
  await expect.poll(() => bodies.length).toBe(1);
  expect(bodies[0]).toMatch(/name="voice"\r\n\r\nfalse/);
});

test("a new recording adds its text at the cursor, and the card stays one card", async ({
  page,
}) => {
  const texts = ["red", "roses"];
  await page.route("**/api/voice/transcriptions", (route) =>
    route.fulfill(upload({ text: texts.shift() })),
  );
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await stop(page).click();
  await expect(input).toHaveValue("red");
  await expect(removeVoice(page)).toBeVisible();
  await begin(page);
  await expect(voiceCard(page)).toHaveCount(1);
  await expect(voiceCard(page)).toContainText("Recording");
  await stop(page).click();
  await expect(input).toHaveValue("red roses");
  await expect(voiceCard(page)).toHaveCount(1);
  await expect(removeVoice(page)).toBeVisible();
});

test("Send and the voice button stay off while the card has failed, and Discard keeps the text", async ({
  page,
}) => {
  await page.route("**/api/voice/transcriptions", (route) =>
    route.fulfill(upload({ error: "The speech model is still starting" })),
  );
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await input.fill("Add a plan");
  await begin(page);
  await stop(page).click();
  await expect(again(page)).toBeVisible();
  await expect(start(page)).toBeDisabled();
  await expect(send(page)).toBeDisabled();
  await input.press("Enter");
  await expect(again(page)).toBeVisible();
  await discard(page).click();
  await expect(voiceCard(page)).toHaveCount(0);
  await expect(input).toHaveValue("Add a plan");
  await expect(start(page)).toBeEnabled();
  await expect(send(page)).toBeEnabled();
  await expect
    .poll(() => drafts(page))
    .toEqual([{ text: "Add a plan", voice: null, audio: false, conversion: "" }]);
});

test("a refused Send brings the voice card back", async ({ page }) => {
  await answer(page, "red roses");
  await page.addInitScript(`
    const add = IDBObjectStore.prototype.add;
    IDBObjectStore.prototype.add = function (value) {
      if (window.refuseSend && this.name === "outbox") {
        throw new DOMException("The store refuses the message.", "DataError");
      }
      return add.call(this, value);
    };
  `);
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await stop(page).click();
  await expect(input).toHaveValue("red roses");
  await expect(removeVoice(page)).toBeVisible();
  await page.evaluate("window.refuseSend = true");
  await send(page).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(input).toHaveValue("red roses");
  await expect(removeVoice(page)).toBeVisible();
});

test("the error of a recording shows only in its own chat", async ({ page }) => {
  await page.route("**/api/voice/transcriptions", (route) =>
    route.fulfill(upload({ error: "The converter returned an error" })),
  );
  await page.goto(shop);
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("alert")).toHaveText("The converter returned an error");
  await open(page, seasonal);
  await expect(page.getByLabel("Message to the Lead")).toBeEditable();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(again(page)).toHaveCount(0);
  await expect(voiceCard(page)).toHaveCount(0);
  await expect(start(page)).toBeEnabled();
  await open(page, shop);
  await expect(page.getByRole("alert")).toHaveText("The converter returned an error");
  await expect(again(page)).toBeVisible();
});

test("a switch to another chat during a recording stops it, and the text goes only to the chat of the start", async ({
  page,
}) => {
  await answer(page, "red roses");
  await page.goto(shop);
  await begin(page);
  await open(page, seasonal);
  await expect(start(page)).toBeVisible();
  expect(await trackState(page)).toEqual(["ended"]);
  const input = page.getByLabel("Message to the Lead");
  await expect(input).toBeEditable();
  await expect(input).toHaveValue("");
  await expect(voiceCard(page)).toHaveCount(0);
  await expect
    .poll(() => drafts(page))
    .toEqual([{ text: "red roses", voice: "ready", audio: false, conversion: expect.any(String) }]);
  await expect(input).toHaveValue("");
  await open(page, shop);
  await expect(input).toHaveValue("red roses");
  await expect(removeVoice(page)).toBeVisible();
  expect(await page.evaluate("microphones.length")).toBe(1);
});

test("a conversion that ends while the page shows another chat puts its text into the draft of the chat of the recording", async ({
  page,
}) => {
  const release = await holdConversions(page);
  await answer(page, "red roses");
  await page.goto(shop);
  const input = page.getByLabel("Message to the Lead");
  await begin(page);
  await stop(page).click();
  await expect(voiceCard(page).getByText("Converting", { exact: true })).toBeVisible();
  await open(page, seasonal);
  await expect(input).toBeEditable();
  await expect(voiceCard(page)).toHaveCount(0);
  await expect(start(page)).toBeEnabled();
  release();
  await expect
    .poll(() => drafts(page))
    .toEqual([{ text: "red roses", voice: "ready", audio: false, conversion: "" }]);
  await expect(input).toHaveValue("");
  await expect(voiceCard(page)).toHaveCount(0);
  await open(page, shop);
  await expect(input).toHaveValue("red roses");
  await expect(removeVoice(page)).toBeVisible();
});

test("a send in an Answer panel input removes the card and sends no voice mark", async ({
  page,
}) => {
  await routeInboxItems(page, [coordinatorQuestion]);
  await answer(page, "red roses");
  const bodies: string[] = [];
  await page.route("**/api/inbox/211/answer", (route) => {
    bodies.push(route.request().postData() ?? "");
    return route.fulfill({ json: { data: { url: "" } } });
  });
  await page.goto("/inbox");
  await page
    .getByRole("main")
    .getByRole("listitem")
    .filter({ hasText: "Pick the shop currency" })
    .getByRole("button", { name: "Answer" })
    .click();
  const panel = page.getByRole("dialog");
  const note = panel.getByRole("textbox", { name: "Your answer" });
  await expect(note).toBeEditable();
  await panel.getByRole("button", { name: "Start voice input" }).click();
  await expect.poll(() => page.evaluate("samples")).toBeGreaterThan(0);
  await expect(panel.getByRole("group", { name: "Voice recording" })).toContainText("Recording");
  await panel.getByRole("button", { name: "Stop voice input" }).click();
  await expect(note).toHaveValue("red roses");
  await expect(panel.getByRole("button", { name: "Remove voice" })).toBeVisible();
  await panel.getByRole("button", { name: "Send", exact: true }).click();
  await expect(panel).toBeHidden();
  expect(bodies.map((body) => JSON.parse(body))).toEqual([{ text: "red roses" }]);
  await expect.poll(() => drafts(page)).toEqual([]);
});

// The script makes the database at version 2 with one record in each older store. The app opens it after a reload.
const versionTwo = `new Promise((resolve, reject) => {
  const remove = indexedDB.deleteDatabase("mobius");
  remove.onerror = () => reject(remove.error);
  remove.onsuccess = () => {
    const open = indexedDB.open("mobius", 2);
    open.onupgradeneeded = () => {
      const db = open.result;
      db.createObjectStore("outbox", { keyPath: "id" }).createIndex("chat", "chat");
      db.createObjectStore("drafts", { keyPath: "key" });
    };
    open.onsuccess = () => {
      const db = open.result;
      const transaction = db.transaction(["outbox", "drafts"], "readwrite");
      transaction.objectStore("outbox").add({ id: "old", chat: ["owner", "shop", 99], text: "old message" });
      transaction.objectStore("drafts").add({ key: "old", text: "old draft", images: [], inboxItems: [] });
      transaction.oncomplete = () => {
        db.close();
        resolve(undefined);
      };
    };
  };
})`;

const upgraded = `new Promise((resolve) => {
  const open = indexedDB.open("mobius");
  open.onsuccess = () => {
    const db = open.result;
    const transaction = db.transaction(["outbox", "drafts"]);
    const outbox = transaction.objectStore("outbox").getAll();
    const drafts = transaction.objectStore("drafts").getAll();
    transaction.oncomplete = () =>
      resolve({
        version: db.version,
        stores: [...db.objectStoreNames],
        outbox: outbox.result.map((item) => item.text),
        drafts: drafts.result.map((item) => item.text),
      });
  };
})`;

test("a reload during a conversion keeps the card with its audio, and the card converts after the reload", async ({
  page,
}) => {
  await page.route("**/api/voice/recordings", (route) =>
    route.fulfill({ status: 500, json: { error: "failed" } }),
  );
  const files: Buffer[] = [];
  await page.route("**/api/voice/transcriptions", (route) => {
    files.push(route.request().postDataBuffer() ?? Buffer.alloc(0));
    return files.length === 1 ? undefined : route.fulfill(upload({ text: "red roses" }));
  });
  await page.goto("/chat");
  const input = page.getByLabel("Message to the Coordinator");
  await begin(page);
  await stop(page).click();
  await expect.poll(() => files.length).toBe(1);
  await expect
    .poll(() => drafts(page))
    .toEqual([{ text: "", voice: "converting", audio: true, conversion: "" }]);
  await page.reload();
  await expect(input).toHaveValue("red roses");
  await expect(removeVoice(page)).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(files.length).toBe(2);
  expect(files[0].subarray(0, 4).toString()).toBe("RIFF");
  expect(files[0].length).toBeGreaterThan(44);
  expect(files[1].equals(files[0])).toBe(true);
  await expect
    .poll(() => drafts(page))
    .toEqual([{ text: "red roses", voice: "ready", audio: false, conversion: "" }]);
});

test("a reload keeps the conversion, and the card asks the server for its result", async ({
  page,
}) => {
  let uploads = 0;
  const release = await holdConversions(page);
  await page.route("**/api/voice/transcriptions", (route) => {
    uploads++;
    return route.fulfill(upload({ text: "red roses" }));
  });
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await expect
    .poll(async () => (await drafts(page)) as { conversion: string }[])
    .toEqual([expect.objectContaining({ voice: "converting", conversion: expect.any(String) })]);
  await expect
    .poll(async () => ((await drafts(page)) as { conversion: string }[])[0].conversion)
    .not.toBe("");
  await page.reload();
  release();
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  expect(uploads).toBe(1);
});

test("a card that converts after a reload puts its text into its own chat while the page shows another chat", async ({
  page,
}) => {
  await page.route("**/api/voice/recordings", (route) =>
    route.fulfill({ status: 500, json: { error: "failed" } }),
  );
  let files = 0;
  await page.route("**/api/voice/transcriptions", (route) =>
    ++files === 1 ? undefined : route.fulfill(upload({ text: "red roses" })),
  );
  await page.goto(shop);
  await begin(page);
  await stop(page).click();
  await expect.poll(() => files).toBe(1);
  await page.goto(seasonal);
  await expect(page.getByLabel("Message to the Lead")).toBeEditable();
  await expect
    .poll(() => drafts(page))
    .toEqual([{ text: "red roses", voice: "ready", audio: false, conversion: "" }]);
  await expect(page.getByLabel("Message to the Lead")).toHaveValue("");
  await page.goto(shop);
  await expect(page.getByLabel("Message to the Lead")).toHaveValue("red roses");
});

test("a failed conversion and a reload keep the card with the error and the audio", async ({
  page,
}) => {
  const files: Buffer[] = [];
  await page.route("**/api/voice/transcriptions", (route) => {
    files.push(route.request().postDataBuffer() ?? Buffer.alloc(0));
    return files.length === 1
      ? route.fulfill(upload({ error: "The converter returned an error" }))
      : route.fulfill(upload({ text: "red roses" }));
  });
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await expect(page.getByRole("alert")).toHaveText("The converter returned an error");
  await page.reload();
  await expect(page.getByRole("alert")).toHaveText("The converter returned an error");
  await expect(again(page)).toBeVisible();
  await expect(discard(page)).toBeVisible();
  expect(await drafts(page)).toEqual([{ text: "", voice: "failed", audio: true, conversion: "" }]);
  await again(page).click();
  await expect(page.getByLabel("Message to the Coordinator")).toHaveValue("red roses");
  expect(files.length).toBe(2);
  expect(files[1].equals(files[0])).toBe(true);
});

test("Discard removes the card and the audio", async ({ page }) => {
  await page.route("**/api/voice/transcriptions", (route) =>
    route.fulfill(upload({ error: "The converter returned an error" })),
  );
  await page.goto("/chat");
  await begin(page);
  await stop(page).click();
  await expect(again(page)).toBeVisible();
  await discard(page).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(again(page)).toHaveCount(0);
  await expect(discard(page)).toHaveCount(0);
  await expect.poll(() => drafts(page)).toEqual([]);
  await page.reload();
  await expect(start(page)).toBeVisible();
  await expect(voiceCard(page)).toHaveCount(0);
});

test("a database at version 2 opens at version 3 and keeps its records", async ({ page }) => {
  await page.evaluate(versionTwo);
  await page.reload();
  await expect(page.getByLabel("Access password")).toBeHidden();
  await page.goto("/chat");
  await expect(page.getByLabel("Message to the Coordinator")).toBeEditable();
  expect(await page.evaluate(upgraded)).toEqual({
    version: 3,
    stores: ["drafts", "outbox", "voice"],
    outbox: ["old message"],
    drafts: ["old draft"],
  });
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

wakeLockTest("Send stops the recording and releases the screen wake lock", async (page) => {
  await start(page).click();
  await expect.poll(() => heldLocks(page)).toEqual(["screen"]);
  await page.getByLabel("Message to the Lead").fill("Add a plan");
  await send(page).click();
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
