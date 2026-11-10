import { expect, test, type Page } from "@playwright/test";
import { addMcpServer, deleteMcpServers } from "./mcp-servers.js";

const organization = "owner";
const listPath = `/api/organizations/${organization}/mcp-servers`;

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

// The tests of this spec are the only users of the servers of the organization owner.
test.afterEach(async ({ page }) => {
  const ids = await page.evaluate(
    (path) =>
      fetch(path)
        .then((response) => response.json() as Promise<{ data: { id: number }[] }>)
        .then(({ data }) => data.map((server) => server.id)),
    listPath,
  );
  await deleteMcpServers(page, organization, ids);
});

const row = (page: Page, name: string) =>
  page
    .getByRole("listitem")
    .filter({ hasText: name, has: page.getByRole("button", { name: "Edit" }) });

const sheet = (page: Page, title: string) => page.getByRole("dialog", { name: title });

const notice = (page: Page, text: string) => page.getByRole("status").filter({ hasText: text });

const openPage = async (page: Page) => {
  await page.goto("/settings/mcp-servers");
  await expect(page.getByRole("button", { name: "Add a server" })).toBeVisible();
};

const write = (page: Page, method: "POST" | "PUT") =>
  page.waitForResponse((res) => res.url().includes(listPath) && res.request().method() === method);

const save = async (page: Page, title: string, method: "POST" | "PUT") => {
  const written = write(page, method);
  await sheet(page, title).getByRole("button", { name: "Save" }).click();
  return written;
};

test("the Owner adds an HTTP server with a header and a stdio server with an argument and an env item, and the list masks their secrets", async ({
  page,
}) => {
  await openPage(page);
  await expect(page.getByText("No MCP servers.")).toBeVisible();

  await page.getByRole("button", { name: "Add a server" }).click();
  const adding = sheet(page, "Add a server");
  await expect(adding.getByRole("button", { name: "Delete" })).toHaveCount(0);
  await adding.getByLabel("Name", { exact: true }).fill("e2e-http");
  await adding.getByLabel("URL").fill("https://mcp.example.com/mcp");
  await adding.getByRole("button", { name: "Add a header" }).click();
  await adding.getByLabel("Name of header 1").fill("Authorization");
  await adding.getByLabel("Value of header 1").fill("Bearer secret-token");
  const first = await save(page, "Add a server", "POST");
  expect(first.status()).toBe(201);
  expect(first.request().postDataJSON()).toMatchObject({
    name: "e2e-http",
    kind: "http",
    url: "https://mcp.example.com/mcp",
    headers: [{ name: "Authorization", value: "Bearer secret-token" }],
  });
  await expect(notice(page, "Added e2e-http")).toBeVisible();
  await expect(adding).toBeHidden();

  await page.getByRole("button", { name: "Add a server" }).click();
  await adding.getByLabel("Name", { exact: true }).fill("e2e-stdio");
  await adding.getByLabel("stdio").check();
  await adding.getByLabel("Command").fill("npx");
  await adding.getByLabel("Arguments").fill("-y\n@acme/search-mcp");
  await adding.getByRole("button", { name: "Add a variable" }).click();
  await adding.getByLabel("Name of variable 1").fill("API_KEY");
  await adding.getByLabel("Value of variable 1").fill("secret-key");
  const second = await save(page, "Add a server", "POST");
  expect(second.status()).toBe(201);
  expect(second.request().postDataJSON()).toMatchObject({
    name: "e2e-stdio",
    kind: "stdio",
    command: "npx",
    args: ["-y", "@acme/search-mcp"],
    env: [{ name: "API_KEY", value: "secret-key" }],
  });

  const http = row(page, "e2e-http");
  await expect(http).toContainText("HTTP");
  await expect(http).toContainText("https://mcp.example.com/mcp");
  await expect(http).toContainText("Authorization ••••••");
  const stdio = row(page, "e2e-stdio");
  await expect(stdio).toContainText("stdio");
  await expect(stdio).toContainText("npx -y @acme/search-mcp");
  await expect(stdio).toContainText("API_KEY ••••••");
  await expect(page.getByText("secret-token")).toHaveCount(0);
  await expect(page.getByText("secret-key")).toHaveCount(0);

  await page.reload();
  await expect(row(page, "e2e-http")).toContainText("Authorization ••••••");
  await expect(row(page, "e2e-stdio")).toContainText("API_KEY ••••••");
});

test("an edit that leaves a secret field empty sends an empty value for that item", async ({
  page,
}) => {
  await openPage(page);
  await addMcpServer(page, organization, {
    name: "e2e-http",
    kind: "http",
    url: "https://mcp.example.com/mcp",
    headers: [{ name: "Authorization", value: "Bearer secret-token" }],
  });
  await page.reload();

  await row(page, "e2e-http").getByRole("button", { name: "Edit" }).click();
  const editing = sheet(page, "Edit e2e-http");
  await expect(editing.getByLabel("Name of header 1")).toHaveValue("Authorization");
  const value = editing.getByLabel("Value of header 1");
  await expect(value).toHaveValue("");
  await expect(value).toHaveAttribute("placeholder", "••••••");
  await editing.getByLabel("URL").fill("https://mcp.example.com/v2");
  const changed = await save(page, "Edit e2e-http", "PUT");
  expect(changed.status()).toBe(200);
  expect(changed.request().postDataJSON()).toMatchObject({
    url: "https://mcp.example.com/v2",
    headers: [{ name: "Authorization", value: "" }],
  });
  await expect(notice(page, "Changed e2e-http")).toBeVisible();
  await expect(row(page, "e2e-http")).toContainText("https://mcp.example.com/v2");
  await expect(row(page, "e2e-http")).toContainText("Authorization ••••••");
});

test("a used name shows its reason in the sheet, and the sheet stays open", async ({ page }) => {
  await openPage(page);
  await addMcpServer(page, organization, {
    name: "e2e-http",
    kind: "http",
    url: "https://mcp.example.com/mcp",
  });
  await page.reload();

  await page.getByRole("button", { name: "Add a server" }).click();
  const adding = sheet(page, "Add a server");
  await adding.getByLabel("Name", { exact: true }).fill("e2e-http");
  await adding.getByLabel("URL").fill("https://mcp.example.com/other");
  const refused = await save(page, "Add a server", "POST");
  expect(refused.status()).toBe(409);
  await expect(
    adding.getByText("another MCP server of the organization has this name"),
  ).toBeVisible();
  await expect(adding.getByRole("button", { name: "Save" })).toBeEnabled();
});

test("the Owner deletes a server after the confirmation, and the list no longer shows it", async ({
  page,
}) => {
  await openPage(page);
  await addMcpServer(page, organization, {
    name: "e2e-http",
    kind: "http",
    url: "https://mcp.example.com/mcp",
  });
  await page.reload();

  await row(page, "e2e-http").getByRole("button", { name: "Edit" }).click();
  await sheet(page, "Edit e2e-http").getByRole("button", { name: "Delete" }).click();
  const confirmation = sheet(page, "Delete e2e-http");
  await confirmation.getByRole("button", { name: "Cancel" }).click();
  await expect(confirmation).toBeHidden();
  await expect(sheet(page, "Edit e2e-http")).toBeVisible();

  await sheet(page, "Edit e2e-http").getByRole("button", { name: "Delete" }).click();
  const deleted = page.waitForResponse(
    (res) => res.url().includes(listPath) && res.request().method() === "DELETE",
  );
  await confirmation.getByRole("button", { name: "Delete" }).click();
  expect((await deleted).status()).toBe(204);
  await expect(notice(page, "Deleted e2e-http")).toBeVisible();
  await expect(row(page, "e2e-http")).toHaveCount(0);
  await expect(page.getByText("No MCP servers.")).toBeVisible();
});

test("Connect sends the browser to the url of the response", async ({ page }) => {
  await openPage(page);
  const id = await addMcpServer(page, organization, {
    name: "e2e-http",
    kind: "http",
    url: "https://mcp.example.com/mcp",
  });
  await page.reload();
  await page.route(`**${listPath}/${id}/oauth`, (route) =>
    route.fulfill({ json: { data: { url: "/e2e-authorize" } } }),
  );
  await page.route("**/e2e-authorize", (route) =>
    route.fulfill({ contentType: "text/html", body: "<p>Authorize</p>" }),
  );

  await row(page, "e2e-http").getByRole("button", { name: "Edit" }).click();
  const editing = sheet(page, "Edit e2e-http");
  await expect(editing.getByText("Not connected")).toBeVisible();
  await expect(editing.getByRole("button", { name: "Disconnect" })).toHaveCount(0);
  await editing.getByLabel("Client ID").fill("client");
  await editing.getByLabel("Client secret").fill("client-secret");
  const started = page.waitForRequest(
    (req) => req.url().endsWith(`/${id}/oauth`) && req.method() === "POST",
  );
  await editing.getByRole("button", { name: "Connect" }).click();
  expect((await started).postDataJSON()).toEqual({
    clientId: "client",
    clientSecret: "client-secret",
  });
  await expect(page).toHaveURL("/e2e-authorize");
  await expect(page.getByText("Authorize")).toBeVisible();
});

test("the result of the OAuth flow shows a notice, and the page removes the query", async ({
  page,
}) => {
  await openPage(page);
  const id = await addMcpServer(page, organization, {
    name: "e2e-http",
    kind: "http",
    url: "https://mcp.example.com/mcp",
  });

  await page.goto(`/settings/mcp-servers?oauth=connected&server=${id}`);
  await expect(notice(page, "Connected e2e-http")).toBeVisible();
  await expect(page).toHaveURL("/settings/mcp-servers");

  await page.goto(
    `/settings/mcp-servers?oauth=failed&server=${id}&reason=${encodeURIComponent("access denied")}`,
  );
  await expect(notice(page, "The connect of e2e-http failed: access denied")).toBeVisible();
  await expect(page).toHaveURL("/settings/mcp-servers");
  await expect(row(page, "e2e-http")).toBeVisible();
});

test("the result of the OAuth flow shows no second time after the Owner changes the organization", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await openPage(page);
  const id = await addMcpServer(page, organization, {
    name: "e2e-http",
    kind: "http",
    url: "https://mcp.example.com/mcp",
  });

  await page.goto(`/settings/mcp-servers?oauth=connected&server=${id}`);
  await expect(notice(page, "Connected e2e-http")).toBeVisible();
  await expect(page).toHaveURL("/settings/mcp-servers");
  await expect(notice(page, "Connected e2e-http")).toBeHidden({ timeout: 10_000 });

  const loaded = page.waitForResponse(
    (res) =>
      res.url().endsWith("/api/organizations/plants/mcp-servers") &&
      res.request().method() === "GET",
  );
  await page.getByLabel("Work in another organization").click();
  await page.getByRole("menuitemradio", { name: "plants" }).click();
  await loaded;
  // Foldkit renders in the next animation frame.
  await page.evaluate(
    () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))),
  );
  await expect(row(page, "e2e-http")).toHaveCount(0);
  await expect(notice(page, "Connected")).toHaveCount(0);
});

test("the Settings page links the page of the MCP servers", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings");
  await page.getByRole("main").getByRole("link", { name: "MCP servers" }).click();
  await expect(page).toHaveURL("/settings/mcp-servers");
  await expect(page.getByRole("button", { name: "Add a server" })).toBeVisible();
});
