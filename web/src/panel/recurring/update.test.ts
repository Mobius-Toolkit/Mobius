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
import { DeleteTask, SetPaused } from "./commands";
import { Message, OutMessage } from "./message";
import { initialModel, type Model, type RecurringTask } from "./model";
import { update } from "./update";

const workstream = { owner: "owner", name: "shop", number: 12 };

const task = (overrides: Partial<RecurringTask> = {}): RecurringTask => ({
  cron: "0 8 * * 1-5",
  id: 1,
  next: "2026-09-29T08:00:00Z",
  paused: false,
  runs: [],
  text: "Check the open pull requests",
  ...overrides,
});

const first = task();
const second = task({ id: 2, cron: "0 9 * * 1", next: null, paused: true, text: "Weekly cost" });

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ ...workstream, instance: "side" }),
  tasks: Option.some([first, second]),
  ...overrides,
});

describe("the list", () => {
  it("shows the tasks that the read gives, and a failed read", () => {
    story(
      update,
      given(createModel({ tasks: Option.none() })),
      message(Message.TasksLoaded({ tasks: [first, second] })),
      model((m) => {
        expect(m.tasks).toEqual(Option.some([first, second]));
      }),
      message(Message.LoadFailed({ error: "none" })),
      model((m) => {
        expect(m.error).toBe("none");
        expect(m.tasks).toEqual(Option.some([first, second]));
      }),
      message(Message.TasksLoaded({ tasks: [first] })),
      model((m) => {
        expect(m.error).toBe("");
      }),
    );
  });

  it("closes the history of a task that the new read does not have", () => {
    story(
      update,
      given(createModel({ opened: Option.some(2) })),
      message(Message.TasksLoaded({ tasks: [first] })),
      model((m) => {
        expect(m.opened).toEqual(Option.none());
      }),
    );
  });

  it("asks the host for the login on a 401", () => {
    story(
      update,
      given(createModel({ busy: [1], deletion: Option.some({ id: 1, text: "x", busy: true }) })),
      message(Message.LoginRequired()),
      model((m) => {
        expect(m.busy).toEqual([]);
        expect(m.deletion).toEqual(Option.some({ id: 1, text: "x", busy: false }));
      }),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });
});

describe("the history", () => {
  it("opens the history of a task with a tap, and closes it with a second tap", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedRow({ id: 1 })),
      model((m) => {
        expect(m.opened).toEqual(Option.some(1));
      }),
      message(Message.ClickedRow({ id: 2 })),
      model((m) => {
        expect(m.opened).toEqual(Option.some(2));
      }),
      message(Message.ClickedRow({ id: 2 })),
      model((m) => {
        expect(m.opened).toEqual(Option.none());
      }),
    );
  });
});

describe("the pause", () => {
  it("sends the new state, waits, and shows the task that the answer gives", () => {
    const paused = task({ paused: true, next: null });
    story(
      update,
      given(createModel()),
      message(Message.ClickedPause({ id: 1, paused: true })),
      model((m) => {
        expect(m.busy).toEqual([1]);
      }),
      StoryCommand.expectExact(SetPaused({ ...workstream, id: 1, paused: true })),
      StoryCommand.resolve(SetPaused, Message.PausedChanged({ task: paused })),
      model((m) => {
        expect(m.busy).toEqual([]);
        expect(m.tasks).toEqual(Option.some([paused, second]));
      }),
    );
  });

  it("keeps the task and the other busy tasks when a pause fails", () => {
    story(
      update,
      given(createModel({ busy: [2] })),
      message(Message.ClickedPause({ id: 1, paused: true })),
      StoryCommand.resolve(SetPaused, Message.PauseFailed({ id: 1 })),
      model((m) => {
        expect(m.busy).toEqual([2]);
        expect(m.tasks).toEqual(Option.some([first, second]));
      }),
    );
  });

  it("asks the host for the login when the request gets a 401", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedPause({ id: 1, paused: true })),
      StoryCommand.resolve(SetPaused, Message.LoginRequired()),
      model((m) => {
        expect(m.busy).toEqual([]);
      }),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });
});

describe("the delete", () => {
  it("asks first, and sends the delete only after the confirmation", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedDelete({ id: 1, text: first.text })),
      model((m) => {
        expect(m.deletion).toEqual(Option.some({ id: 1, text: first.text, busy: false }));
      }),
      StoryCommand.expectNone(),
      message(Message.ClickedConfirmDelete()),
      model((m) => {
        expect(m.deletion).toEqual(Option.some({ id: 1, text: first.text, busy: true }));
      }),
      StoryCommand.expectExact(DeleteTask({ ...workstream, id: 1 })),
      StoryCommand.resolve(DeleteTask, Message.TaskDeleted({ id: 1 })),
      model((m) => {
        expect(m.deletion).toEqual(Option.none());
        expect(m.tasks).toEqual(Option.some([second]));
      }),
    );
  });

  it("closes the dialog with Cancel and with Escape, and sends nothing", () => {
    story(
      update,
      given(createModel({ deletion: Option.some({ id: 1, text: first.text, busy: false }) })),
      message(Message.DismissedDelete()),
      model((m) => {
        expect(m.deletion).toEqual(Option.none());
      }),
      message(Message.ClickedDelete({ id: 1, text: first.text })),
      message(Message.PressedEscape()),
      model((m) => {
        expect(m.deletion).toEqual(Option.none());
        expect(m.tasks).toEqual(Option.some([first, second]));
      }),
      StoryCommand.expectNone(),
      expectNoOutMessage(),
    );
  });

  it("closes the dialog and keeps the task when the delete fails", () => {
    story(
      update,
      given(createModel({ deletion: Option.some({ id: 1, text: first.text, busy: false }) })),
      message(Message.ClickedConfirmDelete()),
      StoryCommand.resolve(DeleteTask, Message.DeleteFailed()),
      model((m) => {
        expect(m.deletion).toEqual(Option.none());
        expect(m.tasks).toEqual(Option.some([first, second]));
      }),
    );
  });

  it("closes the history of the deleted task", () => {
    story(
      update,
      given(createModel({ opened: Option.some(1) })),
      message(Message.TaskDeleted({ id: 1 })),
      model((m) => {
        expect(m.opened).toEqual(Option.none());
      }),
    );
  });
});
