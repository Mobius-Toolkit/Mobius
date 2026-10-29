import { expect, type Page } from "@playwright/test";

export type Kind = "start" | "merge" | "repairs";

export const lines = {
  start: "Autopilot paused: no task starts",
  merge: "Autopilot paused: no automatic merge",
  repairs: "Repairs paused: no repair task starts",
};

// The API request of the page carries the device login, but the request context of Playwright does not.
async function put(page: Page, kind: string, on: boolean) {
  const status = await page.evaluate(
    ([path, body]) =>
      fetch(path, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body,
      }).then((response) => response.status),
    [`/api/switches/${kind}`, JSON.stringify({ on })],
  );
  expect(status).toBe(204);
}

export async function getSwitches(page: Page) {
  return (await page.evaluate(`fetch("/api/switches").then((response) => response.json())`)) as {
    data: Record<Kind, boolean>;
  };
}

// The server deletes the held work of a switch when it turns the switch on, so the held work comes after the switch is off.
export async function turnOff(page: Page, kind: Kind, held: number[] = []) {
  await put(page, kind, false);
  for (const issue of held) {
    expect((await page.request.post(`/e2e/held-work/${kind}/${issue}`)).ok()).toBe(true);
  }
}

export async function turnAllOn(page: Page) {
  for (const kind of Object.keys(lines)) {
    await put(page, kind, true);
  }
}
