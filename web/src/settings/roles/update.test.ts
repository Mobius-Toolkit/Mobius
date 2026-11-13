import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import type { HarnessChoices } from "../schemas";
import {
  AddBinding,
  RemoveBinding,
  RequestLogin,
  ResetRole,
  SaveRole,
  SetBinding,
} from "./commands";
import { Message } from "./message";
import { initialModel, type Model, type Sheet } from "./model";
import type { Role } from "./schemas";
import { update } from "./update";

const noUi = { context: false, effort: false, harness: false, max: false, model: false };

const role = (overrides: Partial<Role> = {}): Role => ({
  context: 0,
  effort: "high",
  harness: "claude-code",
  max: 2,
  model: "opus",
  moreBindings: [],
  role: "lead",
  ui: noUi,
  ...overrides,
});

const harnesses: HarnessChoices[] = [
  {
    error: "",
    harness: "claude-code",
    models: [
      { model: "opus", efforts: ["low", "high"] },
      { model: "haiku", efforts: ["low"] },
    ],
  },
  { error: "", harness: "antigravity", models: [{ model: "sonnet", efforts: [] }] },
  { error: "", harness: "dsh", models: [] },
];

const loaded = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel,
  roles: Option.some([role(), role({ role: "judge", model: "haiku", effort: "low" })]),
  harnesses: Option.some(harnesses),
  ...overrides,
});

const open = (name: Role["role"], overrides: Partial<Model> = {}) =>
  update(loaded(overrides), Message.OpenedRole({ role: name })).model;

const sheet = (m: Model): Sheet => Option.getOrThrow(m.sheet);

describe("the sheet of a role", () => {
  it("starts with the values of the role, and has no context text for a role with no limit", () => {
    story(
      update,
      given(loaded()),
      message(Message.OpenedRole({ role: "lead" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({
          harness: "claude-code",
          model: "opus",
          effort: "high",
          context: "",
          max: "2",
          saving: false,
          error: "",
        });
      }),
    );
  });

  it("shows the context of a role as a text", () => {
    const m = open("lead", { roles: Option.some([role({ context: 200000 })]) });
    expect(sheet(m).context).toBe("200000");
  });

  it("opens no sheet for a role that the list does not have", () => {
    story(
      update,
      given(loaded()),
      message(Message.OpenedRole({ role: "curator" })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
      }),
    );
  });

  it("closes the menu of a choice with the Escape key first, and then the sheet", () => {
    story(
      update,
      given(open("lead")),
      message(Message.ToggledChoice({ choice: "model" })),
      model((m) => {
        expect(Option.isSome(sheet(m).openChoice)).toBe(true);
      }),
      message(Message.PressedEscape()),
      model((m) => {
        expect(Option.isNone(sheet(m).openChoice)).toBe(true);
      }),
      message(Message.PressedEscape()),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
      }),
    );
  });
});

describe("the choices of the form", () => {
  it("selects the first model of a new Harness, and keeps an effort that the model has", () => {
    story(
      update,
      given(open("judge")),
      message(Message.PickedHarness({ value: "antigravity" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ harness: "antigravity", model: "sonnet", effort: "" });
      }),
      message(Message.PickedHarness({ value: "claude-code" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ harness: "claude-code", model: "opus", effort: "low" });
      }),
    );
  });

  it("keeps the model and the effort when the Owner picks the checked Harness", () => {
    story(
      update,
      given(open("judge")),
      message(Message.ToggledChoice({ choice: "harness" })),
      message(Message.PickedHarness({ value: "claude-code" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ model: "haiku", effort: "low" });
        expect(Option.isNone(sheet(m).openChoice)).toBe(true);
      }),
    );
  });

  it("changes the effort with the model, and clears the context of a Harness with no limit", () => {
    story(
      update,
      given(open("lead", { roles: Option.some([role({ context: 200000 })]) })),
      message(Message.PickedModel({ value: "haiku" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ model: "haiku", effort: "low", context: "200000" });
      }),
      message(Message.PickedHarness({ value: "dsh" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ model: "", effort: "", context: "" });
      }),
    );
  });
});

describe("the save", () => {
  it("sends the values of the form, with the context as a number", () => {
    story(
      update,
      given(open("lead")),
      message(Message.ChangedMax({ text: "4" })),
      message(Message.ChangedContext({ text: "200000" })),
      message(Message.ClickedSave()),
      model((m) => {
        expect(sheet(m).saving).toBe(true);
      }),
      StoryCommand.expectExact(
        SaveRole({
          role: "lead",
          harness: "claude-code",
          model: "opus",
          effort: "high",
          context: 200000,
          max: 4,
        }),
      ),
      StoryCommand.resolve(
        SaveRole,
        Message.RoleChanged({
          role: role({ max: 4, context: 200000, ui: { ...noUi, max: true, context: true } }),
        }),
      ),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        const lead = Option.getOrThrow(m.roles).find((other) => other.role === "lead");
        expect(lead).toMatchObject({ max: 4, context: 200000 });
        expect(Option.getOrThrow(m.roles)).toHaveLength(2);
      }),
    );
  });

  it("sends no context for a Harness with no limit, and the effort only when the model has efforts", () => {
    story(
      update,
      given(open("judge")),
      message(Message.PickedHarness({ value: "antigravity" })),
      message(Message.ClickedSave()),
      StoryCommand.expectExact(
        SaveRole({
          role: "judge",
          harness: "antigravity",
          model: "sonnet",
          effort: "",
          context: 0,
          max: 2,
        }),
      ),
      StoryCommand.resolve(SaveRole, Message.SaveFailed({ error: "nope" })),
    );
  });

  it("keeps the effort of the role when the list of models is empty", () => {
    story(
      update,
      given(open("lead", { harnesses: Option.some([]) })),
      message(Message.ClickedSave()),
      StoryCommand.expectExact(
        SaveRole({
          role: "lead",
          harness: "claude-code",
          model: "opus",
          effort: "high",
          context: 0,
          max: 2,
        }),
      ),
      StoryCommand.resolve(SaveRole, Message.SaveFailed({ error: "nope" })),
    );
  });

  it("shows the reason of a refused save in the sheet, and keeps the values of the form and of the row", () => {
    story(
      update,
      given(open("lead")),
      message(Message.PickedHarness({ value: "dsh" })),
      message(Message.ClickedSave()),
      StoryCommand.resolve(
        SaveRole,
        Message.SaveFailed({ error: "roles.lead.model: must not be empty" }),
      ),
      model((m) => {
        expect(sheet(m)).toMatchObject({
          harness: "dsh",
          saving: false,
          error: "roles.lead.model: must not be empty",
        });
        expect(Option.getOrThrow(m.roles)[0]).toMatchObject({ harness: "claude-code" });
      }),
      message(Message.ClickedSave()),
      model((m) => {
        expect(sheet(m).error).toBe("");
      }),
      StoryCommand.resolve(SaveRole, Message.SaveLoginRequired()),
      model((m) => {
        expect(sheet(m).saving).toBe(false);
      }),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});

describe("the reset", () => {
  it("shows the Reset button only for a role with a value of the UI, and resets the role", () => {
    const ui = { ...noUi, model: true };
    const changed = sheet(open("lead", { roles: Option.some([role({ ui })]) }));
    expect(Object.values(changed.role.ui).some(Boolean)).toBe(true);
    story(
      update,
      given(open("lead", { roles: Option.some([role({ ui })]) })),
      message(Message.ClickedReset()),
      model((m) => {
        expect(sheet(m).saving).toBe(true);
      }),
      StoryCommand.expectExact(ResetRole({ role: "lead" })),
      StoryCommand.resolve(ResetRole, Message.RoleChanged({ role: role() })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        expect(Option.getOrThrow(m.roles)[0].ui).toEqual(noUi);
      }),
    );
  });

  it("shows the reason of a refused reset in the sheet", () => {
    story(
      update,
      given(open("lead")),
      message(Message.ClickedReset()),
      StoryCommand.resolve(ResetRole, Message.SaveFailed({ error: "config.toml fails a check" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ saving: false, error: "config.toml fails a check" });
      }),
    );
  });
});

describe("the lists", () => {
  it("fills the model of the harnesses with an empty list after a failed read, and shows the error", () => {
    story(
      update,
      given(initialModel),
      message(Message.HarnessesFailed({ error: "no harness" })),
      model((m) => {
        expect(m.harnesses).toEqual(Option.some([]));
        expect(m.error).toBe("no harness");
      }),
      message(Message.RolesFailed({ error: "no roles" })),
      model((m) => {
        expect(m.error).toBe("no roles");
        expect(Option.isNone(m.roles)).toBe(true);
      }),
      message(Message.LoginRequired()),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});

const second = { id: 3, harness: "antigravity", model: "sonnet", effort: "", context: 0, max: 5 };

const implementer = (overrides: Partial<Role> = {}) =>
  role({
    role: "implementer",
    harness: "claude-code",
    model: "haiku",
    effort: "low",
    max: 2,
    moreBindings: [second],
    ...overrides,
  });

const openImplementer = (overrides: Partial<Role> = {}) =>
  open("implementer", { roles: Option.some([implementer(overrides)]) });

describe("the tabs of the Implementer", () => {
  it("starts on the first tab with no new tab", () => {
    expect(sheet(openImplementer())).toMatchObject({ tab: "0", hasNew: false, model: "haiku" });
  });

  it("adds a new tab with the values of the first binding, and selects the new tab again for a second tap", () => {
    story(
      update,
      given(openImplementer()),
      message(Message.SelectedTab({ key: "3" })),
      message(Message.ClickedAddBinding()),
      model((m) => {
        expect(sheet(m)).toMatchObject({
          tab: "new",
          hasNew: true,
          harness: "claude-code",
          model: "haiku",
          effort: "low",
          max: "2",
        });
      }),
      message(Message.PickedModel({ value: "opus" })),
      message(Message.SelectedTab({ key: "3" })),
      message(Message.ClickedAddBinding()),
      model((m) => {
        expect(sheet(m)).toMatchObject({ tab: "new", hasNew: true, model: "haiku" });
      }),
    );
  });

  it("drops the new tab when the sheet closes", () => {
    story(
      update,
      given(openImplementer()),
      message(Message.ClickedAddBinding()),
      message(Message.ClosedSheet()),
      message(Message.OpenedRole({ role: "implementer" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ tab: "0", hasNew: false });
      }),
    );
  });

  it("gives the fields of the binding of a selected tab, and drops the change of the left tab", () => {
    story(
      update,
      given(openImplementer()),
      message(Message.ChangedMax({ text: "9" })),
      message(Message.SelectedTab({ key: "3" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({
          tab: "3",
          harness: "antigravity",
          model: "sonnet",
          effort: "",
          max: "5",
        });
      }),
      message(Message.SelectedTab({ key: "0" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ tab: "0", harness: "claude-code", max: "2" });
      }),
    );
  });

  it("saves the first tab as the role, a stored tab by its id, and the new tab as an added binding", () => {
    story(
      update,
      given(openImplementer()),
      message(Message.ClickedSave()),
      StoryCommand.expectExact(
        SaveRole({
          role: "implementer",
          harness: "claude-code",
          model: "haiku",
          effort: "low",
          context: 0,
          max: 2,
        }),
      ),
      StoryCommand.resolve(SaveRole, Message.SaveFailed({ error: "nope" })),
      message(Message.SelectedTab({ key: "3" })),
      message(Message.ChangedMax({ text: "6" })),
      message(Message.ClickedSave()),
      StoryCommand.expectExact(
        SetBinding({
          id: 3,
          harness: "antigravity",
          model: "sonnet",
          effort: "",
          context: 0,
          max: 6,
        }),
      ),
      StoryCommand.resolve(SetBinding, Message.SaveFailed({ error: "nope" })),
      message(Message.ClickedAddBinding()),
      message(Message.ClickedSave()),
      StoryCommand.expectExact(
        AddBinding({
          harness: "claude-code",
          model: "haiku",
          effort: "low",
          context: 0,
          max: 2,
        }),
      ),
      StoryCommand.resolve(AddBinding, Message.SaveFailed({ error: "nope" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ tab: "new", saving: false, error: "nope" });
      }),
    );
  });

  it("replaces the role with the response of an add, and selects the added tab", () => {
    const added = { ...second, id: 4, max: 1 };
    story(
      update,
      given(openImplementer()),
      message(Message.ClickedAddBinding()),
      message(Message.ClickedSave()),
      StoryCommand.resolve(
        AddBinding,
        Message.BindingsChanged({
          role: implementer({ moreBindings: [second, added] }),
          tab: "4",
        }),
      ),
      model((m) => {
        expect(sheet(m)).toMatchObject({
          tab: "4",
          hasNew: false,
          saving: false,
          harness: "antigravity",
          max: "1",
        });
        expect(Option.getOrThrow(m.roles)[0].moreBindings).toHaveLength(2);
        expect(sheet(m).role.moreBindings).toHaveLength(2);
      }),
    );
  });

  it("replaces the role with the response of a change, and keeps the tab and the new tab", () => {
    story(
      update,
      given(openImplementer()),
      message(Message.ClickedAddBinding()),
      message(Message.SelectedTab({ key: "3" })),
      message(Message.ChangedMax({ text: "6" })),
      message(Message.ClickedSave()),
      StoryCommand.resolve(
        SetBinding,
        Message.BindingsChanged({
          role: implementer({ moreBindings: [{ ...second, max: 6 }] }),
          tab: "3",
        }),
      ),
      model((m) => {
        expect(sheet(m)).toMatchObject({ tab: "3", hasNew: true, saving: false, max: "6" });
        expect(Option.getOrThrow(m.roles)[0].moreBindings[0].max).toBe(6);
      }),
    );
  });

  it("removes a stored tab with its id and the first tab with the id 0, and selects the first tab", () => {
    story(
      update,
      given(openImplementer()),
      message(Message.SelectedTab({ key: "3" })),
      message(Message.ClickedRemove()),
      model((m) => {
        expect(sheet(m).saving).toBe(true);
      }),
      StoryCommand.expectExact(RemoveBinding({ id: 3 })),
      StoryCommand.resolve(
        RemoveBinding,
        Message.BindingsChanged({ role: implementer({ moreBindings: [] }), tab: "0" }),
      ),
      model((m) => {
        expect(sheet(m)).toMatchObject({ tab: "0", saving: false, harness: "claude-code" });
        expect(sheet(m).role.moreBindings).toHaveLength(0);
      }),
    );
    story(
      update,
      given(openImplementer()),
      message(Message.ClickedRemove()),
      StoryCommand.expectExact(RemoveBinding({ id: 0 })),
      StoryCommand.resolve(
        RemoveBinding,
        Message.BindingsChanged({
          role: implementer({ ...second, effort: "", moreBindings: [] }),
          tab: "0",
        }),
      ),
      model((m) => {
        expect(sheet(m)).toMatchObject({ tab: "0", harness: "antigravity", max: "5" });
        expect(Option.getOrThrow(m.roles)[0]).toMatchObject({ harness: "antigravity" });
      }),
    );
  });

  it("drops the new tab with no request, and selects the first tab", () => {
    story(
      update,
      given(openImplementer()),
      message(Message.ClickedAddBinding()),
      message(Message.ClickedRemove()),
      model((m) => {
        expect(sheet(m)).toMatchObject({ tab: "0", hasNew: false, saving: false });
      }),
      StoryCommand.expectNone(),
    );
  });

  it("ignores a tab tap while a request runs", () => {
    const saving = update(openImplementer(), Message.ClickedSave()).model;
    const tapped = update(saving, Message.SelectedTab({ key: "3" })).model;
    const added = update(tapped, Message.ClickedAddBinding()).model;
    expect(sheet(added)).toMatchObject({ tab: "0", hasNew: false, saving: true });
  });
});
