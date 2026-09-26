# One Harness process for each agent session

Mobius starts one Harness process for each agent session and stops that process when the session ends. Each Harness can host many sessions in one process, but Mobius does not use this. Mobius sets the working directory, the environment, and the lifetime of each process. All starts go through one function that makes the command, so a later sandbox or container around a Worker changes only that function.

## Considered Options

- **One shared process for each Harness:** rejected. One crash stops all sessions of that Harness. Antigravity ignores `session/close`, so its sessions never get free until the process ends. Antigravity file tools reach only the `cwd` of the session, and the environment of a process applies to all its sessions.

## Consequences

- A kill of the process ends exactly one session. The Housekeeper restarts one dead agent and touches no other session.
- Each session costs one process, and the adapters of Claude Code and Antigravity also start one child process for each session. The `max_workers_total` limit keeps the memory use of the host low.
- Lead, Judge, and Triager sessions also get their own process. A Lead session holds its process only while it lives.
