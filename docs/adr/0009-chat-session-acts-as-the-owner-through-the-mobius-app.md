# The Lead session acts as the Owner through the Mobius App

The Lead session gets `gh`, so the Owner's skills that use `gh` work in the chat. `gh` in the Lead session uses a user access token of the Mobius App. The author of each action is the Owner, and GitHub shows "<owner> via <app-slug>". The Owner tells the Lead what to do, so the Owner is responsible for its actions. All other sessions act only through the Mobius MCP server as the App (ADR 0006).

## Considered Options

- **The Owner's own `gh` login:** rejected. GitHub puts no mark on its actions. It reaches all repositories and organizations of the Owner. Mobius cannot tell a comment of the Lead session from a comment that the Owner wrote.
- **`gh` with the App installation token:** rejected. The work shows as the bot, and skill steps that need a user, for example `@me`, fail.
- **No `gh` in the Lead session:** rejected. The Role prompt must map each `gh` step of a skill to a Mobius tool, and many steps have no Mobius tool.

## Consequences

- The Owner authorizes the App one time, in the install step of the App. Mobius keeps the user token and the refresh token in `mobius.db`, and it refuses a login that is not a trusted user.
- A `gh` wrapper gets a fresh token from Mobius for each call, because a user token expires after 8 hours.
- The token reaches only the repositories where the App is installed, with the permissions that both the Owner and the App have. It can still merge pull requests and push, so the docs tell the Owner to protect the default branch.
- Mobius treats each action of the Lead session as an Owner action. Mobius does not deliver a comment of the Lead session to the Lead as an event, and it finds such a comment from `performed_via_github_app`.
- `gh` reads skip the trusted-author filter of ADR 0004. The Owner reads each turn of the chat.
- The Lead session has `tell_owner`. In a turn for an Owner message, the reply text of the Lead goes to the chat. In a turn for an event, the reply text goes only to the Transcript.
