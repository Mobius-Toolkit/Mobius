import { expect, type Page, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const tool = {
  name: "git",
  path: "/usr/bin/git",
  status: "",
  version: "git version 2.50.0",
  required: true,
};
const permission = { name: "issues", level: "write", status: "present", url: "" } as const;
const label = { name: "mobius:ready", color: "0e8a16", found: "", status: "present" } as const;
const healthy = {
  labelFix: "none",
  permissions: [permission],
  permissionsError: "",
  repositories: [{ repository: "shop", labels: [label] }],
  issueRepository: "shop",
  issueRepositoryError: "",
  maintenanceWorkstream: 7,
  skillRepository: "",
  skillsWorkstreamRepository: "",
  skillsWorkstream: 0,
};

async function routeCheckup(
  page: Page,
  tools: unknown[],
  checkups: Record<string, unknown>,
  holds: Record<string, Promise<unknown>> = {},
) {
  await page.route(
    (url) => url.pathname === "/api/checkup/tools",
    (route) => route.fulfill({ json: { data: tools } }),
  );
  await page.route(
    (url) => url.pathname === "/api/checkup",
    async (route) => {
      const organization = new URL(route.request().url()).searchParams.get("organization") ?? "";
      await holds[organization];
      return route.fulfill({ json: { data: checkups[organization] } });
    },
  );
}

test("the Checkup page shows a needs you badge on each row with a problem", async ({ page }) => {
  const main = page.getByRole("main");
  const row = (name: string, section?: string) =>
    (section ? main.locator("section").filter({ hasText: section }) : main)
      .getByRole("link", { name })
      .first();
  await routeCheckup(
    page,
    [tool, { ...tool, name: "devin", path: "/usr/bin/devin", status: "no-version", version: "" }],
    {
      owner: {
        ...healthy,
        permissions: [permission, { ...permission, name: "checks", status: "missing" }],
      },
      plants: {
        ...healthy,
        repositories: [
          {
            repository: "garden",
            labels: [{ ...label, status: "wrong-case", found: "Mobius:Ready" }],
          },
        ],
      },
    },
  );

  await page.goto("/settings/checkup");

  await expect(row("Tools").getByText("needs you")).toBeVisible();
  await expect(row("App permissions", "owner").getByText("needs you")).toBeVisible();
  await expect(row("Labels", "owner").getByText("needs you")).toBeHidden();
  await expect(row("Labels", "plants").getByText("needs you")).toBeVisible();
  await expect(row("App permissions", "plants").getByText("needs you")).toBeHidden();
});

test("the Checkup page shows an outdated badge and the newest version of an outdated tool", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const outdated = {
    ...tool,
    name: "claude",
    path: "/usr/bin/claude",
    version: "2.1.284 (Claude Code)",
    newest: "2.1.300",
    outdated: true,
  };
  await routeCheckup(page, [tool, outdated], { owner: healthy, plants: healthy });

  await page.goto("/settings/checkup");

  await expect(main.getByRole("link", { name: "Tools" }).getByText("needs you")).toBeVisible();

  await main.getByRole("link", { name: "Tools" }).click();

  await expect(main.getByText("outdated")).toHaveCount(1);
  await expect(main.getByText("2.1.284 (Claude Code) · newest 2.1.300")).toBeVisible();
  await expect(main.getByText("git version 2.50.0", { exact: true })).toBeVisible();
});

test("the Checkup page shows an optional program that is not found with no badge", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const dsh = {
    ...tool,
    name: "dsh",
    path: "",
    version: "",
    status: "not-found",
    required: false,
  };
  await routeCheckup(page, [tool, dsh], { owner: healthy, plants: healthy });

  const loaded = page.waitForResponse((res) => res.url().endsWith("/api/checkup/tools"));
  await page.goto("/settings/checkup");
  await loaded;

  const toolsLink = main.getByRole("link", { name: "Tools" });
  await expect(toolsLink).toHaveAttribute("aria-busy", "false");
  await expect(toolsLink.getByText("needs you")).toBeHidden();

  await toolsLink.click();

  const row = main.getByRole("listitem").filter({ hasText: "dsh" });
  await expect(row.getByText("not found")).toBeVisible();
  await expect(row.locator('[data-variant="destructive"]')).toHaveCount(0);
});

test("the Checkup page shows a required program that is not found with the badge", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const devin = { ...tool, name: "devin", path: "", version: "", status: "not-found" };
  await routeCheckup(page, [tool, devin], { owner: healthy, plants: healthy });

  const loaded = page.waitForResponse((res) => res.url().endsWith("/api/checkup/tools"));
  await page.goto("/settings/checkup");
  await loaded;

  await expect(main.getByRole("link", { name: "Tools" }).getByText("needs you")).toBeVisible();

  await main.getByRole("link", { name: "Tools" }).click();

  const row = main.getByRole("listitem").filter({ hasText: "devin" });
  await expect(row.getByText("not found")).toBeVisible();
  await expect(row.locator('[data-variant="destructive"]')).toHaveCount(1);
});

test("the Checkup page shows a needs you badge on Workstreams when the issue repository has no maintenance Workstream", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const row = (section: string) =>
    main.locator("section").filter({ hasText: section }).getByRole("link", { name: "Workstreams" });
  await routeCheckup(page, [tool], {
    owner: healthy,
    plants: { ...healthy, issueRepository: "garden", maintenanceWorkstream: 0 },
  });

  await page.goto("/settings/checkup");

  await expect(row("plants").getByText("needs you")).toBeVisible();
  await expect(row("owner").getByText("needs you")).toBeHidden();
});

test("the Checkup page shows a needs you badge on Workstreams when the skill repository has no Skills Workstream", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const row = (section: string) =>
    main.locator("section").filter({ hasText: section }).getByRole("link", { name: "Workstreams" });
  await routeCheckup(page, [tool], {
    owner: { ...healthy, skillRepository: "owner/mobius-skills" },
    plants: {
      ...healthy,
      skillRepository: "plants/mobius-skills",
      skillsWorkstreamRepository: "plants/garden",
      skillsWorkstream: 9,
    },
  });

  await page.goto("/settings/checkup");

  await expect(row("owner").getByText("needs you")).toBeVisible();
  await expect(row("plants").getByText("needs you")).toBeHidden();
});

test("the Workstreams page explains the Workstreams, links them and sends the full request of the maintenance Workstream to the Coordinator chat", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await routeCheckup(page, [tool], {
    plants: {
      ...healthy,
      issueRepository: "plants/garden",
      maintenanceWorkstream: 0,
      skillRepository: "plants/mobius-skills",
      skillsWorkstreamRepository: "plants/garden",
      skillsWorkstream: 9,
    },
  });
  await page.route(
    (url) => url.pathname === "/api/chat/messages",
    (route) => route.fulfill({ status: 204 }),
  );

  await page.goto("/settings/checkup");
  await main
    .locator("section")
    .filter({ hasText: "plants" })
    .getByRole("link", { name: "Workstreams" })
    .click();

  await expect(page).toHaveURL("/settings/checkup/plants/workstreams");
  await expect(
    main.getByText(
      "The maintenance Workstream gets each task issue that Mobius creates and the CI failures of the default branch of each repository of the organization. The Skills Workstream gets the skill proposals of the agents. Create opens the chat with a draft for your approval.",
    ),
  ).toBeVisible();
  const link = main
    .locator("section")
    .filter({ hasText: "plants/garden" })
    .getByRole("link", { name: "Skills Workstream #9" });
  await expect(link).toHaveAttribute("href", "/workstreams/plants/garden/9");
  await expect(main.getByRole("button", { name: "Create" })).toHaveCount(1);

  const request = page.waitForRequest((req) => req.url().endsWith("/api/chat/messages"));
  await main
    .locator("section")
    .filter({ hasText: "plants/garden" })
    .getByRole("button", { name: "Create" })
    .click();

  const body = (await request).postData() ?? "";
  const text = body.replaceAll("\r\n", "\n");
  expect(text).toContain(
    "Create the maintenance Workstream for plants/garden, with the labels `mobius:maintenance` and `mobius:evergreen`.\n\nUse this Brief:\n\n**Goal**\nKeep the default branch of each repository of the organization healthy, so that the work of the other Workstreams can merge. This Workstream has no end.\n\n**Scope**\n- Each task issue that Mobius creates for a repository of the organization.\n- Failures of the CI on the default branch of each repository, and flaky tests.\n- The development tools, the scripts, the project setup, and the CI configuration.\n- Small defects of the applications that no open Workstream covers.\n- When the cause of a failure is outside the repository, close the issue with the cause in a comment.\n\n**Limits**\n- Do not add a feature or change a user flow.\n- Do not take work that belongs to another open Workstream.",
  );
  expect(body).toContain('name="organization"\r\n\r\nplants\r\n');
  expect(body).toContain('name="repository"\r\n\r\n\r\n');
  await expect(page).toHaveURL("/chat");
});

test("the Workstreams page links the maintenance Workstream and sends the full request of the Skills Workstream to the Coordinator chat", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await routeCheckup(page, [tool], {
    plants: {
      ...healthy,
      issueRepository: "plants/garden",
      maintenanceWorkstream: 9,
      skillRepository: "plants/mobius-skills",
    },
  });
  await page.route(
    (url) => url.pathname === "/api/chat/messages",
    (route) => route.fulfill({ status: 204 }),
  );

  await page.goto("/settings/checkup/plants/workstreams");

  const link = main
    .locator("section")
    .filter({ hasText: "plants/garden" })
    .getByRole("link", { name: "Maintenance Workstream #9" });
  await expect(link).toHaveAttribute("href", "/workstreams/plants/garden/9");
  await expect(main.getByRole("button", { name: "Create" })).toHaveCount(1);

  const request = page.waitForRequest((req) => req.url().endsWith("/api/chat/messages"));
  await main
    .locator("section")
    .filter({ hasText: "plants/garden" })
    .getByRole("button", { name: "Create" })
    .click();

  const body = (await request).postData() ?? "";
  const text = body.replaceAll("\r\n", "\n");
  expect(text).toContain(
    "Create the Skills Workstream in plants/garden, with the labels `mobius:skills` and `mobius:evergreen`. It gets the proposals of `propose_skill` for plants/mobius-skills.\n\nUse this Brief:\n\n**Goal**\nKeep the skills of the agents in plants/mobius-skills correct and current. This Workstream has no end.\n\n**Scope**\n- Each skill proposal that an agent sends with `propose_skill`.\n- The task issues and the pull requests that add or change a skill are in plants/mobius-skills.\n\n**Limits**\n- Change only plants/mobius-skills.\n- Do not add a skill that the Owner did not approve.",
  );
  await expect(page).toHaveURL("/chat");
});

test("the Workstreams page shows the error of the issue repository in place of the maintenance Workstream", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const error =
    "Mobius needs the issue repository of plants. Set organizations.plants.issue_repository in config.toml.";
  await routeCheckup(page, [tool], {
    plants: {
      ...healthy,
      issueRepository: "",
      issueRepositoryError: error,
      maintenanceWorkstream: 0,
    },
  });

  await page.goto("/settings/checkup/plants/workstreams");

  await expect(main.getByText(error)).toBeVisible();
  await expect(main.getByText("Maintenance Workstream", { exact: true })).toBeHidden();
  await expect(main.getByRole("button", { name: "Create" })).toHaveCount(0);
});

test("the Checkup page shows no badge on a row with no problem", async ({ page }) => {
  const main = page.getByRole("main");
  const loaded = Promise.all(
    ["/api/checkup/tools", "/api/checkup?organization=owner"].map((path) =>
      page.waitForResponse((res) => res.url().endsWith(path)),
    ),
  );
  await routeCheckup(
    page,
    [tool],
    {
      owner: healthy,
      plants: {
        ...healthy,
        repositories: [
          {
            repository: "garden",
            labels: [{ ...label, status: "wrong-case", found: "Mobius:Ready" }],
          },
        ],
      },
    },
    { plants: loaded },
  );

  await page.goto("/settings/checkup");

  await expect(
    main
      .locator("section")
      .filter({ hasText: "plants" })
      .getByRole("link", { name: "Labels" })
      .getByText("needs you"),
  ).toBeVisible();
  await expect(main.getByText("needs you")).toHaveCount(1);
});

test("the Checkup page shows no badge when a request fails", async ({ page }) => {
  const main = page.getByRole("main");
  await page.route(
    (url) => url.pathname.startsWith("/api/checkup"),
    (route) => route.fulfill({ status: 500, json: { error: "The server failed." } }),
  );

  await page.goto("/settings/checkup");

  await expect(main.getByRole("link", { name: "Tools" })).toBeVisible();
  await expect(main.getByText("needs you")).toBeHidden();
  await main.getByRole("link", { name: "Tools" }).click();
  await expect(page).toHaveURL("/settings/checkup/tools");
});

test("the Tools page upgrades an outdated package and shows the new version and the notice", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const outdated = {
    ...tool,
    name: "dsh",
    path: "/usr/local/bin/dsh",
    version: "0.2.1-rc.4",
    newest: "0.2.1-rc.5",
    outdated: true,
    package: "@deepseek-ai/dsh",
  };
  const upgraded = { ...outdated, version: "0.2.1-rc.5", outdated: false };
  await page.route(
    (url) => url.pathname === "/api/checkup/tools/upgrade",
    (route) => route.fulfill({ json: { data: [upgraded] } }),
  );
  await routeCheckup(page, [outdated], { owner: healthy, plants: healthy });

  await page.goto("/settings/checkup/tools");
  await main.getByRole("button", { name: /dsh/ }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Installed: 0.2.1-rc.4")).toBeVisible();
  await expect(dialog.getByText("Newest: 0.2.1-rc.5")).toBeVisible();

  await dialog.getByRole("button", { name: "Upgrade" }).click();

  await expect(dialog.getByText("Installed: 0.2.1-rc.5")).toBeVisible();
  await expect(page.locator("main").getByText("0.2.1-rc.5", { exact: true })).toBeVisible();
  await expect(page.getByText("Upgraded dsh to 0.2.1-rc.5")).toBeVisible();
});

test("a failed upgrade shows the npm error and keeps the old version", async ({ page }) => {
  const main = page.getByRole("main");
  const outdated = {
    ...tool,
    name: "dsh",
    path: "/usr/local/bin/dsh",
    version: "0.2.1-rc.4",
    newest: "0.2.1-rc.5",
    outdated: true,
    package: "@deepseek-ai/dsh",
  };
  await page.route(
    (url) => url.pathname === "/api/checkup/tools/upgrade",
    (route) => route.fulfill({ status: 409, json: { error: "npm ERR! network down" } }),
  );
  await routeCheckup(page, [outdated], { owner: healthy, plants: healthy });

  await page.goto("/settings/checkup/tools");
  await main.getByRole("button", { name: /dsh/ }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Upgrade" }).click();

  await expect(dialog.getByText("npm ERR! network down")).toBeVisible();
  await expect(dialog.getByText("Installed: 0.2.1-rc.4")).toBeVisible();
  await expect(page.locator("main").getByText("0.2.1-rc.4 · newest 0.2.1-rc.5")).toBeVisible();
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });

  test("the Tools page wraps a long version and does not overflow", async ({ page }) => {
    const version =
      "devin 3000.11.3 (9c803229faa4) a long version line that wraps on a phone screen of 390 pixels";
    await routeCheckup(page, [{ ...tool, name: "devin", path: "/usr/bin/devin", version }], {});

    await page.goto("/settings/checkup/tools");

    await expect(page.getByRole("main").getByText(version)).toBeVisible();
    expect(await page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")).toBe(
      true,
    );
  });
});
