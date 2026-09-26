# Mobius

Mobius is a self-hosted agent factory for one owner. It drives the owner's subscription-based coding agents against GitHub work, and it gives the owner a remote chat to the agents.

## Language

### Install

**Owner**:
The one person who installs a Mobius server and whose subscriptions and GitHub access the agents use.
_Avoid_: User, tenant, member

**Mobius server**:
The installed Mobius process that holds the web UI, the API, and the agent runtime.
_Avoid_: Orchestrator, daemon, proxy

**Harness**:
An external coding-agent program (for example Claude Code, Codex, Devin) that Mobius controls through the Agent Client Protocol. The Owner installs and logs in to each Harness.
_Avoid_: Provider, backend, model

**Mobius App**:
The GitHub App of one Mobius server, and the only GitHub identity under which agents act.
_Avoid_: Bot account, machine user

### Agents

**Role**:
A kind of job that an agent does: Lead, Triager, Implementer, Researcher, Reviewer.
_Avoid_: Agent type, persona

**Role binding**:
The Owner's choice of Harness and model for one Role.
_Avoid_: Agent config, profile

**Lead**:
The top-level agent of one Workstream, and the only agent that the Owner talks to about the work of that Workstream.
_Avoid_: Orchestrator, manager, coordinator

**Triager**:
The top-level agent that helps the Owner compose a new Workstream and its Brief, and that proposes a Workstream for an issue with no Workstream.
_Avoid_: Dispatcher, router, concierge

**Worker**:
An agent that a Lead starts to do one piece of work; the Owner never talks to it directly.
_Avoid_: Sub-agent, child agent

**Implementer**:
A Worker Role that changes code and opens a pull request for one task.
_Avoid_: Coder, developer

**Researcher**:
A Worker Role that finds facts and reports them to its Lead without code changes.

**Reviewer**:
A Worker Role that checks a pull request against its task for correctness, missing requirements, and unwanted side effects.
_Avoid_: Checker, QA

**Housekeeper**:
The part of Mobius that watches all agents, restarts dead ones, and removes stale workspaces.
_Avoid_: Supervisor, janitor

### Work

**Workstream**:
A long-lived scope of work (for example "Integrate loyalty plans") that owns a task list, a memory, and one Lead.
_Avoid_: Project, epic, context

**Workstream issue**:
The GitHub issue that stands for one Workstream. Each issue below it in the parent tree belongs to that Workstream.
_Avoid_: Root issue, epic

**Brief**:
The Owner's description of the goal and the limits of one Workstream.
_Avoid_: Description, charter, spec

**Local check**:
The command of a repository that Mobius runs in a worktree before each push of agent work.
_Avoid_: CI, pre-push hook

**Fix round**:
One pass in which the Implementer changes a pull request to answer its unresolved review threads.
_Avoid_: Iteration, cycle
