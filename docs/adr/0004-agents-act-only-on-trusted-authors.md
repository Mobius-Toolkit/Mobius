# Agents act only on trusted authors

Mobius acts only on GitHub input from a trusted author: a login in the config list `trusted_users`, a bot in the config list `trusted_bots`, or the Mobius App. On a public repository, anyone can open issues and post comments and review threads. Each text that goes into an agent session can change what the agent does, so Mobius drops all other input before it reaches an agent.

## Considered Options

- **Act on all authors:** rejected. A stranger can then start agent work on the Owner's Harness accounts and inject instructions.
- **Only the Owner:** rejected. A small team shares one repository and one Mobius server, and each member must be able to label and comment.
- **User accounts in Mobius:** rejected. Trusted users act through GitHub, and all people who open the UI share the one UI password. Mobius has no teams and no memberships.

## Consequences

- Mobius dispatches a `mobius:ready` issue only when the `labeled` event has a trusted actor. This costs one read of the issue events for each new label.
- A trusted user who puts `mobius:ready` on an issue from another author accepts its body as the task.
- Comments, reviews, and threads from other authors start nothing, and no agent sees them. The read tools of agents apply the same filter.
- The Owner adds each bot, for example `coderabbitai[bot]`, to `trusted_bots` by hand.
