import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { FetchHarnesses } from "@/setting/commands";
import * as SettingFixture from "@/setting/fixtures";
import type { Role } from "@/setting/schemas";
import type { Agent } from "@/transcript/schemas";
import * as Selection from "@/ui/selection";
import * as SelectionFixture from "@/ui/selection-fixtures";
import * as AgentsFixture from "./agents/fixtures";
import { FetchAgents } from "./agents/commands";
import { AddSelection, RequestLogin, ReportTab } from "./commands";
import { FetchDetails, FetchMcpServers, FetchRoles } from "./details/commands";
import type { WorkstreamDetails } from "./details/model";
import * as DetailsFixture from "./details/fixtures";
import { Message } from "./message";
import { initialModel, type Model } from "./model";
import { FetchTasks as FetchRecurring } from "./recurring/commands";
import * as RecurringFixture from "./recurring/fixtures";
import type { Tab } from "./schemas";
import { FetchFallback, FetchTasks } from "./tasks/commands";
import type { TaskLine } from "./tasks/model";
import * as TasksFixture from "./tasks/fixtures";
import { update } from "./update";

const workstream = { owner: "owner", name: "shop", number: 12 };

const createModel = (tab: Tab, overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ ...workstream, instance: "side", tab, opening: Option.none() }),
  ...overrides,
});

const agent = (overrides: Partial<Agent> = {}): Agent => ({
  effort: "high",
  endReason: "",
  endedAt: null,
  harness: "devin",
  id: 7,
  issue: 39,
  keptUntil: null,
  issueTitle: null,
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
  StoryCommand.resolve(FetchMcpServers, DetailsFixture.mcpServersLoaded),
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
      StoryCommand.expectHas(FetchMcpServers),
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

describe("the selection in the Brief tab", () => {
  it("puts a selection of the Brief in the draft of the Lead chat of the Workstream", () => {
    story(
      update,
      given(createModel("brief")),
      message(Message.GotSelectionMessage({ message: SelectionFixture.selected })),
      message(Message.GotSelectionMessage({ message: SelectionFixture.addClicked })),
      model((m) => {
        expect(m.selection).toBeNull();
      }),
      StoryCommand.expectExact(
        AddSelection({
          key: "chatDraft:owner/owner/shop/12",
          selection: {
            source: "brief",
            text: "free plan",
            context: "One free plan for staff",
            reference: "#12",
            link: "https://github.com/owner/shop/issues/12",
            part: "Scope",
            messageId: 0,
            messageAuthor: "",
          },
        }),
        Selection.ClearSelection(),
      ),
      StoryCommand.resolve(Selection.ClearSelection, SelectionFixture.clearedSelection),
      StoryCommand.resolve(AddSelection, Message.AddedSelection()),
    );
  });

  it("drops the selection when the Owner opens another tab", () => {
    story(
      update,
      given(
        createModel("brief", {
          selection: {
            owner: "brief",
            text: "x",
            context: "x",
            part: "",
            rect: SelectionFixture.rect,
          },
        }),
      ),
      message(Message.SelectedTab({ tab: "tasks" })),
      model((m) => {
        expect(m.selection).toBeNull();
      }),
      StoryCommand.expectHas(ReportTab),
      ...resolveTasks,
      StoryCommand.resolve(ReportTab, Message.CompletedReportTab()),
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

const resolveHarnesses = StoryCommand.resolve(FetchHarnesses, SettingFixture.harnessesLoaded);

const loadedDetails: WorkstreamDetails = {
  completedTasks: 0,
  createdAt: "2026-05-01T10:00:00Z",
  evergreen: false,
  implementer: { harness: "devin", model: "swe-1.5", effort: "high", own: false },
  lead: { harness: "claude-code", model: "opus", effort: "medium", own: false },
  maintenance: false,
  mcpServers: [],
  openTasks: 1,
  operator: { harness: "claude-code", model: "opus", effort: "medium", own: false },
  skills: false,
};

const implementerRole: Role = {
  context: 0,
  effort: "low",
  harness: "devin",
  max: 1,
  model: "swe-1.5",
  moreBindings: [],
  role: "implementer",
  ui: { context: false, effort: true, harness: true, max: false, model: true },
};

const taskLine = (number: number): TaskLine => ({
  blockedBy: [],
  depth: 0,
  implementer: null,
  number,
  otherRepository: false,
  repository: "owner/shop",
  state: "open",
  title: `Task ${number}`,
  url: `https://github.com/owner/shop/issues/${number}`,
});

const defaultSetting = { harness: "devin", model: "swe-1.5", effort: "high", own: false };

const asking = (tab: Tab, task: Option.Option<{ repository: string; number: number }>) =>
  createModel(tab, { opening: Option.some({ setting: "implementer", task }) });

describe("the setting sheet that the address asks for", () => {
  it("opens the setting sheet of the Workstream when the details and the Roles are there", () => {
    story(
      update,
      given(asking("details", Option.none())),
      message(Message.GotDetailsMessage({ message: DetailsFixture.detailsLoaded(loadedDetails) })),
      model((m) => {
        expect(Option.isNone(m.details.sheet)).toBe(true);
        expect(Option.isSome(m.opening)).toBe(true);
      }),
      message(Message.GotDetailsMessage({ message: DetailsFixture.rolesGiven([implementerRole]) })),
      model((m) => {
        const sheet = Option.getOrThrow(m.details.sheet);
        expect(sheet.session).toBe("implementer");
        expect(sheet.setting.target).toEqual({ _tag: "WorkstreamImplementer", ...workstream });
        expect(Option.isNone(m.opening)).toBe(true);
      }),
      StoryCommand.expectHas(FetchHarnesses),
      resolveHarnesses,
    );
  });

  it("opens the setting sheet of the task when the tasks and the default setting are there", () => {
    story(
      update,
      given(asking("tasks", Option.some({ repository: "owner/shop", number: 39 }))),
      message(Message.GotTasksMessage({ message: TasksFixture.linesLoaded([taskLine(39)]) })),
      model((m) => {
        expect(Option.isNone(m.tasks.sheet)).toBe(true);
      }),
      message(Message.GotTasksMessage({ message: TasksFixture.fallbackLoaded(defaultSetting) })),
      model((m) => {
        const sheet = Option.getOrThrow(m.tasks.sheet);
        expect(sheet.target).toEqual({ _tag: "Task", owner: "owner", name: "shop", number: 39 });
        expect(Option.isNone(m.opening)).toBe(true);
      }),
      StoryCommand.expectHas(FetchHarnesses),
      resolveHarnesses,
    );
  });

  it("opens no sheet for a task that is not in the list", () => {
    story(
      update,
      given(asking("tasks", Option.some({ repository: "owner/shop", number: 39 }))),
      message(Message.GotTasksMessage({ message: TasksFixture.linesLoaded([taskLine(40)]) })),
      message(Message.GotTasksMessage({ message: TasksFixture.fallbackLoaded(defaultSetting) })),
      model((m) => {
        expect(Option.isNone(m.tasks.sheet)).toBe(true);
        expect(Option.isNone(m.opening)).toBe(true);
      }),
      StoryCommand.expectNone(),
    );
  });

  it("drops the request when the Owner opens another tab", () => {
    story(
      update,
      given(asking("details", Option.none())),
      message(Message.SelectedTab({ tab: "agents" })),
      model((m) => {
        expect(Option.isNone(m.opening)).toBe(true);
      }),
      StoryCommand.expectHas(FetchAgents),
      StoryCommand.expectHas(ReportTab),
      resolveAgents,
      StoryCommand.resolve(ReportTab, Message.CompletedReportTab()),
    );
  });
});
