# Agents run only on the Owner's own Harness subscriptions

Mobius is self-hosted, and one install serves one Owner. Every agent runs on a Harness subscription that the Owner holds (for example Claude Max or a ChatGPT plan), because a subscription gives much more usage than API prices for the same money. Mobius has no API key mode, no hosted service, and no user accounts.

## Considered Options

- **API keys for each Harness:** rejected. API prices remove the cost advantage, which is the reason Mobius exists.
- **A hosted service:** rejected. Harness terms forbid the resale of access that runs on a consumer subscription.

## Consequences

- Mobius starts the official, unmodified Harness program. The Owner logs in through the Harness's own login flow. Mobius never reads, stores, or forwards a subscription token.
- Mobius does not work around the rate limits of a subscription.
- If a vendor stops subscription use by a program like Mobius, that Harness stops working in Mobius. Mobius does not fall back to an API key.
- Mobius does not check how a Harness authenticates. If the Owner logs a Harness in with an API key, Mobius runs it the same way.
- The Owner is responsible for the terms of each Harness account, also when trusted users start agent work on it.
