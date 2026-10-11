import { expect, type Page, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const tool = { name: "git", path: "/usr/bin/git", status: "", version: "git version 2.50.0" };
const permission = { name: "issues", level: "write", status: "present", url: "" } as const;
const label = { name: "mobius:ready", color: "0e8a16", found: "", status: "present" } as const;
const healthy = {
  labelFix: "none",
  permissions: [permission],
  permissionsError: "",
  repositories: [{ repository: "shop", labels: [label], maintenanceWorkstream: 7 }],
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
  await routeCheckup(page, [tool, { ...tool, name: "tar", status: "no-version", version: "" }], {
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
          maintenanceWorkstream: 7,
        },
      ],
    },
  });

  await page.goto("/settings/checkup");

  await expect(row("Tools").getByText("needs you")).toBeVisible();
  await expect(row("App permissions", "owner").getByText("needs you")).toBeVisible();
  await expect(row("Labels", "owner").getByText("needs you")).toBeHidden();
  await expect(row("Labels", "plants").getByText("needs you")).toBeVisible();
  await expect(row("App permissions", "plants").getByText("needs you")).toBeHidden();
});

test("the Checkup page shows a needs you badge on Workstreams when a repository has no maintenance Workstream", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const row = (section: string) =>
    main.locator("section").filter({ hasText: section }).getByRole("link", { name: "Workstreams" });
  await routeCheckup(page, [tool], {
    owner: healthy,
    plants: {
      ...healthy,
      repositories: [{ repository: "garden", labels: [label], maintenanceWorkstream: 0 }],
    },
  });

  await page.goto("/settings/checkup");

  await expect(row("plants").getByText("needs you")).toBeVisible();
  await expect(row("owner").getByText("needs you")).toBeHidden();
});

test("the Workstreams page links the maintenance Workstream and sends the draft request to the Triager chat", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await routeCheckup(page, [tool], {
    plants: {
      ...healthy,
      repositories: [
        { repository: "plants/garden", labels: [label], maintenanceWorkstream: 0 },
        { repository: "plants/pond", labels: [label], maintenanceWorkstream: 9 },
      ],
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
  const link = main
    .locator("section")
    .filter({ hasText: "plants/pond" })
    .getByRole("link", { name: "Maintenance Workstream #9" });
  await expect(link).toHaveAttribute("href", "/workstreams/plants/pond/9");
  await expect(main.getByRole("button", { name: "Create" })).toHaveCount(1);

  const request = page.waitForRequest((req) => req.url().endsWith("/api/chat/messages"));
  await main
    .locator("section")
    .filter({ hasText: "plants/garden" })
    .getByRole("button", { name: "Create" })
    .click();

  const body = (await request).postData() ?? "";
  expect(body).toContain("Draft the maintenance Workstream for plants/garden.");
  expect(body).toContain('name="organization"\r\n\r\nplants\r\n');
  expect(body).toContain('name="repository"\r\n\r\n\r\n');
  await expect(page).toHaveURL("/chat");
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
            maintenanceWorkstream: 7,
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

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });

  test("the Tools page wraps a long version and does not overflow", async ({ page }) => {
    const version =
      "curl 8.7.1 (x86_64-apple-darwin23.0) libcurl/8.7.1 (SecureTransport) LibreSSL/3.3.6 zlib/1.2.12 nghttp2/1.61.0";
    await routeCheckup(page, [{ ...tool, name: "curl", path: "/usr/bin/curl", version }], {});

    await page.goto("/settings/checkup/tools");

    await expect(page.getByRole("main").getByText(version)).toBeVisible();
    expect(await page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")).toBe(
      true,
    );
  });
});
