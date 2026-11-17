import type { Page } from "@playwright/test";

const base = {
  dismissedAt: null,
  action: null,
  answer: "",
  mergeRefusal: "",
  merging: false,
  todo: null,
  withdrawn: false,
  questions: [],
  organization: "owner",
  repository: "",
  workstreamRepository: "",
  workstream: 0,
  issue: 0,
  link: "",
  time: "2026-09-28T09:30:00Z",
  reason: "",
  pullRequest: 0,
  pullRequestUrl: "",
  reportUrl: "",
  blockingTodos: [],
  mergeQueuePlace: null,
  mergeQueueSize: 0,
};

export const missingMaintenanceItem = {
  ...base,
  id: 301,
  kind: "missing maintenance",
  title: "Maintenance Workstream is missing",
  text: "The organization has no Maintenance Workstream. A CI failure and a CI break of the default branch get no issue. A check timeout ends with no issue. The Curator cannot create issues.",
};

export const missingSkillsItem = {
  ...base,
  id: 302,
  kind: "missing skills",
  title: "Skills Workstream is missing",
  text: "The organization has no Skills Workstream. The tool propose_skill refuses each skill proposal of the organization.",
};

export const setupCheckup = {
  labelFix: "none",
  permissions: [],
  permissionsError: "",
  repositories: [
    { repository: "owner/shed", labels: [] },
    { repository: "owner/shop", labels: [] },
  ],
  issueRepository: "owner/shop",
  issueRepositoryError: "",
  issueRepositorySource: "choice",
  maintenanceWorkstream: 0,
  skillRepository: "owner/mobius-skills",
  skillsWorkstreamRepository: "",
  skillsWorkstream: 0,
};

export const noIssueRepositoryCheckup = {
  ...setupCheckup,
  issueRepository: "",
  issueRepositoryError:
    'Mobius needs the issue repository of owner. Select it on the Checkup page "Workstreams", or set organizations.owner.issue_repository in config.toml.',
  issueRepositorySource: "none",
};

export const noSkillRepositoryCheckup = { ...setupCheckup, skillRepository: "" };

export const checkupRoute = (url: URL) => url.pathname === "/api/checkup";

export function routeCheckup(page: Page, checkup: () => object) {
  return page.route(checkupRoute, (route) => route.fulfill({ json: { data: checkup() } }));
}
