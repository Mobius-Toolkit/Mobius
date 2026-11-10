import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { GoBack, RequestSwitch } from "./commands";
import { Message } from "./message";
import { initialModel, isOn, shownError } from "./model";
import { update } from "./update";

const allOn = { start: true, merge: true, repairs: true };
const noErrors = { start: "", merge: "", repairs: "" };

describe("the switches", () => {
  it("shows each switch as on until the shell gives the switches", () => {
    story(
      update,
      given(initialModel),
      model((m) => {
        expect(isOn(m, "start")).toBe(true);
        expect(isOn(m, "merge")).toBe(true);
        expect(isOn(m, "repairs")).toBe(true);
      }),
      message(Message.ReceivedSwitches({ switches: { ...allOn, merge: false } })),
      model((m) => {
        expect(isOn(m, "start")).toBe(true);
        expect(isOn(m, "merge")).toBe(false);
      }),
      message(Message.ReceivedSwitches({ switches: null })),
      model((m) => {
        expect(Option.isNone(m.switches)).toBe(true);
      }),
    );
  });

  it("gives the change of a switch to the shell, and changes no value itself", () => {
    story(
      update,
      given({ ...initialModel, switches: Option.some(allOn) }),
      message(Message.ClickedSwitch({ kind: "start", on: false })),
      model((m) => {
        expect(isOn(m, "start")).toBe(true);
      }),
      StoryCommand.expectExact(RequestSwitch({ kind: "start", on: false })),
      StoryCommand.resolve(RequestSwitch, Message.CompletedRequestSwitch()),
    );
  });

  it("goes back with the back button", () => {
    story(
      update,
      given(initialModel),
      message(Message.ClickedBack()),
      StoryCommand.expectExact(GoBack()),
      StoryCommand.resolve(GoBack, Message.CompletedGoBack()),
    );
  });
});

describe("the error of a switch", () => {
  it("shows the error of a switch that is on, and none for a switch that is off", () => {
    story(
      update,
      given({ ...initialModel, switches: Option.some(allOn) }),
      message(
        Message.ReceivedErrors({ errors: { ...noErrors, start: "The switch did not change." } }),
      ),
      model((m) => {
        expect(shownError(m, "start")).toBe("The switch did not change.");
        expect(shownError(m, "merge")).toBe("");
      }),
      message(Message.ReceivedSwitches({ switches: { ...allOn, start: false } })),
      model((m) => {
        expect(shownError(m, "start")).toBe("");
      }),
    );
  });

  it("clears the error when the shell clears it", () => {
    story(
      update,
      given({ ...initialModel, errors: { ...noErrors, start: "The switch did not change." } }),
      message(Message.ReceivedErrors({ errors: noErrors })),
      model((m) => {
        expect(shownError(m, "start")).toBe("");
      }),
    );
  });

  it("shows the error of a switch while the shell has not given the switches", () => {
    story(
      update,
      given({ ...initialModel, errors: { ...noErrors, repairs: "The switch did not change." } }),
      model((m) => {
        expect(shownError(m, "repairs")).toBe("The switch did not change.");
      }),
    );
  });
});
