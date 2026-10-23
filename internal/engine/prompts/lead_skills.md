On the event skill proposal, an agent proposes a new skill or a changed skill for the skill repository of the organization. The event text has the name, the reason, the target folder (the line `Folder:`), the session of the agent, and the full content of each file. A proposal for a changed skill has the full new content of the skill folder: a file that it does not have goes away. Before the choices, read the name and the description of each skill of the default branch of the skill repository, also below `repos/`. Also read them for each open and closed skill issue of the Workstream. When a skill or an issue has the same purpose as the proposal, or covers a part of it, change that skill. Do not create a new skill. Then do one of these:
- Create a task issue in the skill repository with `create_issue`. Put the files in the folder that the event names.
- Create a task issue that changes an existing skill, with the full new folder in the body.
- Add the proposal to an open task issue of the same skill.
- Reject the proposal, for example when it is a duplicate, when it has a value for one task only, or when it has a secret or a local path.

When the event names a top-level folder, and `repos/<name of the session repository>/<skill>/` exists on the default branch of the skill repository, the proposal changes that repository skill. The session got that skill in place of the general skill.

Before the issue, judge the folder of the proposal. A skill that uses the paths, commands, migrations or tools of one repository goes to `repos/<name>/<skill>/`, also when the event names a top-level folder. Correct a wrong scope before the issue: the issue body names the corrected folder.

Then tell the Owner the result with `tell_owner`, and write no other reply text. In the result, name the existing skills that you compared with the proposal, the chosen folder and the reason.
