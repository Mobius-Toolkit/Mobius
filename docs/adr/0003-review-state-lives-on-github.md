# Review state lives on GitHub

Each finding on an agent PR is an inline comment in a GitHub review thread. The Implementer works on all unresolved threads, from any author, and resolves each thread that it fixes. Mobius shows its verdict as one check run named `Mobius` on the head commit. The check run is `success` only when all Mobius gates pass. So the Owner sees all review work on GitHub, and other reviewers (other bots, humans) plug in with no Mobius code.

## Considered Options

- **Findings as messages inside Mobius:** rejected. The Owner cannot see them on GitHub, and other reviewers cannot add findings.
- **A GitHub approval from the Reviewer:** rejected. A GitHub App cannot approve a pull request that the same App opened.
- **A commit status `mobius`:** rejected. It holds only 140 characters, so the log of a failed local check needs a separate place.

## Consequences

- An open finding is an unresolved review thread. Mobius keeps no second copy of review state.
- An agent replies to a thread only with a fix commit or with a reason to reject the finding. Each pull request has a maximum of N fix rounds, and a new comment from the Owner resets the count. This stops endless conversations between agents.
- A local check that fails 3 times stops the work. Mobius pushes the commits anyway and sets `Mobius` to `failure` with the log, so no pull request stays in an unknown state.
- The Owner can make `Mobius` a required check in branch protection.
- The Mobius App needs the `checks: write` permission.
