export const maintenanceWorkstreamRequest = (
  repository: string,
) => `Create the maintenance Workstream for ${repository}, with the labels \`mobius:maintenance\` and \`mobius:evergreen\`.

Use this Brief:

**Goal**
Keep the default branch of each repository of the organization healthy, so that the work of the other Workstreams can merge. This Workstream has no end.

**Scope**
- Each task issue that Mobius creates for a repository of the organization.
- Failures of the CI on the default branch of each repository, and flaky tests.
- The development tools, the scripts, the project setup, and the CI configuration.
- Small defects of the applications that no open Workstream covers.
- When the cause of a failure is outside the repository, close the issue with the cause in a comment.

**Limits**
- Do not add a feature or change a user flow.
- Do not take work that belongs to another open Workstream.`;

export const skillsWorkstreamRequest = (
  issueRepository: string,
  skillRepository: string,
) => `Create the Skills Workstream in ${issueRepository}, with the labels \`mobius:skills\` and \`mobius:evergreen\`. It gets the proposals of \`propose_skill\` for ${skillRepository}.

Use this Brief:

**Goal**
Keep the skills of the agents in ${skillRepository} correct and current. This Workstream has no end.

**Scope**
- Each skill proposal that an agent sends with \`propose_skill\`.
- The task issues and the pull requests that add or change a skill are in ${skillRepository}.

**Limits**
- Change only ${skillRepository}.
- Do not add a skill that the Owner did not approve.`;
