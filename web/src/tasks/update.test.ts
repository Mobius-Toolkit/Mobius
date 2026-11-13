import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { FetchBoard, Navigate, RequestLogin } from "./commands";
import { Message } from "./message";
import { columns, initialModel, sortColumn, type Model } from "./model";
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
    mergeQueuePlace: null,
    mergeQueueSize: 0,
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

const queued = (number: number, place: number, stateAt: string) =>
  createTask(number, {
    state: "ready_for_review",
    stateAt,
    mergeQueuePlace: place,
    mergeQueueSize: 3,
  });

const plain = (number: number, stateAt: string) =>
  createTask(number, { state: "ready_for_review", stateAt });

const inRepository = (number: number, repository: string, place: number) =>
  createTask(number, {
    repository,
    state: "ready_for_review",
    mergeQueuePlace: place,
    mergeQueueSize: 2,
  });

const withPlaces = (state: BoardTask["state"]) => [
  createTask(1, { state, stateAt: "2026-10-15T09:00:00Z" }),
  createTask(2, {
    state,
    stateAt: "2026-10-15T10:00:00Z",
    mergeQueuePlace: 1,
    mergeQueueSize: 2,
  }),
  createTask(3, {
    state,
    stateAt: "2026-10-15T08:00:00Z",
    mergeQueuePlace: 2,
    mergeQueueSize: 2,
  }),
];

const numbersOf = (title: string, tasks: BoardTask[]) =>
  sortColumn(title, tasks).map(({ number }) => number);

describe("the order of the cards of a column", () => {
  it("shows a queued task in Waiting before an older task with no place", () => {
    const tasks = [
      createTask(1, { state: "approval", stateAt: "2026-10-15T08:00:00Z" }),
      createTask(2, {
        state: "checks",
        stateAt: "2026-10-15T10:00:00Z",
        mergeQueuePlace: 1,
        mergeQueueSize: 3,
      }),
    ];

    expect(numbersOf("Waiting", tasks)).toEqual([2, 1]);
  });

  it("shows the places in order in Ready to merge, and then the tasks with no place, the oldest first", () => {
    const tasks = [
      plain(1, "2026-10-15T09:00:00Z"),
      queued(2, 3, "2026-10-15T07:00:00Z"),
      queued(3, 1, "2026-10-15T11:00:00Z"),
      plain(4, "2026-10-15T08:00:00Z"),
      queued(5, 2, "2026-10-15T10:00:00Z"),
    ];

    expect(numbersOf("Ready to merge", tasks)).toEqual([3, 5, 2, 4, 1]);
  });

  it("sorts two tasks at the same place by repository", () => {
    const tasks = [
      inRepository(1, "owner/shop", 2),
      inRepository(2, "plants/garden", 1),
      inRepository(3, "plants/garden", 2),
      inRepository(4, "owner/shop", 1),
    ];

    expect(numbersOf("Ready to merge", tasks)).toEqual([4, 2, 1, 3]);
  });

  it("keeps the newest first in Done and the oldest first in Queued, also with a place", () => {
    expect(numbersOf("Done", withPlaces("ended"))).toEqual([2, 1, 3]);
    expect(numbersOf("Queued", withPlaces("queued"))).toEqual([3, 1, 2]);
  });
});
