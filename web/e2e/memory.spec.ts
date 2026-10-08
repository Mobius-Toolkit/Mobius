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
  await expect(main.getByText("the maximum is 200")).toBeHidden();

  await main.getByRole("button", { name: "Edit" }).click();
  await main.getByLabel("Memory text").fill("Water the roses at noon.\n");
  await main.getByRole("button", { name: "Save" }).click();
  await expect(main.getByText("+ Water the roses at noon.")).toBeVisible();
  await expect(main.getByText("A later version changed this part. Edit the file.")).toBeVisible();
  const reverts = main.getByRole("button", { name: "Revert" });
  await expect(reverts.first()).toBeEnabled();
  await expect(reverts.last()).toBeDisabled();

  await reverts.first().click();
  await expect(main.getByText("- Water the roses at noon.")).toBeVisible();
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
