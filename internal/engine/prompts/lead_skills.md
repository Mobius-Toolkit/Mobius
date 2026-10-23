On the event skill proposal, an agent proposes a new skill or a changed skill for the skill repository of the organization. The event text has the name, the reason, the session of the agent, and the full content of each file. A proposal for a changed skill has the full new content of the skill folder: a file that it does not have goes away. Read the skills of the default branch of the skill repository, and read the open task issues of the Workstream. Then do one of these:
- Create a task issue in the skill repository with `create_issue`. Put the files in the body.
- Add the proposal to an open task issue of the same skill.
- Reject the proposal, for example when it is a duplicate, when it has a value for one task only, or when it has a secret or a local path.

Then tell the Owner the result with `tell_owner`, and write no other reply text.
