import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import {
  missingMaintenanceItem,
  missingSkillsItem,
  noIssueRepositoryCheckup,
  noSkillRepositoryCheckup,
  routeCheckup,
  setupCheckup,
} from "./missing-workstreams.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const routeInbox = (page: Page, list: () => object[]) =>
  page.route("**/api/inbox", (route) => route.fulfill({ json: { data: list() } }));

const routeChatMessages = (page: Page) =>
  page.route(
    (url) => url.pathname === "/api/chat/messages",
    (route) => route.fulfill({ status: 204 }),
  );

async function reconnect(page: Page, context: BrowserContext) {
  await context.setOffline(true);
  await expect(page.getByRole("status").filter({ hasText: "Offline" })).toHaveText("Offline");
  const reopened = page.waitForResponse("/api/events");
  await context.setOffline(false);
  await reopened;
}

const row = (page: Page, id: number) => page.getByRole("main").locator(`[data-inbox-item="${id}"]`);

test("the Coordinator chat shows a row for each missing Workstream, Create adds the request and Dismiss removes the row", async ({
  page,
  context,
}) => {
  const main = page.getByRole("main");
  let current = [missingMaintenanceItem, missingSkillsItem];
  await routeInbox(page, () => current);
  await routeCheckup(page, () => setupCheckup);
  await routeChatMessages(page);
  await page.route("**/api/inbox/301/dismiss", (route) => {
    current = [missingSkillsItem];
    return route.fulfill({ status: 204 });
  });

  await page.goto("/chat");

  await expect(main.getByRole("heading", { name: "Waits for you (2)" })).toBeVisible();
  const maintenance = row(page, 301);
  const skills = row(page, 302);
  await expect(maintenance).toContainText("Maintenance Workstream is missing");
  await expect(maintenance).toContainText(
    "The organization has no Maintenance Workstream. A CI failure and a CI break of the default branch get no issue.",
  );
  await expect(maintenance.getByRole("button", { name: "Create" })).toBeVisible();
  await expect(skills).toContainText("Skills Workstream is missing");
  await expect(skills).toContainText("The tool propose_skill refuses each skill proposal");
  await expect(skills.getByRole("button", { name: "Create" })).toBeVisible();

  const request = page.waitForRequest((req) => req.url().endsWith("/api/chat/messages"));
  await skills.getByRole("button", { name: "Create" }).click();

  const body = ((await request).postData() ?? "").replaceAll("\r\n", "\n");
  expect(body).toContain(
    "Create the Skills Workstream in owner/shop, with the labels `mobius:skills` and `mobius:evergreen`. It gets the proposals of `propose_skill` for owner/mobius-skills.",
  );
  expect(body).toContain('name="organization"\n\nowner\n');
  await expect(skills.getByRole("button", { name: "Create" })).toBeEnabled();

  const dismissed = page.waitForResponse("**/api/inbox/301/dismiss");
  await maintenance.getByRole("button", { name: "Dismiss" }).click();
  await dismissed;

  await expect(page.getByText("Dismissed: Maintenance Workstream is missing")).toBeVisible();
  await reconnect(page, context);
  await expect(maintenance).toHaveCount(0);
  await expect(skills).toHaveCount(1);
  await expect(main.getByRole("heading", { name: "Waits for you (1)" })).toBeVisible();
});

test("the row of an organization with no issue repository shows None and no Create button until the Owner selects the repository", async ({
  page,
}) => {
  const main = page.getByRole("main");
  let saved = false;
  await routeInbox(page, () => [missingMaintenanceItem, missingSkillsItem]);
  await routeCheckup(page, () =>
    saved ? { ...setupCheckup, issueRepository: "owner/shed" } : noIssueRepositoryCheckup,
  );
  await page.route(
    (url) => url.pathname === "/api/checkup/issue-repository",
    (route) => {
      saved = true;
      return route.fulfill({ json: { data: { organization: "owner", repository: "owner/shed" } } });
    },
  );

  await page.goto("/chat");

  const none = main.getByRole("button", { name: "Issue repository None", exact: true });
  await expect(none).toHaveCount(2);
  await expect(main.getByRole("button", { name: "Create" })).toHaveCount(0);

  await none.first().click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toHaveCount(1);
  await expect(dialog.getByRole("radio")).toHaveCount(2);
  await dialog.getByRole("radio", { name: "owner/shed" }).click();
  await expect(dialog.getByRole("radio", { name: "owner/shed" })).toBeChecked();
  const response = page.waitForResponse(
    (res) =>
      res.url().endsWith("/api/checkup/issue-repository") && res.request().method() === "PUT",
  );
  await dialog.getByRole("button", { name: "Save" }).click();

  expect((await response).status()).toBe(200);
  await expect(dialog).toBeHidden();
  await expect(main.getByRole("button", { name: "Create" })).toHaveCount(2);
  await expect(none).toHaveCount(0);
});

test("the Skills row with no skill repository shows the Open GitHub link and no Create button", async ({
  page,
}) => {
  await routeInbox(page, () => [missingSkillsItem]);
  await routeCheckup(page, () => noSkillRepositoryCheckup);

  await page.goto("/chat");

  const skills = row(page, 302);
  await expect(
    skills.getByText("Create owner/mobius-skills and give the Mobius App access to it."),
  ).toBeVisible();
  const link = skills.getByRole("link", { name: "Open GitHub" });
  await expect(link).toHaveAttribute(
    "href",
    "https://github.com/new?owner=owner&name=mobius-skills",
  );
  await expect(skills.getByRole("button", { name: "Create" })).toHaveCount(0);
});

test("the Inbox shows the row of a missing Workstream and Create opens the Coordinator chat", async ({
  page,
}) => {
  await routeInbox(page, () => [missingMaintenanceItem]);
  await routeCheckup(page, () => setupCheckup);
  await routeChatMessages(page);

  await page.goto("/inbox");

  const maintenance = row(page, 301);
  await expect(maintenance).toContainText("Maintenance Workstream is missing");
  await expect(maintenance.getByRole("button", { name: "Dismiss" })).toBeVisible();
  const request = page.waitForRequest((req) => req.url().endsWith("/api/chat/messages"));
  await maintenance.getByRole("button", { name: "Create" }).click();

  const body = ((await request).postData() ?? "").replaceAll("\r\n", "\n");
  expect(body).toContain(
    "Create the maintenance Workstream for owner/shop, with the labels `mobius:maintenance` and `mobius:evergreen`.",
  );
  expect(body).toContain('name="organization"\n\nowner\n');
  await expect(page).toHaveURL("/chat");
});
