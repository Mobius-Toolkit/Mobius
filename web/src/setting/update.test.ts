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
import { ClearSetting, FetchHarnesses, SaveSetting } from "./commands";
import { Message, OutMessage } from "./message";
import { initialModel, type Model } from "./model";
import { Target, type HarnessChoices } from "./schemas";
import { update } from "./update";

const harnesses: HarnessChoices[] = [
  {
    harness: "devin",
    error: "",
    models: [{ model: "swe-1.5", efforts: ["low", "high"] }],
  },
  {
    harness: "claude-code",
    error: "",
    models: [
      { model: "opus", efforts: ["medium", "high"] },
      { model: "sonnet", efforts: ["low", "medium"] },
    ],
  },
  { harness: "plain", error: "", models: [{ model: "one", efforts: [] }] },
];

const target = Target.members[0].make({ owner: "owner", name: "shop", number: 39 });
const fallback = { harness: "devin", model: "swe-1.5", effort: "high" };

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel({
    title: "Implementer of #39",
    target,
    own: Option.none(),
    fallback,
    fallbackSource: "role",
  }),
  harnesses: Option.some(harnesses),
  ...overrides,
});

describe("the setting sheet", () => {
  it("starts with the default setting, and with the own setting when there is one", () => {
    expect(createModel()).toMatchObject({ harness: "devin", model: "swe-1.5", effort: "high" });
    const own = { harness: "claude-code", model: "sonnet", effort: "low" };
    expect(
      createModel(
        initialModel({
          title: "Lead",
          target,
          own: Option.some(own),
          fallback,
          fallbackSource: "role",
        }),
      ),
    ).toMatchObject(own);
  });

  it("shows an empty list of Harnesses and the reason when the read fails", () => {
    story(
      update,
      given(createModel({ harnesses: Option.none() })),
      message(Message.HarnessesFailed({ error: "no PATH" })),
      model((m) => {
        expect(m.harnesses).toEqual(Option.some([]));
        expect(m.error).toBe("no PATH");
      }),
    );
  });

  it("takes the first model of a new Harness, and keeps the effort when the model has it", () => {
    story(
      update,
      given(createModel()),
      message(Message.PickedHarness({ value: "claude-code" })),
      model((m) => {
        expect(m).toMatchObject({ harness: "claude-code", model: "opus", effort: "high" });
      }),
      message(Message.PickedModel({ value: "sonnet" })),
      model((m) => {
        expect(m).toMatchObject({ model: "sonnet", effort: "low" });
      }),
      message(Message.PickedEffort({ value: "medium" })),
      model((m) => {
        expect(m.effort).toBe("medium");
      }),
    );
  });

  it("changes nothing when the Owner picks the Harness that is set", () => {
    story(
      update,
      given(createModel({ openChoice: Option.some("harness") })),
      message(Message.PickedHarness({ value: "devin" })),
      model((m) => {
        expect(m).toMatchObject({ harness: "devin", model: "swe-1.5", effort: "high" });
        expect(Option.isNone(m.openChoice)).toBe(true);
      }),
    );
  });

  it("opens one list at a time, and Escape closes the list before the sheet", () => {
    story(
      update,
      given(createModel()),
      message(Message.ToggledChoice({ choice: "model" })),
      model((m) => {
        expect(m.openChoice).toEqual(Option.some("model"));
      }),
      message(Message.PressedEscape()),
      model((m) => {
        expect(Option.isNone(m.openChoice)).toBe(true);
      }),
      expectNoOutMessage(),
      message(Message.PressedEscape()),
      expectOutMessage(OutMessage.Closed()),
    );
  });

  it("saves the setting, and gives it to the parent after the 204", () => {
    const setting = { harness: "devin", model: "swe-1.5", effort: "high" };

    story(
      update,
      given(createModel()),
      message(Message.ClickedSave()),
      model((m) => {
        expect(m.saving).toBe(true);
      }),
      StoryCommand.expectExact(SaveSetting({ target, title: "Implementer of #39", setting })),
      StoryCommand.resolve(SaveSetting, Message.Saved({ setting })),
      expectOutMessage(OutMessage.Changed({ setting: Option.some(setting) })),
    );
  });

  it("saves an empty effort for a model with no effort levels", () => {
    story(
      update,
      given(createModel({ harness: "plain", model: "one", effort: "high" })),
      message(Message.ClickedSave()),
      StoryCommand.expectExact(
        SaveSetting({
          target,
          title: "Implementer of #39",
          setting: { harness: "plain", model: "one", effort: "" },
        }),
      ),
      StoryCommand.resolve(
        SaveSetting,
        Message.Failed({ error: "roles.reviewer: must not have the same harness" }),
      ),
      model((m) => {
        expect(m.saving).toBe(false);
        expect(m.error).toBe("roles.reviewer: must not have the same harness");
      }),
    );
  });

  it("clears the setting with Use the default", () => {
    story(
      update,
      given(
        createModel({
          own: Option.some({ harness: "claude-code", model: "sonnet", effort: "low" }),
        }),
      ),
      message(Message.ClickedUseDefault()),
      StoryCommand.expectExact(ClearSetting({ target, title: "Implementer of #39" })),
      StoryCommand.resolve(ClearSetting, Message.Cleared()),
      expectOutMessage(OutMessage.Changed({ setting: Option.none() })),
    );
  });

  it("asks the parent for the login on a 401, and ends the wait", () => {
    story(
      update,
      given(createModel({ saving: true })),
      message(Message.LoginRequired()),
      model((m) => {
        expect(m.saving).toBe(false);
      }),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });

  it("gives the close to the parent", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedClose()),
      expectOutMessage(OutMessage.Closed()),
    );
    expect(FetchHarnesses().name).toBe("FetchHarnesses");
  });
});
