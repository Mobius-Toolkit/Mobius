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
  repositories: [{ repository: "shop", labels: [label] }],
};

async function routeCheckup(page: Page, tools: unknown[], checkups: Record<string, unknown>) {
  await page.route(
    (url) => url.pathname === "/api/checkup/tools",
    (route) => route.fulfill({ json: { data: tools } }),
  );
  await page.route(
    (url) => url.pathname === "/api/checkup",
    (route) => {
      const organization = new URL(route.request().url()).searchParams.get("organization") ?? "";
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
        },
      ],
    },
  });

  await page.goto("/settings/checkup");

  await expect(row("Tools").getByText("needs you")).toBeVisible();
  await expect(row("App permissions", "owner").getByText("needs you")).toBeVisible();
  await expect(row("Labels", "owner").getByText("needs you")).toBeHidden();
  await expect(row("App permissions", "plants").getByText("needs you")).toBeHidden();
  await expect(row("Labels", "plants").getByText("needs you")).toBeVisible();
});

test("the Checkup page shows no badge on a row with no problem", async ({ page }) => {
  const main = page.getByRole("main");
  await routeCheckup(page, [tool], { owner: healthy, plants: healthy });
  const loaded = Promise.all(
    [
      "/api/checkup/tools",
      "/api/checkup?organization=owner",
      "/api/checkup?organization=plants",
    ].map((path) => page.waitForResponse((res) => res.url().endsWith(path))),
  );

  await page.goto("/settings/checkup");

  await loaded;
  await expect(main.getByRole("link", { name: "Labels" })).toHaveCount(2);
  await expect(main.getByText("needs you")).toBeHidden();
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
