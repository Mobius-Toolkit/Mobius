import { Option } from "effect";
import { describe, expect, it } from "vitest";
import {
  Command as StoryCommand,
  expectNoOutMessage,
  expectOutMessage,
  given,
  message,
  model,
  story,
} from "foldkit/story";
import { FetchHarnesses } from "@/setting/commands";
import * as SettingFixture from "@/setting/fixtures";
import type { Role } from "@/setting/schemas";
import { CloseWorkstream, FetchClosure, SetEvergreen } from "./commands";
import { Message, OutMessage } from "./message";
import { initialModel, type Model, type WorkstreamDetails } from "./model";
import { update } from "./update";

const workstream = { owner: "owner", name: "shop", number: 12 };

const details: WorkstreamDetails = {
  completedTasks: 3,
  createdAt: "2026-05-01T10:00:00Z",
  evergreen: false,
  implementer: { harness: "devin", model: "swe-1.5", effort: "high", own: false },
  lead: { harness: "claude-code", model: "opus", effort: "medium", own: true },
  maintenance: false,
  mcpServers: [],
  openTasks: 2,
  operator: { harness: "claude-code", model: "opus", effort: "medium", own: false },
  skills: false,
};

const role = (name: string, harness: string, modelName: string, effort: string): Role => ({
  context: 0,
  effort,
  harness,
  max: 1,
  model: modelName,
  role: name,
  ui: { context: false, effort: true, harness: true, max: false, model: true },
});

const roles = [
  role("lead", "claude-code", "sonnet", "low"),
  role("implementer", "devin", "swe-1.5", "low"),
  role("operator", "claude-code", "haiku", "medium"),
];

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ ...workstream, instance: "side" }),
  details: Option.some(details),
  roles: Option.some(roles),
  ...overrides,
});

const resolveHarnesses = StoryCommand.resolve(FetchHarnesses, SettingFixture.harnessesLoaded);

describe("the Details tab", () => {
  it("shows the details, the Roles and a failed read", () => {
    story(
      update,
      given(createModel({ details: Option.none(), roles: Option.none() })),
      message(Message.DetailsLoaded({ details })),
      message(Message.RolesLoaded({ roles })),
      model((m) => {
        expect(m.details).toEqual(Option.some(details));
        expect(m.roles).toEqual(Option.some(roles));
      }),
      message(Message.LoadFailed({ error: "Workstream is not open" })),
      model((m) => {
        expect(m.error).toBe("Workstream is not open");
      }),
    );
  });

  it("asks the host for the login on a 401", () => {
    story(
      update,
      given(createModel({ evergreenBusy: true })),
      message(Message.LoginRequired()),
      model((m) => {
        expect(m.evergreenBusy).toBe(false);
      }),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });
});

describe("the Evergreen switch", () => {
  it("sends the new value, waits, and shows the new value after the 204", () => {
    story(
      update,
      given(createModel({ error: "old" })),
      message(Message.SwitchedEvergreen({ on: true })),
      model((m) => {
        expect(m.evergreenBusy).toBe(true);
        expect(m.error).toBe("");
      }),
      StoryCommand.expectExact(SetEvergreen({ ...workstream, on: true })),
      StoryCommand.resolve(SetEvergreen, Message.EvergreenChanged({ on: true })),
      model((m) => {
        expect(m.evergreenBusy).toBe(false);
        expect(Option.getOrThrow(m.details).evergreen).toBe(true);
      }),
    );
  });

  it("keeps the old value and shows the reason when the request fails", () => {
    story(
      update,
      given(createModel()),
      message(Message.SwitchedEvergreen({ on: true })),
      StoryCommand.resolve(
        SetEvergreen,
        Message.EvergreenFailed({ error: "The Workstream is the Maintenance Workstream." }),
      ),
      model((m) => {
        expect(m.evergreenBusy).toBe(false);
        expect(m.error).toBe("The Workstream is the Maintenance Workstream.");
        expect(Option.getOrThrow(m.details).evergreen).toBe(false);
      }),
    );
  });

  it("asks the host for the login when the request gets a 401", () => {
    story(
      update,
      given(createModel()),
      message(Message.SwitchedEvergreen({ on: true })),
      StoryCommand.resolve(SetEvergreen, Message.LoginRequired()),
      model((m) => {
        expect(m.evergreenBusy).toBe(false);
      }),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });
});

describe("the setting of the Lead, of the Implementer and of the Operator", () => {
  it("opens the sheet for the Lead with its own setting, and the binding of the Role as default", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedChange({ session: "lead" })),
      model((m) => {
        const sheet = Option.getOrThrow(m.sheet);
        expect(sheet.session).toBe("lead");
        expect(sheet.setting.title).toBe("Lead");
        expect(sheet.setting.target).toEqual({ _tag: "Lead", ...workstream });
        expect(sheet.setting.own).toEqual(
          Option.some({ harness: "claude-code", model: "opus", effort: "medium" }),
        );
        expect(sheet.setting.fallback).toEqual({
          harness: "claude-code",
          model: "sonnet",
          effort: "low",
        });
      }),
      StoryCommand.expectHas(FetchHarnesses),
      resolveHarnesses,
    );
  });

  it("opens the sheet for the Implementer with no own setting when the binding comes from the Role", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedChange({ session: "implementer" })),
      model((m) => {
        const sheet = Option.getOrThrow(m.sheet);
        expect(sheet.setting.title).toBe("Implementer of the Workstream");
        expect(sheet.setting.target).toEqual({ _tag: "WorkstreamImplementer", ...workstream });
        expect(Option.isNone(sheet.setting.own)).toBe(true);
      }),
      StoryCommand.expectHas(FetchHarnesses),
      resolveHarnesses,
    );
  });

  it("opens the sheet for the Operator with no own setting, and the binding of the Role as default", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedChange({ session: "operator" })),
      model((m) => {
        const sheet = Option.getOrThrow(m.sheet);
        expect(sheet.session).toBe("operator");
        expect(sheet.setting.title).toBe("Operator");
        expect(sheet.setting.target).toEqual({ _tag: "Operator", ...workstream });
        expect(Option.isNone(sheet.setting.own)).toBe(true);
        expect(sheet.setting.fallback).toEqual({
          harness: "claude-code",
          model: "haiku",
          effort: "medium",
        });
      }),
      StoryCommand.expectHas(FetchHarnesses),
      resolveHarnesses,
    );
  });

  it("opens no sheet before the details and the Roles are known", () => {
    story(
      update,
      given(createModel({ roles: Option.none() })),
      message(Message.ClickedChange({ session: "lead" })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
      }),
      StoryCommand.expectNone(),
    );
  });

  it("marks the session as own after a saved setting, and closes the sheet", () => {
    const setting = { harness: "devin", model: "swe-1.5", effort: "low" };

    story(
      update,
      given(createModel()),
      message(Message.ClickedChange({ session: "implementer" })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.saved(setting) })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        expect(Option.getOrThrow(m.details).implementer).toEqual({ ...setting, own: true });
        expect(Option.getOrThrow(m.details).lead.model).toBe("opus");
      }),
    );
  });

  it("marks the Operator as own after a saved setting, and keeps the other sessions", () => {
    const setting = { harness: "devin", model: "swe-1.5", effort: "low" };

    story(
      update,
      given(createModel()),
      message(Message.ClickedChange({ session: "operator" })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.saved(setting) })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        expect(Option.getOrThrow(m.details).operator).toEqual({ ...setting, own: true });
        expect(Option.getOrThrow(m.details).implementer.own).toBe(false);
        expect(Option.getOrThrow(m.details).lead.model).toBe("opus");
      }),
    );
  });

  it("gives the binding of the Role of the Operator back after Use the default", () => {
    story(
      update,
      given(
        createModel({
          details: Option.some({
            ...details,
            operator: { harness: "devin", model: "swe-1.5", effort: "low", own: true },
          }),
        }),
      ),
      message(Message.ClickedChange({ session: "operator" })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.cleared })),
      model((m) => {
        expect(Option.getOrThrow(m.details).operator).toEqual({
          harness: "claude-code",
          model: "haiku",
          effort: "medium",
          own: false,
        });
      }),
    );
  });

  it("gives the binding of the Role back after Use the default", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedChange({ session: "lead" })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.cleared })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        expect(Option.getOrThrow(m.details).lead).toEqual({
          harness: "claude-code",
          model: "sonnet",
          effort: "low",
          own: false,
        });
      }),
    );
  });

  it("keeps the binding when the Owner closes the sheet", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedChange({ session: "lead" })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.closed })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        expect(Option.getOrThrow(m.details).lead.model).toBe("opus");
      }),
    );
  });

  it("asks the host for the login when the sheet gets a 401", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedChange({ session: "lead" })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.loginRequired })),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });
});

describe("the close of the Workstream as won't do", () => {
  const items = [
    {
      kind: "issue" as const,
      number: 13,
      repository: "owner/shop",
      title: "Task 13",
      url: "https://github.com/owner/shop/issues/13",
    },
  ];

  it("asks for the list of the items that the close closes, and shows it", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedClose()),
      model((m) => {
        expect(Option.getOrThrow(m.closure)).toEqual({
          items: Option.none(),
          error: "",
          busy: false,
        });
      }),
      StoryCommand.expectExact(FetchClosure(workstream)),
      StoryCommand.resolve(FetchClosure, Message.ClosureLoaded({ items })),
      model((m) => {
        expect(Option.getOrThrow(m.closure).items).toEqual(Option.some(items));
      }),
    );
  });

  it("shows the reason when the list fails", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedClose()),
      StoryCommand.resolve(FetchClosure, Message.ClosureFailed({ error: "GitHub is down" })),
      model((m) => {
        expect(Option.getOrThrow(m.closure).error).toBe("GitHub is down");
        expect(Option.isNone(Option.getOrThrow(m.closure).items)).toBe(true);
      }),
    );
  });

  it("closes the Workstream, and keeps the wait until the Workstream leaves the list", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedClose()),
      StoryCommand.resolve(FetchClosure, Message.ClosureLoaded({ items })),
      message(Message.ClickedCloseWorkstream()),
      model((m) => {
        expect(Option.getOrThrow(m.closure).busy).toBe(true);
      }),
      StoryCommand.expectExact(CloseWorkstream(workstream)),
      StoryCommand.resolve(CloseWorkstream, Message.WorkstreamClosed()),
      model((m) => {
        expect(Option.getOrThrow(m.closure).busy).toBe(true);
      }),
      expectNoOutMessage(),
    );
  });

  it("ends the wait and shows the reason when the close fails", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedClose()),
      StoryCommand.resolve(FetchClosure, Message.ClosureLoaded({ items })),
      message(Message.ClickedCloseWorkstream()),
      StoryCommand.resolve(
        CloseWorkstream,
        Message.CloseFailed({ error: "The Workstream has a running task." }),
      ),
      model((m) => {
        const closure = Option.getOrThrow(m.closure);
        expect(closure.busy).toBe(false);
        expect(closure.error).toBe("The Workstream has a running task.");
      }),
    );
  });

  it("closes the confirmation with Cancel and with Escape", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedClose()),
      StoryCommand.resolve(FetchClosure, Message.ClosureLoaded({ items })),
      message(Message.DismissedClosure()),
      model((m) => {
        expect(Option.isNone(m.closure)).toBe(true);
      }),
      message(Message.ClickedClose()),
      StoryCommand.resolve(FetchClosure, Message.ClosureLoaded({ items })),
      message(Message.PressedEscape()),
      model((m) => {
        expect(Option.isNone(m.closure)).toBe(true);
      }),
    );
  });
});
