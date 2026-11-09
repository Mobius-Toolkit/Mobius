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
import { AttachTask, ChangeIssue, FetchFallback, FetchTasks } from "./commands";
import { Message, OutMessage } from "./message";
import { initialModel, rowKey, rowOf, withoutClosed, type Model, type TaskLine } from "./model";
import { update } from "./update";

const workstream = { owner: "owner", name: "shop", number: 12 };

const line = (number: number, overrides: Partial<TaskLine> = {}): TaskLine => ({
  blockedBy: [],
  depth: 0,
  implementer: null,
  number,
  otherRepository: false,
  repository: "owner/shop",
  state: "open",
  title: `Task ${number}`,
  url: `https://github.com/owner/shop/issues/${number}`,
  ...overrides,
});

const resolveHarnesses = StoryCommand.resolve(FetchHarnesses, SettingFixture.harnessesLoaded);

const fallback = { harness: "devin", model: "swe-1.5", effort: "high", own: false };

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ ...workstream, instance: "side" }),
  ...overrides,
});

describe("the Tasks tab", () => {
  it("reads the tasks and the default setting of the Workstream, and shows the tasks", () => {
    const lines = [line(39)];

    story(
      update,
      given(createModel()),
      message(Message.TasksLoaded({ lines })),
      model((m) => {
        expect(m.lines).toEqual(Option.some(lines));
      }),
      message(Message.FallbackLoaded({ fallback })),
      model((m) => {
        expect(m.fallback).toEqual(Option.some(fallback));
      }),
    );
  });

  it("asks the host for the login on a 401, and keeps the tasks", () => {
    story(
      update,
      given(createModel()),
      message(Message.LoginRequired()),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });

  it("shows a failed read", () => {
    story(
      update,
      given(createModel()),
      message(Message.LoadFailed({ error: "database is locked" })),
      model((m) => {
        expect(m.error).toBe("database is locked");
      }),
      expectNoOutMessage(),
    );
  });
});

describe("Chat about a task", () => {
  it("attaches the task to the draft of the Lead chat, and tells the host after the write", () => {
    const task = line(39, { state: "triage" });
    const key = rowKey(task);

    story(
      update,
      given(createModel({ lines: Option.some([task]) })),
      message(Message.ClickedChat({ key, line: task })),
      StoryCommand.expectExact(
        AttachTask({
          key,
          draftKey: "chatDraft:owner/owner/shop/12",
          item: {
            id: task.url,
            reference: "#39",
            title: "Task 39",
            reason: "triage",
            link: task.url,
          },
        }),
      ),
      StoryCommand.resolve(AttachTask, Message.AttachedTask()),
      expectOutMessage(OutMessage.TaskAttached()),
    );
  });

  it("names the repository of a task in another repository", () => {
    const task = line(7, { repository: "plants/orchard", otherRepository: true });
    const key = rowKey(task);

    story(
      update,
      given(createModel({ lines: Option.some([task]) })),
      message(Message.ClickedChat({ key, line: task })),
      StoryCommand.expectExact(
        AttachTask({
          key,
          draftKey: "chatDraft:owner/owner/shop/12",
          item: {
            id: task.url,
            reference: "plants/orchard#7",
            title: "Task 7",
            reason: "open",
            link: task.url,
          },
        }),
      ),
      StoryCommand.resolve(AttachTask, Message.AttachedTask()),
    );
  });

  it("shows a failed write on the row and keeps the panel", () => {
    const task = line(39);
    const key = rowKey(task);

    story(
      update,
      given(createModel({ lines: Option.some([task]) })),
      message(Message.ClickedChat({ key, line: task })),
      StoryCommand.resolve(AttachTask, Message.AttachFailed({ key, error: "storage is full" })),
      model((m) => {
        expect(rowOf(m, key).error).toBe("storage is full");
      }),
      expectNoOutMessage(),
    );
  });
});

describe("Start, Resume and Stop of a task", () => {
  const open = line(39);
  const key = rowKey(open);

  it("starts an open task, waits for the request, and clears the wait after it", () => {
    story(
      update,
      given(createModel({ lines: Option.some([open]) })),
      message(Message.ClickedStart({ key, line: open })),
      model((m) => {
        expect(rowOf(m, key)).toMatchObject({ starting: true, stopping: false, error: "" });
      }),
      StoryCommand.expectExact(
        ChangeIssue({
          key,
          action: "start",
          owner: "owner",
          name: "shop",
          number: 39,
          reference: "#39",
        }),
      ),
      StoryCommand.resolve(ChangeIssue, Message.ChangedIssue({ key })),
      model((m) => {
        expect(rowOf(m, key)).toMatchObject({ starting: false, error: "" });
      }),
    );
  });

  it("starts a task with open blockers as a start when unblocked", () => {
    const blocked = line(71, {
      blockedBy: [{ number: 70, repository: null, workstreamTitle: null }],
    });
    const blockedKey = rowKey(blocked);

    story(
      update,
      given(createModel({ lines: Option.some([blocked]) })),
      message(Message.ClickedStart({ key: blockedKey, line: blocked })),
      StoryCommand.expectExact(
        ChangeIssue({
          key: blockedKey,
          action: "start-blocked",
          owner: "owner",
          name: "shop",
          number: 71,
          reference: "#71",
        }),
      ),
      StoryCommand.resolve(ChangeIssue, Message.ChangedIssue({ key: blockedKey })),
    );
  });

  it("resumes a task that needs a human, and names the repository of a task in another repository", () => {
    const resumed = line(7, {
      state: "needs-human",
      repository: "plants/orchard",
      otherRepository: true,
    });
    const resumedKey = rowKey(resumed);

    story(
      update,
      given(createModel({ lines: Option.some([resumed]) })),
      message(Message.ClickedStart({ key: resumedKey, line: resumed })),
      StoryCommand.expectExact(
        ChangeIssue({
          key: resumedKey,
          action: "resume",
          owner: "plants",
          name: "orchard",
          number: 7,
          reference: "plants/orchard#7",
        }),
      ),
      StoryCommand.resolve(ChangeIssue, Message.ChangedIssue({ key: resumedKey })),
    );
  });

  it("stops a running task, and shows the reason when the request fails", () => {
    const working = line(40, { state: "working" });
    const workingKey = rowKey(working);

    story(
      update,
      given(createModel({ lines: Option.some([working]) })),
      message(Message.ClickedStop({ key: workingKey, line: working })),
      model((m) => {
        expect(rowOf(m, workingKey)).toMatchObject({ stopping: true, starting: false });
      }),
      StoryCommand.expectExact(
        ChangeIssue({
          key: workingKey,
          action: "stop",
          owner: "owner",
          name: "shop",
          number: 40,
          reference: "#40",
        }),
      ),
      StoryCommand.resolve(
        ChangeIssue,
        Message.ChangeFailed({ key: workingKey, error: "The issue has no task to stop." }),
      ),
      model((m) => {
        expect(rowOf(m, workingKey)).toMatchObject({
          stopping: false,
          error: "The issue has no task to stop.",
        });
      }),
    );
  });

  it("clears the reason of a failed start at the next start", () => {
    story(
      update,
      given(createModel({ lines: Option.some([open]) })),
      message(Message.ClickedStart({ key, line: open })),
      StoryCommand.resolve(ChangeIssue, Message.ChangeFailed({ key, error: "refused" })),
      model((m) => {
        expect(rowOf(m, key).error).toBe("refused");
      }),
      message(Message.ClickedStart({ key, line: open })),
      model((m) => {
        expect(rowOf(m, key)).toMatchObject({ starting: true, error: "" });
      }),
      StoryCommand.resolve(ChangeIssue, Message.ChangedIssue({ key })),
    );
  });
});

describe("the Implementer setting of a task", () => {
  const task = line(39);
  const own = { harness: "claude-code", model: "sonnet", effort: "medium" };

  it("opens no sheet before the default setting is known", () => {
    story(
      update,
      given(createModel({ lines: Option.some([task]) })),
      message(Message.ClickedSetting({ line: task })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
      }),
      StoryCommand.expectNone(),
    );
  });

  it("opens the sheet with the setting of the task and the default, and reads the Harnesses", () => {
    story(
      update,
      given(
        createModel({
          lines: Option.some([{ ...task, implementer: own }]),
          fallback: Option.some({ ...fallback, own: true }),
        }),
      ),
      message(Message.ClickedSetting({ line: { ...task, implementer: own } })),
      model((m) => {
        const sheet = Option.getOrThrow(m.sheet);
        expect(sheet.title).toBe("Implementer of #39");
        expect(sheet.fallbackSource).toBe("Workstream");
        expect(sheet.own).toEqual(Option.some(own));
        expect(sheet.harness).toBe("claude-code");
      }),
      StoryCommand.expectHas(FetchHarnesses),
      resolveHarnesses,
    );
  });

  it("shows a saved setting on the row and closes the sheet", () => {
    story(
      update,
      given(
        createModel({
          lines: Option.some([task, line(40)]),
          fallback: Option.some(fallback),
        }),
      ),
      message(Message.ClickedSetting({ line: task })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.saved(own) })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        const lines = Option.getOrThrow(m.lines);
        expect(lines[0].implementer).toEqual(own);
        expect(lines[1].implementer).toBeNull();
      }),
    );
  });

  it("removes the setting from the row after Use the default", () => {
    story(
      update,
      given(
        createModel({
          lines: Option.some([{ ...task, implementer: own }]),
          fallback: Option.some(fallback),
        }),
      ),
      message(Message.ClickedSetting({ line: { ...task, implementer: own } })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.cleared })),
      model((m) => {
        expect(Option.getOrThrow(m.lines)[0].implementer).toBeNull();
      }),
    );
  });

  it("keeps the setting of the row when the Owner closes the sheet", () => {
    story(
      update,
      given(createModel({ lines: Option.some([task]), fallback: Option.some(fallback) })),
      message(Message.ClickedSetting({ line: task })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.closed })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        expect(Option.getOrThrow(m.lines)[0].implementer).toBeNull();
      }),
    );
  });

  it("asks the host for the login when the sheet gets a 401", () => {
    story(
      update,
      given(createModel({ lines: Option.some([task]), fallback: Option.some(fallback) })),
      message(Message.ClickedSetting({ line: task })),
      resolveHarnesses,
      message(Message.GotSettingMessage({ message: SettingFixture.loginRequired })),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });
});

describe("the list of the tasks", () => {
  it("shows or hides the closed tasks", () => {
    story(
      update,
      given(createModel()),
      message(Message.ToggledClosed()),
      model((m) => {
        expect(m.showClosed).toBe(true);
      }),
      message(Message.ToggledClosed()),
      model((m) => {
        expect(m.showClosed).toBe(false);
      }),
    );
  });

  it("moves the sub-issues of a removed closed task up one level for each removed ancestor", () => {
    const lines = [
      line(1, { depth: 0 }),
      line(2, { depth: 0, state: "closed" }),
      line(3, { depth: 1 }),
      line(4, { depth: 2, state: "closed" }),
      line(5, { depth: 3 }),
      line(6, { depth: 0 }),
    ];

    expect(withoutClosed(lines).map(({ number, depth }) => [number, depth])).toEqual([
      [1, 0],
      [3, 0],
      [5, 1],
      [6, 0],
    ]);
  });

  it("reads the tasks and the default setting again", () => {
    const loaded = createModel();
    story(
      update,
      given(loaded),
      message(Message.TasksLoaded({ lines: [] })),
      model((m) => {
        expect(m.lines).toEqual(Option.some([]));
      }),
    );
    expect(FetchTasks(workstream).name).toBe("FetchTasks");
    expect(FetchFallback(workstream).name).toBe("FetchFallback");
  });
});
