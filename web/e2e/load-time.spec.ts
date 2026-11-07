import { expect, test } from "@playwright/test";

test("load time of chat screen on Slow 4G and 4x CPU slowdown", async ({ page }) => {
  test.setTimeout(180_000);
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();

  const client = await page.context().newCDPSession(page);
  await client.send("Network.emulateNetworkConditions", {
    offline: false,
    latency: 150,
    downloadThroughput: (1_600 * 1000) / 8,
    uploadThroughput: (750 * 1000) / 8,
  });
  await client.send("Emulation.setCPUThrottlingRate", { rate: 4 });

  const times: number[] = [];
  for (let i = 0; i < 5; i++) {
    const start = performance.now();
    await page.goto("/chat");
    const input = page.getByLabel("Message to the Coordinator");
    await expect(input).toBeEditable({ timeout: 60_000 });
    times.push(Math.round(performance.now() - start));
  }

  times.sort((a, b) => a - b);
  const median = times[2];
  console.log(`Median load time until chat input is editable: ${median} ms`);
  console.log(`All 5 runs: ${times.join(", ")} ms`);
});
