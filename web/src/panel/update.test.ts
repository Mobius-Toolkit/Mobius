import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import type { Agent } from "@/transcript/schemas";
import * as AgentsFixture from "./agents/fixtures";
import { FetchAgents } from "./agents/commands";
import { RequestLogin, ReportTab, ReportTaskAttached } from "./commands";
import { FetchDetails, FetchRoles } from "./details/commands";
import * as DetailsFixture from "./details/fixtures";
import { Message } from "./message";
import { initialModel, type Model } from "./model";
import { FetchTasks as FetchRecurring } from "./recurring/commands";
import * as RecurringFixture from "./recurring/fixtures";
import type { Tab } from "./schemas";
import { FetchFallback, FetchTasks } from "./tasks/commands";
import * as TasksFixture from "./tasks/fixtures";
import { update } from "./update";

const workstream = { owner: "owner", name: "shop", number: 12 };

const createModel = (tab: Tab, overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ ...workstream, instance: "side", tab }),
  ...overrides,
});

const agent = (overrides: Partial<Agent> = {}): Agent => ({
  effort: "high",
  endReason: "",
  endedAt: null,
  harness: "devin",
  id: 7,
  issue: 39,
  model: "swe-1.5",
  name: "Implementer",
  organization: "owner",
  parent: null,
  pausedUntil: null,
  queueReason: "",
  repository: "owner/shop",
  role: "implementer",
  startedAt: "2026-05-01T10:00:00Z",
  title: "Task 39",
  working: true,
  workstream: 12,
  workstreamRepository: "owner/shop",
  ...overrides,
});

const resolveTasks = [
  StoryCommand.resolve(FetchTasks, TasksFixture.loaded),
  StoryCommand.resolve(FetchFallback, TasksFixture.failed),
] as const;

const resolveAgents = StoryCommand.resolve(FetchAgents, AgentsFixture.loaded);

const resolveDetails = [
  StoryCommand.resolve(FetchDetails, DetailsFixture.failed),
  StoryCommand.resolve(FetchRoles, DetailsFixture.rolesLoaded),
] as const;

const resolveRecurring = StoryCommand.resolve(FetchRecurring, RecurringFixture.loaded);

describe("the tabs of the panel", () => {
  it("reads the data of a tab when the Owner opens it, and tells the host the tab", () => {
    story(
      update,
      given(createModel("brief")),
      message(Message.SelectedTab({ tab: "tasks" })),
      model((m) => {
        expect(m.tab).toBe("tasks");
      }),
      StoryCommand.expectHas(FetchTasks),
      StoryCommand.expectHas(FetchFallback),
      StoryCommand.expectHas(ReportTab),
      ...resolveTasks,
      StoryCommand.resolve(ReportTab, Message.CompletedReportTab()),
    );
  });

  it("reads the agents when the Owner opens the Agents tab", () => {
    story(
      update,
      given(createModel("tasks")),
      message(Message.SelectedTab({ tab: "agents" })),
      StoryCommand.expectHas(FetchAgents),
      StoryCommand.expectHas(ReportTab),
      resolveAgents,
      StoryCommand.resolve(ReportTab, Message.CompletedReportTab()),
    );
  });

  it("reads the details and the Roles when the Owner opens the Details tab", () => {
    story(
      update,
      given(createModel("tasks")),
      message(Message.SelectedTab({ tab: "details" })),
      StoryCommand.expectHas(FetchDetails),
      StoryCommand.expectHas(FetchRoles),
      ...resolveDetails,
      StoryCommand.resolve(ReportTab, Message.CompletedReportTab()),
    );
  });

  it("reads the recurring tasks when the Owner opens the Recurring tab", () => {
    story(
      update,
      given(createModel("tasks")),
      message(Message.SelectedTab({ tab: "recurring" })),
      model((m) => {
        expect(m.tab).toBe("recurring");
      }),
      StoryCommand.expectHas(FetchRecurring),
      StoryCommand.expectHas(ReportTab),
      resolveRecurring,
      StoryCommand.resolve(ReportTab, Message.CompletedReportTab()),
    );
  });

  it("reads nothing for the Brief tab", () => {
    story(
      update,
      given(createModel("tasks")),
      message(Message.SelectedTab({ tab: "brief" })),
      model((m) => {
        expect(m.tab).toBe("brief");
      }),
      StoryCommand.expectHas(ReportTab),
      StoryCommand.resolve(ReportTab, Message.CompletedReportTab()),
    );
  });

  it("starts a tab with no data from the earlier visit", () => {
    const stale = createModel("details");
    const withAgent = {
      ...stale,
      agents: { ...stale.agents, agents: [agent()], showStopped: true },
    };

    story(
      update,
      given(withAgent),
      message(Message.SelectedTab({ tab: "agents" })),
      model((m) => {
        expect(m.agents.agents).toEqual([]);
        expect(m.agents.showStopped).toBe(false);
      }),
      resolveAgents,
      StoryCommand.resolve(ReportTab, Message.CompletedReportTab()),
    );
  });

  it("does not read or report again when the host sends the tab that is open", () => {
    story(
      update,
      given(createModel("agents")),
      message(Message.ReceivedTab({ tab: "agents" })),
      StoryCommand.expectNone(),
    );
  });

  it("opens the tab that the host sends, with its data, and does not report it back", () => {
    story(
      update,
      given(createModel("agents")),
      message(Message.ReceivedTab({ tab: "tasks" })),
      model((m) => {
        expect(m.tab).toBe("tasks");
      }),
      StoryCommand.expectHas(FetchTasks),
      ...resolveTasks,
    );
  });

  it("keeps the Brief that the store gives", () => {
    story(
      update,
      given(createModel("brief")),
      message(Message.ReceivedBrief({ brief: "Build the shop." })),
      model((m) => {
        expect(m.brief).toBe("Build the shop.");
      }),
    );
  });
});

describe("the reads after a live change", () => {
  it("reads the Tasks tab again after the connection opens and after a change of the Workstreams", () => {
    story(
      update,
      given(createModel("tasks")),
      message(Message.ConnectionOpened()),
      StoryCommand.expectHas(FetchTasks),
      ...resolveTasks,
      message(Message.WorkstreamsChanged()),
      StoryCommand.expectHas(FetchTasks),
      ...resolveTasks,
    );
  });

  it("reads the Details tab again after a change of the Workstreams", () => {
    story(
      update,
      given(createModel("details")),
      message(Message.WorkstreamsChanged()),
      StoryCommand.expectHas(FetchDetails),
      ...resolveDetails,
    );
  });

  it("does not read the Agents tab again after a change of the Workstreams", () => {
    story(
      update,
      given(createModel("agents")),
      message(Message.WorkstreamsChanged()),
      StoryCommand.expectNone(),
    );
  });

  it("reads the Agents tab again after a change of an agent of this Workstream", () => {
    story(
      update,
      given(createModel("agents")),
      message(Message.AgentChanged({ agent: agent() })),
      StoryCommand.expectHas(FetchAgents),
      resolveAgents,
    );
  });

  it("ignores an agent of another Workstream or of another repository", () => {
    story(
      update,
      given(createModel("agents")),
      message(Message.AgentChanged({ agent: agent({ workstream: 13 }) })),
      StoryCommand.expectNone(),
      message(Message.AgentChanged({ agent: agent({ workstreamRepository: "plants/orchard" }) })),
      StoryCommand.expectNone(),
    );
  });

  it("reads the Recurring tab again after the connection opens and after a change of its Workstream", () => {
    story(
      update,
      given(createModel("recurring")),
      message(Message.ConnectionOpened()),
      StoryCommand.expectHas(FetchRecurring),
      resolveRecurring,
      message(Message.RecurringChanged({ repository: "owner/shop", number: 12 })),
      StoryCommand.expectHas(FetchRecurring),
      resolveRecurring,
    );
  });

  it("ignores a change of the recurring tasks of another Workstream or of another repository", () => {
    story(
      update,
      given(createModel("recurring")),
      message(Message.RecurringChanged({ repository: "owner/shop", number: 13 })),
      StoryCommand.expectNone(),
      message(Message.RecurringChanged({ repository: "plants/shop", number: 12 })),
      StoryCommand.expectNone(),
    );
  });

  it("does not read the Recurring tab again after a change of the Workstreams", () => {
    story(
      update,
      given(createModel("recurring")),
      message(Message.WorkstreamsChanged()),
      StoryCommand.expectNone(),
    );
  });

  it("does not read the Tasks tab again after a change of the recurring tasks", () => {
    story(
      update,
      given(createModel("tasks")),
      message(Message.RecurringChanged({ repository: "owner/shop", number: 12 })),
      StoryCommand.expectNone(),
    );
  });

  it("does not read the Brief tab again", () => {
    story(
      update,
      given(createModel("brief")),
      message(Message.ConnectionOpened()),
      StoryCommand.expectNone(),
      message(Message.WorkstreamsChanged()),
      StoryCommand.expectNone(),
    );
  });
});

describe("an attached task", () => {
  it("tells the host after the task reaches the draft", () => {
    story(
      update,
      given(createModel("tasks")),
      message(Message.GotTasksMessage({ message: TasksFixture.attached })),
      StoryCommand.expectExact(ReportTaskAttached()),
      StoryCommand.resolve(ReportTaskAttached, Message.CompletedReportTaskAttached()),
    );
  });
});

describe("the login", () => {
  it("asks the host for the login on a 401 of the panel or of a tab", () => {
    story(
      update,
      given(createModel("tasks")),
      message(Message.LoginRequired()),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
      message(Message.GotTasksMessage({ message: TasksFixture.loginRequired })),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
      message(Message.GotDetailsMessage({ message: DetailsFixture.loginRequired })),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
      message(Message.GotRecurringMessage({ message: RecurringFixture.loginRequired })),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});
