# Long-lived Workstreams on GitHub

Research for ticket [#18](https://github.com/Mobius-Toolkit/Mobius/issues/18). Date: 2026-09-26.

## Question

How can GitHub hold a long-lived Workstream, so that deterministic code routes each `mobius:ready` issue to exactly one Workstream? What did older methods use for long-lived areas of work?

## Short answer

- Three GitHub primitives give exactly one value for each issue by design: the parent issue, the milestone, and a single-select issue field. The issue type also has one value, but its purpose is the kind of work.
- Labels and Projects do not give this guarantee. An issue can have many labels and can be in many projects.
- A parent issue holds a maximum of 100 sub-issues. Thus one flat parent cannot hold thousands of tasks. A tree of up to 8 levels can hold them.
- Issue fields (general availability on 2026-07-02) are the closest match to a "Workstream" attribute. They exist only in organizations.
- Older methods keep two concepts apart: a long-lived area (Jira component, SAFe Agile Release Train, Kanban swimlane) and a finite container (epic, sprint, Shape Up cycle).

## 1. GitHub primitives

### 1.1 Parent issue and sub-issues

- A parent issue holds up to 100 sub-issues. The tree has up to 8 levels. [S1]
- The docs give no open-only exception to the 100 limit. The GraphQL `subIssuesSummary.total` value counts closed sub-issues too (live check: #3 shows `total: 27, completed: 9`). [S3] A user report says that closed and archived sub-issues count toward the limit. GitHub staff did not answer. [S4] (This is secondary evidence.)
- An issue has one parent. The GraphQL `Issue.parent` field is a single `Issue`, not a list. The `addSubIssue` input has `replaceParent: "Option to replace parent issue if one already exists"`. [S2] [S3]
- Sub-issues can be in other repositories and, since 2025-09-11, in other organizations. [S1] [S5]
- Sub-issues inherit the Project and the Milestone of their parent by default. [S5]
- REST endpoints: `GET /issues/{n}/parent`, `GET|POST /issues/{n}/sub_issues` (`per_page` max 100), `DELETE /issues/{n}/sub_issue`. Fast writes can cause secondary rate limits. [S2]
- The `sub_issues` webhook sends `parent_issue_added`, `parent_issue_removed`, `sub_issue_added`, `sub_issue_removed`. [S6]
- The GitHub CLI (v2.94.0 and later) has `--parent`, `--set-parent`, `--remove-parent`, and JSON fields for parent and sub-issues. [S7]
- Search: `has:parent-issue` and `no:parent-issue` work in the API (live check with `search(type: ISSUE_ADVANCED)`). Projects views filter with `parent-issue:OWNER/REPO#N` and can group by "Parent issue". [S8] [S9] In a live check, `parent-issue:Mobius-Toolkit/Mobius#3` in the GraphQL search API returned 0 results, although #3 has 27 sub-issues. Thus, read `Issue.parent` directly and do not rely on this search qualifier.

### 1.2 Labels

- Labels belong to one repository. A change in one repository has no effect on other repositories. [S10]
- The docs give no limit on labels for each issue. [S10] `Issue.labels` is a connection (a list). [S3]
- Thus labels give no exactly-one guarantee. Mobius must count the labels itself.
- The `issues` webhook sends `labeled` and `unlabeled`. [S6] The GraphQL `IssueFilters.labels` argument filters issues by label. [S3]

### 1.3 Milestones

- A milestone groups issues and pull requests in one repository. It shows a due date and a completion percentage. [S11]
- An issue has one milestone. `Issue.milestone` is a single object. [S3]
- Only `title` is required. `due_on` is optional, so a milestone can have no end date. [S12]
- A milestone has no hierarchy. The `issues` webhook sends `milestoned` and `demilestoned`. [S6]
- Milestones are the GitHub container for releases. A Workstream in the milestone slot removes that slot from release use.

### 1.4 Issue types

- Issue types exist at the organization level. An organization has up to 25 types. The defaults are task, bug, and feature. [S13]
- An issue has one type. `Issue.issueType` is a single object. [S3] The webhook sends `typed` and `untyped`. [S6]
- The purpose of an issue type is the kind of work, not the area. A Workstream in the type slot removes the bug/feature/task meaning. The limit of 25 applies to all repositories in the organization.

### 1.5 Projects (v2) with a single-select field

- A project holds up to 50,000 items. This limit includes the archive. At the limit, you must delete items. [S14] [S15]
- A project has up to 50 fields. A single-select field has up to 50 options. [S16] [S17]
- A field has one value for each project item. But an issue can be in many projects: `Issue.projectItems` is a connection. [S3] Thus the guarantee applies only inside one project that Mobius selects.
- Iteration fields hold repeated time blocks (`iteration:@current`). [S18] [S9] They fit cycle work, not areas with no end.
- GraphQL `updateProjectV2ItemFieldValue` sets single-select, multi-select, text, number, date, and iteration values. [S3] A REST API for projects, items, and field values exists since 2025-09-11. [S5]
- The `projects_v2_item` webhook is available only at the organization level. [S6] A project that a user owns sends no item webhooks.
- The owner sees the field only in the project view and in the "Projects" part of the issue sidebar.

### 1.6 Issue fields (organization custom fields on the issue)

- Issue fields went to general availability on 2026-07-02 for all organizations on Free, Team, and Enterprise plans. [S19]
- Types: single-select, text, number, date. [S20] Multi-select fields are in public preview since 2026-07-23. [S21] Thus a "Workstream" field must use the single-select type to keep one value.
- Limits: 25 fields for each organization, 100 options for each single-select field, 10 pinned fields for each issue type. [S20]
- The value is on the issue, not on a project item. The docs describe issue fields only at the organization level. [S20]
- Options are shared by all repositories in the organization. Thus 100 options must hold the Workstreams of all repositories.
- Search: `field.priority:high`. [S22] [S23] GraphQL: `IssueFilters.issueFieldValues` filters `Repository.issues`. `setIssueFieldValue` sets a value. [S3]
- REST: `GET|POST|PUT /issues/{n}/issue-field-values` and `DELETE /issues/{n}/issue-field-values/{field_id}`. [S24] The docs say "full REST and GraphQL API support". [S23]
- The `issues` webhook sends `field_added` and `field_removed`. [S6] The timeline has `IssueFieldAddedEvent`, `IssueFieldChangedEvent`, and `IssueFieldRemovedEvent`. [S3]
- The `IssueFieldCreateOrUpdateInput` has `suggest: "the value is stored as a pending suggestion for human review"`, and `rationale` and `confidence` inputs. [S3] An agent can propose a Workstream and the owner can accept it.
- Values show on the repository issues list and can be project columns. [S19] [S23]
- Live check: the Mobius-Toolkit organization (Free plan) already has the four default fields (Priority, Effort, Start date, Target date). [S3]

## 2. Older methods

| Method | Long-lived area | Finite container | One area for each item? |
|---|---|---|---|
| Scrum | Product Backlog: "an emergent, ordered list of what is needed to improve the product" [S25] | Sprint; Product Goal: "must fulfill (or abandon) one objective before taking on the next" [S25] | The guide does not name epics or themes. [S25] |
| SAFe | Agile Release Train: "a long-lived team of Agile teams" in a value stream [S26]; Strategic themes: "portfolio-level business objectives" [S27] | Epic: "a significant solution development initiative" with an MVP and a Lean business case [S28] | Not defined. |
| Jira | Component: groups work "around product features, departments, or workstreams" [S29] | Epic: "a large body of work", "delivered over a set of sprints" [S30] | Epic: yes, "You can only assign one parent to a work item." [S31] Component: no, a work item can have many components. The component lead that comes first in alphabetical order gets the item. [S32] |
| Kanban | Class of service: "a specific level of service ... established through a defined set of policies"; work item type [S33]. Board swimlanes by query, epic, assignee, or space. [S34] | None. Kanban has continuous flow. | A query swimlane board has a catch-all lane for items that match no query. [S34] |
| Shape Up | None. "Backlogs are a big weight we don't need to carry." Each department keeps its own list. [S35] | Six-week cycle, two-week cool-down, bets at the betting table. [S36] | Not defined. |

Lesson: older methods split the area (long-lived, no end) from the container (finite, with an end). Epics end. Components and trains stay. On GitHub, a parent issue acts like an epic: it has a size limit and a closed state. A field or a label acts like a component: it has no size limit and no end.

## 3. Comparison

| Model | Exactly one? | Hierarchy | Scale | Evergreen fit | API for deterministic route | Owner UI |
|---|---|---|---|---|---|---|
| Parent issue + sub-issues | Yes, by GitHub (one `parent`) | Yes, 8 levels | 100 children for each parent; closed children count | Poor for a flat parent. Good with cycle parents under a root. | Walk `parent` up to the root. One GraphQL query can nest `parent { parent { ... } }`. | Sidebar shows the parent. Parent shows a progress bar. Projects can group by parent. |
| Label `ws:<slug>` | No. Mobius must reject 0 or 2+ labels. | No | No documented limit | Good | `labels` filter, `labeled` webhook | Colored chip in all lists |
| Milestone | Yes, by GitHub | No | No documented limit | Possible (`due_on` optional), but it takes the release slot | `milestone` filter, `milestoned` webhook | Milestone page with % |
| Issue type | Yes, by GitHub | No | 25 types for each organization | Poor: wrong meaning | `type` filter, `typed` webhook | Type in all lists |
| Projects single-select field | Only inside one chosen project | Parent grouping in views | 50,000 items with archive; 50 options | Good until 50,000 items | GraphQL/REST item field values; webhooks only for organization projects | Only in project views and sidebar |
| Issue field (single-select) | Yes, by GitHub (single-select type) | No (combine with parent) | 100 options for each organization; no documented issue limit | Good | `issueFieldValues` filter, REST endpoints, `field_added` webhook | Sidebar and repository issues list |

## 4. Combinations

- **Root parent + cycle parents.** A root issue "Workstream: Keep test coverage" holds one child parent for each cycle ("2026-W39"). Tasks go under the cycle parent. Two levels give 100 x 100 = 10,000 tasks. Each extra level multiplies by 100. The route follows `parent` to the root. When a cycle parent is full, Mobius must open a new one. This matches the Shape Up cycle and the Scrum sprint. [S1] [S36]
- **Issue field + parent.** The issue field holds the Workstream. Parent issues hold feature groups inside it. The route reads only the field. The parent tree has no role in the route.
- **Label + parent.** The label holds the Workstream. Parents group tasks. Mobius must enforce one `ws:` label for each issue.

```
Root parent + cycle parents          Issue field + parent

#100 Workstream: Keep coverage       field Workstream = "keep-coverage"
 ├─ #210 Cycle 2026-W38 (100 max)      #512 task   (parent: #300, optional)
 │   ├─ #211 task                      #513 task   (no parent)
 │   └─ ...                          route = issueFieldValues["Workstream"]
 └─ #340 Cycle 2026-W39
     └─ #341 task
route = follow parent to root (#100)
```

## 5. Implications for Mobius

The owner decides in a later grilling ticket. These options are viable:

1. **Root parent issue with cycle parents.**
   - For: GitHub enforces one parent. It works in user-owned and organization repositories. The owner sees the tree in the issue UI. It needs no organization setup.
   - Against: Mobius must create and roll over cycle parents at 100 children. Closed children fill the slots. The route needs a walk up the tree. A parent change moves the issue to another Workstream. Mobius sees the move through the `sub_issues` webhook or a new read.
2. **Single-select issue field "Workstream".**
   - For: GitHub enforces one value on the issue itself. It has no size limit on issues. It has search, filter, webhooks, and REST and GraphQL APIs. The `suggest` input lets an agent propose a Workstream for owner review.
   - Against: It works only in organizations. 100 options are shared by all repositories in the organization. Multi-select is also a field type, so Mobius must check that the "Workstream" field is single-select. The feature went to general availability on 2026-07-02, so it is new.
3. **Label `ws:<slug>`, with optional parent issues for groups.**
   - For: It works in all repositories. It has no size limit. The owner sees it in all lists. The API and webhooks are simple.
   - Against: GitHub does not enforce one label. Mobius must detect 0 or 2+ `ws:` labels and send the issue to triage. Labels are per repository, so each repository needs its own `ws:` labels.

Not recommended: milestones (they take the release slot and have no hierarchy), issue types (wrong meaning, 25 for the whole organization), and a Projects field (the guarantee holds only in one chosen project, the 50,000-item limit includes the archive, and user projects send no webhooks).

## Sources

- [S1] https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues
- [S2] https://docs.github.com/en/rest/issues/sub-issues
- [S3] GitHub GraphQL schema, read with `gh api graphql` introspection on 2026-09-26 (`Issue`, `AddSubIssueInput`, `SubIssuesSummary`, `IssueFilters`, `IssueFieldCreateOrUpdateInput`, mutations). Reference: https://docs.github.com/en/graphql/reference/objects#issue
- [S4] https://github.com/orgs/community/discussions/193327 (user report, 2026-04-21)
- [S5] https://github.blog/changelog/2025-09-11-a-rest-api-for-github-projects-sub-issues-improvements-and-more/
- [S6] https://docs.github.com/en/webhooks/webhook-events-and-payloads
- [S7] https://github.blog/changelog/2026-06-10-manage-sub-issues-types-and-dependencies-from-github-cli/
- [S8] https://docs.github.com/en/issues/planning-and-tracking-with-projects/understanding-fields/about-parent-issue-and-sub-issue-progress-fields
- [S9] https://docs.github.com/en/issues/planning-and-tracking-with-projects/customizing-views-in-your-project/filtering-projects
- [S10] https://docs.github.com/en/issues/using-labels-and-milestones-to-track-work/managing-labels
- [S11] https://docs.github.com/en/issues/using-labels-and-milestones-to-track-work/about-milestones
- [S12] https://docs.github.com/en/rest/issues/milestones
- [S13] https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/managing-issue-types-in-an-organization
- [S14] https://docs.github.com/en/issues/planning-and-tracking-with-projects/managing-items-in-your-project/adding-items-to-your-project
- [S15] https://github.blog/changelog/2025-04-09-evolving-github-issues-and-projects/
- [S16] https://docs.github.com/en/issues/planning-and-tracking-with-projects/learning-about-projects/about-projects
- [S17] https://docs.github.com/en/issues/planning-and-tracking-with-projects/understanding-fields/about-single-select-fields
- [S18] https://docs.github.com/en/issues/planning-and-tracking-with-projects/understanding-fields/about-iteration-fields
- [S19] https://github.blog/changelog/2026-07-02-issue-fields-are-now-generally-available/
- [S20] https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/managing-issue-fields-in-your-organization
- [S21] https://github.blog/changelog/2026-07-23-multi-select-fields-for-projects-and-issues-in-public-preview/
- [S22] https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/filtering-and-searching-issues-and-pull-requests
- [S23] https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-and-managing-issue-fields
- [S24] https://docs.github.com/en/rest/issues/issue-field-values
- [S25] https://scrumguides.org/scrum-guide.html
- [S26] https://framework.scaledagile.com/agile-release-train
- [S27] https://framework.scaledagile.com/strategic-themes
- [S28] https://framework.scaledagile.com/epic
- [S29] https://support.atlassian.com/jira-software-cloud/docs/what-are-jira-components/
- [S30] https://support.atlassian.com/jira-software-cloud/docs/what-is-an-epic/
- [S31] https://support.atlassian.com/jira-software-cloud/docs/manage-epics-in-team-managed-projects/
- [S32] https://support.atlassian.com/jira/kb/manage-components-default-assignee-jira-software/
- [S33] https://kanban.university/glossary/
- [S34] https://support.atlassian.com/jira-software-cloud/docs/configure-swimlanes/
- [S35] https://basecamp.com/shapeup/2.1-chapter-07
- [S36] https://basecamp.com/shapeup/2.2-chapter-08
