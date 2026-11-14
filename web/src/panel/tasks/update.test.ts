import { Option } from "effect";
import * as Scene from "foldkit/scene";
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
import { EnqueueMessage, SaveDraftInboxItems, SaveDraftText } from "@/composer/commands";
import * as ComposerFixture from "@/composer/fixtures";
import type { Message as ComposerMessage } from "@/composer/message";
import { ComposerBehavior } from "@/composer/view";
import { FetchHarnesses } from "@/setting/commands";
import * as SettingFixture from "@/setting/fixtures";
import * as Selection from "@/ui/selection";
import * as SelectionFixture from "@/ui/selection-fixtures";
import {
  AddSelection,
  AttachTask,
  ChangeIssue,
  FetchFallback,
  FetchTaskDetail,
  FetchTasks,
  OpenTask,
  RestoreScroll,
} from "./commands";
import { Message, OutMessage } from "./message";
import {
  initialModel,
  rowKey,
  rowOf,
  taskItem,
  withoutClosed,
  type Model,
  type TaskLine,
} from "./model";
import { update } from "./update";
import { view } from "./view";

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

const composerMessage = (child: ComposerMessage) => Message.GotComposerMessage({ message: child });

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
      message(Message.ClickedChat({ key, line: task })),
      model((m) => {
        expect(rowOf(m, key).error).toBe("");
      }),
      StoryCommand.resolve(AttachTask, Message.AttachedTask()),
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

describe("the detail view of a task", () => {
  const task = line(41);
  const detail = {
    body: "Issue body",
    pullRequest: 44,
    pullRequestUrl: "https://github.com/owner/shop/pull/44",
  };

  it("opens the detail view, reads the task detail, and resets scroll to 0", () => {
    story(
      update,
      given(createModel({ lines: Option.some([task]) })),
      message(Message.ClickedTask({ line: task })),
      StoryCommand.expectExact(OpenTask({ instance: "side", line: task })),
      StoryCommand.resolve(OpenTask, Message.OpenedTask({ line: task, scroll: 150 })),
      model((m) => {
        expect(m.opened).toEqual(Option.some({ repository: "owner/shop", number: 41 }));
        expect(m.scroll).toBe(150);
        expect(m.detailLoading).toBe(true);
      }),
      StoryCommand.expectExact(
        RestoreScroll({ instance: "side", scroll: 0 }),
        FetchTaskDetail({
          owner: "owner",
          name: "shop",
          workstream: 12,
          repository: "owner/shop",
          number: 41,
        }),
      ),
      StoryCommand.resolve(RestoreScroll, Message.CompletedRestoreScroll()),
      StoryCommand.resolve(
        FetchTaskDetail,
        Message.TaskDetailLoaded({ repository: "owner/shop", number: 41, detail }),
      ),
      model((m) => {
        expect(m.detail).toEqual(Option.some(detail));
        expect(m.detailLoading).toBe(false);
        expect(m.detailError).toBe("");
      }),
    );
  });

  it("shows an error when reading the task detail fails", () => {
    story(
      update,
      given(
        createModel({
          lines: Option.some([task]),
          opened: Option.some({ repository: "owner/shop", number: 41 }),
          detailLoading: true,
        }),
      ),
      message(
        Message.TaskDetailFailed({ repository: "owner/shop", number: 41, error: "Not found" }),
      ),
      model((m) => {
        expect(m.detailLoading).toBe(false);
        expect(m.detailError).toBe("Not found");
      }),
    );
  });

  it("ignores an answer for an earlier task when a different task is opened", () => {
    const task71 = line(71);
    const task72 = line(72);
    const detail71 = {
      body: "Detail 71",
      pullRequest: 710,
      pullRequestUrl: "https://github.com/owner/shop/pull/710",
    };
    const detail72 = {
      body: "Detail 72",
      pullRequest: 720,
      pullRequestUrl: "https://github.com/owner/shop/pull/720",
    };

    story(
      update,
      given(
        createModel({
          lines: Option.some([task71, task72]),
          opened: Option.some({ repository: "owner/shop", number: 72 }),
          detailLoading: true,
        }),
      ),
      message(Message.TaskDetailLoaded({ repository: "owner/shop", number: 71, detail: detail71 })),
      model((m) => {
        expect(Option.isNone(m.detail)).toBe(true);
        expect(m.detailLoading).toBe(true);
      }),
      message(
        Message.TaskDetailFailed({ repository: "owner/shop", number: 71, error: "Not found" }),
      ),
      model((m) => {
        expect(Option.isNone(m.detail)).toBe(true);
        expect(m.detailLoading).toBe(true);
        expect(m.detailError).toBe("");
      }),
      message(Message.TaskDetailLoaded({ repository: "owner/shop", number: 72, detail: detail72 })),
      model((m) => {
        expect(m.detail).toEqual(Option.some(detail72));
        expect(m.detailLoading).toBe(false);
        expect(m.detailError).toBe("");
      }),
    );
  });

  it("ignores an answer when no task is opened", () => {
    story(
      update,
      given(createModel()),
      message(Message.TaskDetailLoaded({ repository: "owner/shop", number: 41, detail })),
      model((m) => {
        expect(Option.isNone(m.detail)).toBe(true);
        expect(m.detailLoading).toBe(false);
      }),
      message(
        Message.TaskDetailFailed({ repository: "owner/shop", number: 41, error: "Not found" }),
      ),
      model((m) => {
        expect(m.detailError).toBe("");
      }),
    );
  });

  it("returns to the task list and restores scroll position on back click", () => {
    story(
      update,
      given(
        createModel({
          lines: Option.some([task]),
          opened: Option.some({ repository: "owner/shop", number: 41 }),
          scroll: 150,
          detail: Option.some(detail),
        }),
      ),
      message(Message.ClickedBack()),
      model((m) => {
        expect(Option.isNone(m.opened)).toBe(true);
        expect(Option.isNone(m.detail)).toBe(true);
      }),
      StoryCommand.expectExact(RestoreScroll({ instance: "side", scroll: 150 })),
      StoryCommand.resolve(RestoreScroll, Message.CompletedRestoreScroll()),
    );
  });

  it("returns to the list when the opened task is no longer in the list", () => {
    const otherTask = line(42);
    story(
      update,
      given(
        createModel({
          lines: Option.some([task]),
          opened: Option.some({ repository: "owner/shop", number: 41 }),
          scroll: 150,
        }),
      ),
      message(Message.TasksLoaded({ lines: [otherTask] })),
      model((m) => {
        expect(Option.isNone(m.opened)).toBe(true);
      }),
      StoryCommand.expectExact(RestoreScroll({ instance: "side", scroll: 150 })),
      StoryCommand.resolve(RestoreScroll, Message.CompletedRestoreScroll()),
    );
  });

  it("shows an error when attaching a task from the detail view fails", () => {
    Scene.scene(
      { update, view },
      Scene.given(
        createModel({
          lines: Option.some([task]),
          opened: Option.some({ repository: "owner/shop", number: 41 }),
        }),
      ),
      Scene.Mount.resolve(ComposerBehavior, ComposerFixture.sendSubmitted),
      Scene.click(Scene.role("button", { name: "Chat" })),
      Scene.Command.resolve(
        AttachTask,
        Message.AttachFailed({ key: rowKey(task), error: "storage is full" }),
      ),
      Scene.expect(Scene.text("storage is full")).toExist(),
    );
  });
});

describe("the selection in the issue body of the detail view", () => {
  const task = line(41);
  const openedModel = (overrides: Partial<Model> = {}) =>
    createModel({
      lines: Option.some([task]),
      opened: Option.some({ repository: "owner/shop", number: 41 }),
      ...overrides,
    });
  const selected = Message.GotSelectionMessage({ message: SelectionFixture.selectedInTask });

  it("puts a selection of the issue body in the draft of the Lead chat", () => {
    story(
      update,
      given(openedModel()),
      message(selected),
      message(Message.GotSelectionMessage({ message: SelectionFixture.addClicked })),
      model((m) => {
        expect(m.selection).toBeNull();
      }),
      StoryCommand.expectExact(
        AddSelection({
          key: "chatDraft:owner/owner/shop/12",
          selection: {
            source: "task",
            text: "free plan",
            context: "One free plan for staff",
            reference: "#41",
            link: "https://github.com/owner/shop/issues/41",
            part: "issue body",
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

  it("names the repository in the reference of a task of another repository", () => {
    const other = line(7, {
      repository: "owner/api",
      otherRepository: true,
      url: "https://github.com/owner/api/issues/7",
    });
    story(
      update,
      given(
        openedModel({
          lines: Option.some([other]),
          opened: Option.some({ repository: "owner/api", number: 7 }),
        }),
      ),
      message(selected),
      message(Message.GotSelectionMessage({ message: SelectionFixture.addClicked })),
      StoryCommand.expectExact(
        AddSelection({
          key: "chatDraft:owner/owner/shop/12",
          selection: {
            source: "task",
            text: "free plan",
            context: "One free plan for staff",
            reference: "owner/api#7",
            link: "https://github.com/owner/api/issues/7",
            part: "issue body",
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

  it("drops the selection when the view closes or another task opens", () => {
    story(
      update,
      given(openedModel()),
      message(selected),
      message(Message.ClickedBack()),
      model((m) => {
        expect(m.selection).toBeNull();
      }),
      StoryCommand.resolve(RestoreScroll, Message.CompletedRestoreScroll()),
      message(Message.OpenedTask({ line: task, scroll: 0 })),
      StoryCommand.resolveAll(
        [RestoreScroll, Message.CompletedRestoreScroll()],
        [
          FetchTaskDetail,
          Message.TaskDetailFailed({ repository: "owner/shop", number: 41, error: "x" }),
        ],
      ),
      message(selected),
      message(Message.OpenedTask({ line: task, scroll: 0 })),
      model((m) => {
        expect(m.selection).toBeNull();
      }),
      StoryCommand.resolveAll(
        [RestoreScroll, Message.CompletedRestoreScroll()],
        [
          FetchTaskDetail,
          Message.TaskDetailFailed({ repository: "owner/shop", number: 41, error: "x" }),
        ],
      ),
    );
  });
});

describe("the composer of the detail view", () => {
  const task = line(41);
  const item = taskItem(task);
  const openedModel = (overrides: Partial<Model> = {}) =>
    createModel({
      lines: Option.some([task]),
      opened: Option.some({ repository: "owner/shop", number: 41 }),
      ...overrides,
    });
  const key = createModel().composer.draftKey;

  it("adds the task to the draft on the first change of the draft, and does not close the sheet", () => {
    story(
      update,
      given(openedModel()),
      message(composerMessage(ComposerFixture.draftTextChanged("H"))),
      model((m) => {
        expect(m.composer.draftText).toBe("H");
        expect(m.composer.draftInboxItems).toEqual([item]);
        expect(m.taskAttached).toBe(true);
      }),
      StoryCommand.expectExact(
        SaveDraftInboxItems({ key, inboxItems: [item] }),
        SaveDraftText({ key, text: "H" }),
      ),
      StoryCommand.resolveAll(
        [SaveDraftInboxItems, ComposerFixture.saved],
        [SaveDraftText, ComposerFixture.saved],
      ),
      expectNoOutMessage(),
    );
  });

  it("adds no second item when the draft has the task", () => {
    const { composer } = openedModel();
    story(
      update,
      given(openedModel({ composer: { ...composer, draftInboxItems: [item] } })),
      message(composerMessage(ComposerFixture.draftTextChanged("H"))),
      model((m) => {
        expect(m.composer.draftInboxItems).toEqual([item]);
        expect(m.taskAttached).toBe(true);
      }),
      StoryCommand.expectExact(SaveDraftText({ key, text: "H" })),
      StoryCommand.resolve(SaveDraftText, ComposerFixture.saved),
    );
  });

  it("adds the task again only after the view opens again", () => {
    story(
      update,
      given(openedModel()),
      message(composerMessage(ComposerFixture.draftTextChanged("H"))),
      StoryCommand.resolveAll(
        [SaveDraftInboxItems, ComposerFixture.saved],
        [SaveDraftText, ComposerFixture.saved],
      ),
      message(composerMessage(ComposerFixture.inboxItemRemoved(item.id))),
      StoryCommand.resolve(SaveDraftInboxItems, ComposerFixture.saved),
      message(composerMessage(ComposerFixture.draftTextChanged("Hi"))),
      model((m) => {
        expect(m.composer.draftInboxItems).toEqual([]);
      }),
      StoryCommand.expectExact(SaveDraftText({ key, text: "Hi" })),
      StoryCommand.resolve(SaveDraftText, ComposerFixture.saved),
      message(Message.OpenedTask({ line: task, scroll: 0 })),
      StoryCommand.resolveAll(
        [RestoreScroll, Message.CompletedRestoreScroll()],
        [
          FetchTaskDetail,
          Message.TaskDetailFailed({ repository: "owner/shop", number: 41, error: "x" }),
        ],
      ),
      message(composerMessage(ComposerFixture.draftTextChanged("Hii"))),
      model((m) => {
        expect(m.composer.draftInboxItems).toEqual([item]);
      }),
      StoryCommand.resolveAll(
        [SaveDraftInboxItems, ComposerFixture.saved],
        [SaveDraftText, ComposerFixture.saved],
      ),
    );
  });

  it("queues a sent message for the chat and keeps the view open", () => {
    const { composer } = openedModel();
    const queued = {
      id: "queued-1",
      organization: "owner",
      repository: "owner/shop",
      workstream: 12,
      text: "Hello",
      images: [],
      inboxItems: [],
      selections: [],
      voice: false,
      time: "2026-11-14T08:00:00.000Z",
    };
    story(
      update,
      given(openedModel({ composer: { ...composer, draftText: "Hello", draftLoaded: true } })),
      message(composerMessage(ComposerFixture.sendSubmitted)),
      model((m) => {
        expect(m.composer.draftText).toBe("");
        expect(Option.isSome(m.opened)).toBe(true);
      }),
      StoryCommand.resolve(EnqueueMessage, ComposerFixture.queued(queued)),
      model((m) => {
        expect(Option.isSome(m.opened)).toBe(true);
      }),
      expectNoOutMessage(),
    );
  });
});
