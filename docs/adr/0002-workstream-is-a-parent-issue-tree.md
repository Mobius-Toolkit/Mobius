# A Workstream is a parent issue tree on GitHub

Each Workstream is one GitHub issue with the label `mobius:workstream`. Its body is the Brief. A `mobius:ready` issue belongs to the first Workstream issue in its parent chain, and Mobius reads that chain one time, at dispatch. GitHub gives each issue only one parent, so each issue has at most one Workstream, and the Owner gets a hierarchical task list with no extra tool.

## Considered Options

- **A label `ws:<slug>` on each issue:** rejected. GitHub does not limit an issue to one such label, and sub-issues do not inherit labels, so the Owner must label each task by hand.
- **A single-select issue field "Workstream":** rejected. Issue fields exist only in organizations, and one Owner often works in personal repositories.
- **A milestone or a Projects field:** rejected. A milestone takes the release slot and has no hierarchy. A Projects field gives one value only inside one project.

## Consequences

- A parent holds a maximum of 100 sub-issues, and closed sub-issues count. The Owner and the Lead put groups (a map, a spec, a month) below the Workstream issue. Mobius code does not restructure the tree.
- A change of parent moves an issue to another Workstream. A task that runs already stays with its Lead until it ends.
- An issue with no Workstream issue in its parent chain goes to the Triager, not to a default Workstream.
- Mobius reads `Issue.parent` directly. The `parent-issue:` search qualifier returned no results in the API.
