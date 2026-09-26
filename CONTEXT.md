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

### Agents

**Role**:
A kind of job that an agent does: Lead, Implementer, Researcher, Reviewer.
_Avoid_: Agent type, persona

**Role binding**:
The Owner's choice of Harness and model for one Role.
_Avoid_: Agent config, profile

**Lead**:
The top-level agent of one Workstream, and the only agent that the Owner talks to.
_Avoid_: Orchestrator, manager, coordinator

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
