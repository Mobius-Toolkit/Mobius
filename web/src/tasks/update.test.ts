import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { FetchBoard, Navigate, RequestLogin } from "./commands";
import { Message } from "./message";
import { columns, initialModel, type Model } from "./model";
import type { BoardTask } from "./schemas";
import { update } from "./update";

function createTask(number: number, overrides: Partial<BoardTask> = {}): BoardTask {
  return {
    repository: "owner/shop",
    number,
    title: `Task ${number}`,
    url: `https://github.com/owner/shop/issues/${number}`,
    workstreamRepository: "owner/shop",
    workstream: 12,
    workstreamTitle: "Integrate loyalty plans",
    state: "queued",
    stateAt: "2026-10-15T11:00:00Z",
    pullRequest: null,
    pullRequestUrl: null,
    fixRounds: 0,
    reviewRounds: 0,
    merging: false,
    mergeRefusal: "",
    ...overrides,
  };
}

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ organization: "owner" }),
  ...overrides,
});

describe("the Tasks board", () => {
  it("reads the tasks when the connection opens, and shows them", () => {
    const tasks = [createTask(1), createTask(2, { state: "working" })];

    story(
      update,
      given(createModel()),
      message(Message.ConnectionOpened()),
      model((m) => {
        expect(Option.isNone(m.tasks)).toBe(true);
      }),
      StoryCommand.expectExact(FetchBoard()),
      StoryCommand.resolve(FetchBoard, Message.BoardLoaded({ tasks })),
      model((m) => {
        expect(m.tasks).toEqual(Option.some(tasks));
        expect(m.error).toBe("");
      }),
    );
  });

  it("reads the tasks again after a workstreams event or an inbox event, and keeps the board until the answer", () => {
    const before = [createTask(1)];
    const after = [createTask(1, { state: "working" })];

    story(
      update,
      given(createModel({ tasks: Option.some(before) })),
      message(Message.BoardChanged()),
      model((m) => {
        expect(m.tasks).toEqual(Option.some(before));
      }),
      StoryCommand.expectExact(FetchBoard()),
      StoryCommand.resolve(FetchBoard, Message.BoardLoaded({ tasks: after })),
      model((m) => {
        expect(m.tasks).toEqual(Option.some(after));
      }),
    );
  });

  it("asks the host for the login on a 401, and shows no error", () => {
    story(
      update,
      given(createModel()),
      message(Message.BoardChanged()),
      StoryCommand.expectExact(FetchBoard()),
      StoryCommand.resolve(FetchBoard, Message.LoginRequired()),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
      model((m) => {
        expect(m.error).toBe("");
        expect(Option.isNone(m.tasks)).toBe(true);
      }),
    );
  });

  it("shows another failure, keeps the old tasks, and clears the error at the next read", () => {
    const tasks = [createTask(1)];

    story(
      update,
      given(createModel({ tasks: Option.some(tasks) })),
      message(Message.BoardChanged()),
      StoryCommand.expectExact(FetchBoard()),
      StoryCommand.resolve(FetchBoard, Message.BoardFailed({ error: "database is locked" })),
      model((m) => {
        expect(m.error).toBe("database is locked");
        expect(m.tasks).toEqual(Option.some(tasks));
      }),
      message(Message.ConnectionOpened()),
      StoryCommand.expectExact(FetchBoard()),
      StoryCommand.resolve(FetchBoard, Message.BoardLoaded({ tasks })),
      model((m) => {
        expect(m.error).toBe("");
      }),
    );
  });

  it("asks the host to open the Workstream of a card", () => {
    story(
      update,
      given(createModel()),
      message(Message.NavigateTo({ path: "/workstreams/owner/shop/12" })),
      StoryCommand.expectExact(Navigate({ path: "/workstreams/owner/shop/12" })),
      StoryCommand.resolve(Navigate, Message.CompletedNavigate()),
    );
  });

  it("stores the time of the clock", () => {
    story(
      update,
      given(createModel()),
      message(Message.Ticked({ now: 1_000 })),
      model((m) => {
        expect(m.now).toBe(1_000);
      }),
      StoryCommand.expectNone(),
    );
  });
});

const columnOf = (task: BoardTask) =>
  columns.filter(({ has }) => has(task)).map(({ title }) => title);

describe("the columns", () => {
  it("puts a task in the merge in Ready to merge, and not in Waiting", () => {
    expect(columnOf(createTask(1, { state: "ready_for_review", merging: true }))).toEqual([
      "Ready to merge",
    ]);
    expect(columnOf(createTask(2, { state: "ready_for_review" }))).toEqual(["Ready to merge"]);
  });

  it("puts the states that wait for GitHub in Waiting", () => {
    expect(columnOf(createTask(1, { state: "checks" }))).toEqual(["Waiting"]);
    expect(columnOf(createTask(2, { state: "approval" }))).toEqual(["Waiting"]);
  });
});
