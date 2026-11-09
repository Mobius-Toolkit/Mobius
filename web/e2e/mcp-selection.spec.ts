import { expect, test, type Page } from "@playwright/test";
import { addMcpServer, deleteMcpServers } from "./mcp-servers.js";

const organization = "plants";
const workstream = "/workstreams/plants/garden/18";
const putPath = "/api/workstreams/plants/garden/18/mcp-servers";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

let ids: number[] = [];

// A delete of a server also removes it from the selection of each Workstream.
test.afterEach(async ({ page }) => {
  await deleteMcpServers(page, organization, ids);
  ids = [];
});

const addServers = async (page: Page) => {
  ids = [
    await addMcpServer(page, organization, {
      name: "makeadir",
      kind: "http",
      url: "https://mcp.example.com/mcp",
    }),
    await addMcpServer(page, organization, {
      name: "search",
      kind: "stdio",
      command: "search-mcp",
    }),
  ];
};

const openDetails = async (page: Page) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(workstream);
  await page.getByRole("tab", { name: "Details" }).filter({ visible: true }).click();
};

const panel = (page: Page) => page.getByRole("tabpanel", { name: "Details" });

const line = (page: Page) =>
  panel(page)
    .locator("dd")
    .filter({ has: page.getByRole("button", { name: "Change MCP servers" }) });

const sheet = (page: Page) => page.getByRole("dialog", { name: "MCP servers of #18" });

const save = async (page: Page) => {
  const saved = page.waitForResponse(
    (res) => res.url().endsWith(putPath) && res.request().method() === "PUT",
  );
  await sheet(page).getByRole("button", { name: "Save" }).click();
  const response = await saved;
  expect(response.status()).toBe(204);
  return response.request().postDataJSON() as { ids: number[] };
};

test("a Save of one checked server shows its name on the line, and the name stays after a reload", async ({
  page,
}) => {
  await addServers(page);
  await openDetails(page);
  await expect(line(page)).toContainText("None");

  await page.getByRole("button", { name: "Change MCP servers" }).click();
  await expect(sheet(page).getByText("HTTP")).toBeVisible();
  await expect(sheet(page).getByText("stdio")).toBeVisible();
  await sheet(page)
    .getByRole("checkbox", { name: /^search/ })
    .check();
  const body = await save(page);
  expect(body).toEqual({ ids: [ids[1]] });
  await expect(sheet(page)).toBeHidden();
  await expect(line(page)).toContainText("search");
  await expect(line(page)).not.toContainText("makeadir");
  await expect(
    page.getByRole("status").filter({ hasText: "Changed the MCP servers of #18" }),
  ).toBeVisible();

  await page.reload();
  await page.getByRole("tab", { name: "Details" }).filter({ visible: true }).click();
  await expect(line(page)).toContainText("search");

  await page.getByRole("button", { name: "Change MCP servers" }).click();
  await expect(sheet(page).getByRole("checkbox", { name: /^search/ })).toBeChecked();
  await expect(sheet(page).getByRole("checkbox", { name: /^makeadir/ })).not.toBeChecked();
});

test("a Save with no check shows None on the line", async ({ page }) => {
  await addServers(page);
  await openDetails(page);
  await page.getByRole("button", { name: "Change MCP servers" }).click();
  await sheet(page)
    .getByRole("checkbox", { name: /^makeadir/ })
    .check();
  expect(await save(page)).toEqual({ ids: [ids[0]] });
  await expect(line(page)).toContainText("makeadir");

  await page.getByRole("button", { name: "Change MCP servers" }).click();
  await sheet(page)
    .getByRole("checkbox", { name: /^makeadir/ })
    .uncheck();
  expect(await save(page)).toEqual({ ids: [] });
  await expect(line(page)).toHaveText(/None/);
  await expect(line(page)).not.toContainText("makeadir");
});

test("a Cancel keeps the line", async ({ page }) => {
  await addServers(page);
  await openDetails(page);
  await page.getByRole("button", { name: "Change MCP servers" }).click();
  await sheet(page)
    .getByRole("checkbox", { name: /^makeadir/ })
    .check();
  await sheet(page).getByRole("button", { name: "Cancel" }).click();
  await expect(sheet(page)).toBeHidden();
  await expect(line(page)).toContainText("None");
});

test("with no server in the organization, the sheet shows No MCP servers. and the link to the page of the servers", async ({
  page,
}) => {
  await openDetails(page);
  await expect(line(page)).toContainText("None");
  await page.getByRole("button", { name: "Change MCP servers" }).click();
  await expect(sheet(page).getByText("No MCP servers.")).toBeVisible();
  await expect(sheet(page).getByRole("checkbox")).toHaveCount(0);
  const link = sheet(page).getByRole("link", { name: "Add a server" });
  await expect(link).toHaveAttribute("href", "/settings/mcp-servers");
  // The marker stays only when the host router opens the page with no reload.
  await page.evaluate(() => Object.assign(window, { navigationMarker: true }));
  await link.click();
  await expect(sheet(page)).toBeHidden();
  await expect(page).not.toHaveURL(new RegExp(`${workstream}$`));
  expect(await page.evaluate(() => "navigationMarker" in window)).toBe(true);
});
