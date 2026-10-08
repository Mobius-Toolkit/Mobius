import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
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
  await expect(main.getByText("+ Water the roses in the morning.")).toBeVisible();
  await expect(main.getByText("owner", { exact: true })).toBeVisible();

  await main.getByRole("button", { name: "Edit" }).click();
  await main.getByLabel("Memory text").fill("Water the roses.\n".repeat(201));
  await main.getByRole("button", { name: "Save" }).click();
  await expect(main.getByText("the maximum is 200")).toBeVisible();
  await main.getByRole("button", { name: "Cancel" }).click();

  await main.getByRole("button", { name: "Revert" }).click();
  await expect(main.getByText("The memory file is empty.")).toBeVisible();
  await expect(main.getByText("- Water the roses in the morning.")).toBeVisible();
});
