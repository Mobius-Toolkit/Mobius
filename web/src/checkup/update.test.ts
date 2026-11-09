import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import {
  CreateWorkstream,
  FetchCheckup,
  FixLabels,
  GoBack,
  Navigate,
  OpenChat,
  ReportFixButton,
  RequestLogin,
  SetAutoUpgrade,
  UpgradeTool,
} from "./commands";
import { Message } from "./message";
import { fixButtonOf, initialModel, type Model } from "./model";
import { maintenanceWorkstreamRequest, skillsWorkstreamRequest } from "./requests";
import type { Checkup, ToolCheck } from "./schemas";
import { update } from "./update";

const tool = (overrides: Partial<ToolCheck> = {}): ToolCheck => ({
  autoUpgrade: false,
  harness: "dsh",
  name: "dsh",
  newest: "0.2.1-rc.5",
  outdated: true,
  package: "@deepseek-ai/dsh",
  path: "/usr/local/bin/dsh",
  required: true,
  status: "",
  version: "0.2.1-rc.4",
  ...overrides,
});

const checkup = (overrides: Partial<Checkup> = {}): Checkup => ({
  issueRepository: "plants/garden",
  issueRepositoryError: "",
  labelFix: "none",
  maintenanceWorkstream: 0,
  permissions: [],
  permissionsError: "",
  repositories: [],
  skillRepository: "plants/mobius-skills",
  skillsWorkstream: 0,
  skillsWorkstreamRepository: "",
  ...overrides,
});

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ page: "tools", organization: "plants" }),
  ...overrides,
});

const reportFixButton = StoryCommand.resolve(ReportFixButton, Message.CompletedReportFixButton());

const labelsModel = (overrides: Partial<Model> = {}) =>
  createModel({
    page: "labels",
    checkups: { plants: checkup({ labelFix: "fix" }) },
    ...overrides,
  });

describe("the organizations of the Checkup page", () => {
  it("reads the checkup of each new organization once", () => {
    story(
      update,
      given(createModel({ page: "overview", organization: "" })),
      message(Message.ReceivedOrganizations({ organizations: ["owner", "plants"] })),
      StoryCommand.expectExact(
        FetchCheckup({ organization: "owner" }),
        FetchCheckup({ organization: "plants" }),
      ),
      StoryCommand.resolve(
        FetchCheckup({ organization: "owner" }),
        Message.CheckupLoaded({ organization: "owner", checkup: checkup() }),
      ),
      StoryCommand.resolve(
        FetchCheckup({ organization: "plants" }),
        Message.CheckupLoaded({ organization: "plants", checkup: checkup() }),
      ),
      message(Message.ReceivedOrganizations({ organizations: ["owner", "plants", "trees"] })),
      StoryCommand.expectExact(FetchCheckup({ organization: "trees" })),
      StoryCommand.resolve(
        FetchCheckup({ organization: "trees" }),
        Message.CheckupFailed({ organization: "trees", error: "down" }),
      ),
      message(Message.ReceivedOrganizations({ organizations: ["owner", "plants", "trees"] })),
      StoryCommand.expectNone(),
      model((m) => {
        expect(Object.keys(m.checkups)).toEqual(["owner", "plants"]);
      }),
    );
  });

  it("reads nothing on a page of one organization", () => {
    story(
      update,
      given(createModel({ page: "labels" })),
      message(Message.ReceivedOrganizations({ organizations: ["owner", "plants"] })),
      StoryCommand.expectNone(),
    );
  });
});

describe("the tools", () => {
  it("shows the tools and the pauses", () => {
    story(
      update,
      given(createModel()),
      message(Message.ToolsLoaded({ tools: [tool()] })),
      message(
        Message.ReceivedPauses({
          pauses: [{ harness: "dsh", inUse: true, pausedUntil: "2026-09-29T08:30:00Z" }],
        }),
      ),
      model((m) => {
        expect(m.tools).toEqual(Option.some([tool()]));
        expect(m.pauses).toHaveLength(1);
      }),
    );
  });

  it("keeps the error of a failed read, and asks for the login on a 401", () => {
    story(
      update,
      given(createModel()),
      message(Message.ToolsFailed({ error: "The server failed." })),
      model((m) => {
        expect(m.toolsError).toBe("The server failed.");
        expect(Option.isNone(m.tools)).toBe(true);
      }),
      message(Message.LoginRequired()),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });

  it("upgrades a package, and shows the new list", () => {
    const upgraded = tool({ version: "0.2.1-rc.5", outdated: false });

    story(
      update,
      given(createModel({ tools: Option.some([tool()]) })),
      message(Message.OpenedTool({ name: "dsh" })),
      message(Message.ClickedUpgrade({ tool: tool() })),
      model((m) => {
        expect(m.upgrading).toBe(true);
      }),
      StoryCommand.expectExact(UpgradeTool({ tool: tool() })),
      StoryCommand.resolve(UpgradeTool, Message.ToolsUpgraded({ tools: [upgraded] })),
      model((m) => {
        expect(m.upgrading).toBe(false);
        expect(m.tools).toEqual(Option.some([upgraded]));
      }),
    );
  });

  it("shows the error of a failed upgrade, and clears it at the next try", () => {
    story(
      update,
      given(createModel({ tools: Option.some([tool()]) })),
      message(Message.OpenedTool({ name: "dsh" })),
      message(Message.ClickedUpgrade({ tool: tool() })),
      StoryCommand.resolve(UpgradeTool, Message.UpgradeFailed({ error: "npm ERR! code EACCES" })),
      model((m) => {
        expect(m.upgrading).toBe(false);
        expect(m.upgradeError).toBe("npm ERR! code EACCES");
        expect(m.tools).toEqual(Option.some([tool()]));
      }),
      message(Message.ClickedAutoUpgrade({ tool: tool(), on: true })),
      model((m) => {
        expect(m.upgradeError).toBe("");
        expect(m.togglingAuto).toBe(true);
      }),
      StoryCommand.expectExact(SetAutoUpgrade({ tool: tool(), on: true })),
      StoryCommand.resolve(
        SetAutoUpgrade,
        Message.ToolsUpgraded({ tools: [tool({ autoUpgrade: true })] }),
      ),
      model((m) => {
        expect(m.togglingAuto).toBe(false);
        expect(m.upgradeError).toBe("");
      }),
    );
  });

  it("forgets the state of the dialog when it closes", () => {
    story(
      update,
      given(
        createModel({
          openTool: Option.some("dsh"),
          upgrading: true,
          togglingAuto: true,
          upgradeError: "npm ERR!",
        }),
      ),
      message(Message.ClosedTool()),
      model((m) => {
        expect(Option.isNone(m.openTool)).toBe(true);
        expect(m.upgrading).toBe(false);
        expect(m.togglingAuto).toBe(false);
        expect(m.upgradeError).toBe("");
      }),
    );
  });

  it("shows no error of an answer that comes after the dialog closed", () => {
    story(
      update,
      given(createModel()),
      message(Message.UpgradeFailed({ error: "late" })),
      model((m) => {
        expect(m.upgradeError).toBe("");
      }),
    );
  });

  it("asks for the login when an upgrade gets a 401", () => {
    story(
      update,
      given(createModel({ tools: Option.some([tool()]) })),
      message(Message.ClickedUpgrade({ tool: tool() })),
      StoryCommand.resolve(UpgradeTool, Message.LoginRequired()),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});

describe("the labels", () => {
  it("shows no fix button until the checkup says that labels need a fix", () => {
    story(
      update,
      given(createModel({ page: "labels" })),
      message(Message.CheckupLoaded({ organization: "plants", checkup: checkup() })),
      StoryCommand.expectNone(),
      message(
        Message.CheckupLoaded({ organization: "plants", checkup: checkup({ labelFix: "create" }) }),
      ),
      StoryCommand.expectExact(
        ReportFixButton({ button: { text: "Create labels", pending: false } }),
      ),
      StoryCommand.resolve(ReportFixButton, Message.CompletedReportFixButton()),
    );
  });

  it("fixes the labels, shows the pending state, and reads the status again", () => {
    story(
      update,
      given(labelsModel()),
      message(Message.ClickedFix()),
      model((m) => {
        expect(m.fixing).toBe(true);
        expect(fixButtonOf(m)).toEqual({ text: "Fix labels", pending: true });
      }),
      StoryCommand.expectExact(
        FixLabels({ organization: "plants" }),
        ReportFixButton({ button: { text: "Fix labels", pending: true } }),
      ),
      reportFixButton,
      StoryCommand.resolve(FixLabels, Message.LabelsFixed()),
      StoryCommand.expectExact(
        FetchCheckup({ organization: "plants" }),
        ReportFixButton({ button: { text: "Fix labels", pending: false } }),
      ),
      reportFixButton,
      StoryCommand.resolve(
        FetchCheckup,
        Message.CheckupLoaded({ organization: "plants", checkup: checkup() }),
      ),
      StoryCommand.expectExact(ReportFixButton({ button: null })),
      reportFixButton,
    );
  });

  it("ignores a second click while the fix runs", () => {
    story(
      update,
      given(labelsModel({ fixing: true })),
      message(Message.ClickedFix()),
      StoryCommand.expectNone(),
    );
  });

  it("shows the error of a failed fix, and reads the status again because labels can change", () => {
    story(
      update,
      given(labelsModel({ fixing: true })),
      message(Message.LabelFixFailed({ error: "GitHub refused the label." })),
      model((m) => {
        expect(m.fixing).toBe(false);
        expect(m.checkupError).toBe("GitHub refused the label.");
      }),
      StoryCommand.expectExact(
        FetchCheckup({ organization: "plants" }),
        ReportFixButton({ button: { text: "Fix labels", pending: false } }),
      ),
      reportFixButton,
      StoryCommand.resolve(
        FetchCheckup,
        Message.CheckupFailed({ organization: "plants", error: "down" }),
      ),
      model((m) => {
        expect(m.checkupError).toBe("down");
      }),
    );
  });

  it("clears the error of an earlier fix when the next fix works", () => {
    story(
      update,
      given(labelsModel({ fixing: true, checkupError: "old" })),
      message(Message.LabelsFixed()),
      model((m) => {
        expect(m.checkupError).toBe("");
      }),
      reportFixButton,
      StoryCommand.resolve(
        FetchCheckup,
        Message.CheckupLoaded({ organization: "plants", checkup: checkup() }),
      ),
      StoryCommand.expectExact(ReportFixButton({ button: null })),
      reportFixButton,
    );
  });
});

const loaded = (overrides: Partial<Checkup> = {}) =>
  createModel({
    page: "workstreams",
    checkups: { plants: checkup(overrides) },
  });

describe("the Workstreams", () => {
  it("sends the request of the maintenance Workstream, and opens the chat", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedCreate({ kind: "maintenance" })),
      model((m) => {
        expect(m.creating).toEqual(Option.some("maintenance"));
      }),
      StoryCommand.expectExact(
        CreateWorkstream({
          organization: "plants",
          request: maintenanceWorkstreamRequest("plants/garden"),
        }),
      ),
      StoryCommand.resolve(
        CreateWorkstream,
        Message.WorkstreamRequested({ organization: "plants" }),
      ),
      StoryCommand.expectExact(OpenChat({ organization: "plants" })),
      StoryCommand.resolve(OpenChat, Message.CompletedOpenChat()),
      model((m) => {
        expect(m.creating).toEqual(Option.some("maintenance"));
      }),
    );
  });

  it("sends the request of the Skills Workstream", () => {
    story(
      update,
      given(loaded({ maintenanceWorkstream: 9 })),
      message(Message.ClickedCreate({ kind: "skills" })),
      StoryCommand.expectExact(
        CreateWorkstream({
          organization: "plants",
          request: skillsWorkstreamRequest("plants/garden", "plants/mobius-skills"),
        }),
      ),
      StoryCommand.resolve(
        CreateWorkstream,
        Message.WorkstreamRequested({ organization: "plants" }),
      ),
      StoryCommand.resolve(OpenChat, Message.CompletedOpenChat()),
    );
  });

  it("shows the error of a message that the device cannot store, and stops the pending state", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedCreate({ kind: "skills" })),
      StoryCommand.resolve(CreateWorkstream, Message.CreateFailed({ error: "QuotaExceededError" })),
      model((m) => {
        expect(m.createError).toBe("QuotaExceededError");
        expect(Option.isNone(m.creating)).toBe(true);
      }),
    );
  });

  it("sends nothing before the checkup is loaded", () => {
    story(
      update,
      given(createModel({ page: "workstreams" })),
      message(Message.ClickedCreate({ kind: "maintenance" })),
      StoryCommand.expectNone(),
    );
  });
});

describe("the host", () => {
  it("asks the host to open a page, and to go back", () => {
    story(
      update,
      given(createModel()),
      message(Message.NavigateTo({ path: "/settings/checkup/tools" })),
      StoryCommand.expectExact(Navigate({ path: "/settings/checkup/tools" })),
      StoryCommand.resolve(Navigate, Message.CompletedNavigate()),
      message(Message.ClickedBack()),
      StoryCommand.expectExact(GoBack()),
      StoryCommand.resolve(GoBack, Message.CompletedGoBack()),
    );
  });
});
