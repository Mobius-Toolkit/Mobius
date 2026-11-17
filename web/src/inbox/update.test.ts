import { Option } from "effect";
import { describe, expect, it, vi } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import {
  AnswerInboxItem,
  OpenChatWithItem,
  ResumeIssue,
  RunTrayAction,
  SaveDraftText,
} from "@/chat/commands";
import { Message, toGotWorkstreamSetupMessage } from "@/chat/message";
import { initialModel, type Model } from "@/chat/model";
import type { InboxItem } from "@/chat/schemas";
import { update } from "@/chat/update";
import { CreateWorkstream, FetchCheckup, SaveIssueRepository } from "@/workstreamSetup/commands";
import { Message as SetupMessage } from "@/workstreamSetup/message";
import { skillsWorkstreamRequest } from "@/workstreamSetup/requests";
import type { Checkup } from "@/workstreamSetup/schemas";

// The voice library reads the document of the browser when it loads.
vi.mock("@/lib/voice", () => ({ resend: vi.fn(), stopVoice: vi.fn(), toggleVoice: vi.fn() }));

const createItem = (overrides: Partial<InboxItem> = {}): InboxItem => ({
  id: 1,
  kind: "stopped",
  action: null,
  answer: "",
  blockingTodos: [],
  dismissedAt: null,
  issue: 42,
  link: "https://github.com/owner/shop/issues/42",
  mergeRefusal: "",
  merging: false,
  organization: "owner",
  pullRequest: 0,
  pullRequestUrl: "",
  questions: [],
  reason: "The session hung",
  reportUrl: "",
  repository: "owner/shop",
  text: "",
  time: "2026-11-08T08:00:00Z",
  title: "Let customers change plans",
  todo: null,
  withdrawn: false,
  workstream: 12,
  workstreamRepository: "owner/shop",
  mergeQueuePlace: null,
  mergeQueueSize: 0,
  ...overrides,
});

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ organization: "owner", repository: "", workstream: 0, agent: "Coordinator" }),
  ...overrides,
});

describe("inbox update story tests", () => {
  describe("Chat button", () => {
    it("opens the chat of the Workstream of the item", () => {
      const item = createItem();

      story(
        update,
        given(createModel({ trayItems: [item] })),
        message(Message.ChatWithItemClicked({ item })),
        StoryCommand.expectExact(OpenChatWithItem({ item, organization: "owner" })),
        StoryCommand.resolve(OpenChatWithItem, Message.CompletedOpenChat()),
        StoryCommand.expectNone(),
      );
    });

    it("opens the Coordinator chat of the organization of an item with no Workstream", () => {
      const item = createItem({ workstream: 0, workstreamRepository: "", organization: "plants" });

      story(
        update,
        given(createModel({ trayItems: [item] })),
        message(Message.ChatWithItemClicked({ item })),
        StoryCommand.expectExact(OpenChatWithItem({ item, organization: "owner" })),
        StoryCommand.resolve(OpenChatWithItem, Message.CompletedOpenChat()),
        StoryCommand.expectNone(),
      );
    });
  });

  describe("Resume and Start again", () => {
    it("names the issue against the Workstream of the item, and not against the Inbox", () => {
      const item = createItem();

      story(
        update,
        given(createModel({ trayItems: [item] })),
        message(Message.ResumeClicked({ item })),
        model((m) => {
          expect(m.trayRunning).toEqual({ 1: "action" });
        }),
        StoryCommand.expectExact(
          ResumeIssue({
            id: 1,
            repository: "owner/shop",
            issue: 42,
            workstreamRepository: "owner/shop",
          }),
        ),
        StoryCommand.resolve(ResumeIssue, Message.ResumeCompleted({ id: 1, error: undefined })),
        StoryCommand.expectNone(),
      );
    });

    it("ignores a second tap while the request runs", () => {
      const item = createItem();

      story(
        update,
        given(createModel({ trayItems: [item], trayRunning: { 1: "action" } })),
        message(Message.ResumeClicked({ item })),
        StoryCommand.expectNone(),
      );
    });

    it("removes the row after a success", () => {
      const item = createItem();
      const other = createItem({ id: 2 });

      story(
        update,
        given(createModel({ trayItems: [item, other], trayRunning: { 1: "action" } })),
        message(Message.ResumeCompleted({ id: 1, error: undefined })),
        model((m) => {
          expect(m.trayItems).toEqual([other]);
          expect(m.trayError).toBe("");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the row, shows the error and ends the pending state after a failure", () => {
      const item = createItem();

      story(
        update,
        given(createModel({ trayItems: [item], trayRunning: { 1: "action" } })),
        message(Message.ResumeCompleted({ id: 1, error: "The server failed." })),
        model((m) => {
          expect(m.trayItems).toEqual([item]);
          expect(m.trayRunning).toEqual({});
          expect(m.trayError).toBe("The server failed.");
        }),
        StoryCommand.expectNone(),
      );
    });
  });

  describe("Dismiss", () => {
    it("keeps the pending state after a success, and the next tap on another row runs", () => {
      const item = createItem({ kind: "tool upgrade", issue: 0 });
      const other = createItem({ id: 2, kind: "Lead failed" });

      story(
        update,
        given(createModel({ trayItems: [item, other] })),
        message(Message.TrayActionClicked({ item, kind: "dismiss" })),
        StoryCommand.expectExact(RunTrayAction({ item, kind: "dismiss" })),
        StoryCommand.resolve(
          RunTrayAction,
          Message.TrayActionDone({ id: 1, kind: "dismiss", error: "", merging: false }),
        ),
        model((m) => {
          expect(m.trayRunning).toEqual({ 1: "dismiss" });
          expect(m.trayError).toBe("");
        }),
        message(Message.TrayActionClicked({ item: other, kind: "dismiss" })),
        StoryCommand.expectExact(RunTrayAction({ item: other, kind: "dismiss" })),
        StoryCommand.resolve(
          RunTrayAction,
          Message.TrayActionDone({ id: 2, kind: "dismiss", error: "", merging: false }),
        ),
        model((m) => {
          expect(m.trayRunning).toEqual({ 1: "dismiss", 2: "dismiss" });
        }),
        StoryCommand.expectNone(),
      );
    });

    it("ends the pending state and shows the error after a failure", () => {
      const item = createItem({ kind: "tool upgrade", issue: 0 });

      story(
        update,
        given(createModel({ trayItems: [item] })),
        message(Message.TrayActionClicked({ item, kind: "dismiss" })),
        StoryCommand.expectExact(RunTrayAction({ item, kind: "dismiss" })),
        StoryCommand.resolve(
          RunTrayAction,
          Message.TrayActionDone({ id: 1, kind: "dismiss", error: "No.", merging: false }),
        ),
        model((m) => {
          expect(m.trayRunning).toEqual({});
          expect(m.trayError).toBe("No.");
          expect(m.trayItems).toEqual([item]);
        }),
        StoryCommand.expectNone(),
      );
    });
  });

  describe("answer", () => {
    const question = createItem({ id: 7, kind: "question", issue: 45 });

    it("sends the answer to the repository and the Workstream of the item", () => {
      story(
        update,
        given(createModel({ trayItems: [question] })),
        message(Message.OpenAnswerPanel({ item: question })),
        message(
          Message.ReceivedAnswerDraft({
            id: 7,
            draft: { text: "", images: [], inboxItems: [], selections: [], voice: null },
          }),
        ),
        message(Message.AnswerTextChanged({ text: "Yes" })),
        StoryCommand.resolve(SaveDraftText, Message.CompletedSaveDraft()),
        message(Message.AnswerSubmitted()),
        StoryCommand.expectExact(
          AnswerInboxItem({
            id: 7,
            repository: "owner/shop",
            issue: 45,
            answer: "Yes",
            notice: "Sent the answer to the Lead of #12",
            voice: false,
          }),
        ),
        StoryCommand.resolve(AnswerInboxItem, Message.AnswerCompleted({ id: 7, error: "" })),
        StoryCommand.expectNone(),
      );
    });

    it("opens the panel again with the error and keeps the row after a failed answer", () => {
      story(
        update,
        given(createModel({ trayItems: [question] })),
        message(Message.OpenAnswerPanel({ item: question })),
        message(Message.AnswerTextChanged({ text: "Yes" })),
        StoryCommand.resolve(SaveDraftText, Message.CompletedSaveDraft()),
        message(Message.AnswerSubmitted()),
        model((m) => {
          expect(m.requesting).toEqual([7]);
        }),
        StoryCommand.resolve(
          AnswerInboxItem,
          Message.AnswerCompleted({ id: 7, error: "The server failed." }),
        ),
        model((m) => {
          expect(m.activePanel?.item.id).toBe(7);
          expect(m.answerError).toBe("The server failed.");
          expect(m.requesting).toEqual([]);
          expect(m.trayItems).toEqual([question]);
        }),
        StoryCommand.expectNone(),
      );
    });
  });
});

const checkup = (overrides: Partial<Checkup> = {}): Checkup => ({
  issueRepository: "owner/shop",
  issueRepositoryError: "",
  issueRepositorySource: "choice",
  labelFix: "none",
  maintenanceWorkstream: 0,
  permissions: [],
  permissionsError: "",
  repositories: [
    { repository: "owner/shop", labels: [] },
    { repository: "owner/shed", labels: [] },
  ],
  skillRepository: "owner/mobius-skills",
  skillsWorkstream: 0,
  skillsWorkstreamRepository: "",
  ...overrides,
});
const noIssueRepository = () =>
  checkup({ issueRepository: "", issueRepositorySource: "none", issueRepositoryError: "None." });
const loaded = (found: Checkup): Model => {
  const base = createModel();
  return {
    ...base,
    workstreamSetup: { ...base.workstreamSetup, checkup: Option.some(found) },
  };
};

describe("the Workstream setup in the row of a missing Workstream", () => {
  const got = toGotWorkstreamSetupMessage;

  it("keeps the checkup that the Submodel reads", () => {
    story(
      update,
      given(createModel()),
      message(got(SetupMessage.CheckupLoaded({ checkup: checkup() }))),
      model((m) => {
        expect(m.workstreamSetup.checkup).toEqual(Option.some(checkup()));
      }),
    );
  });

  it("opens the picker, saves the choice and reads the checkup again", () => {
    story(
      update,
      given(loaded(noIssueRepository())),
      message(got(SetupMessage.OpenedPicker())),
      model((m) => {
        expect(m.workstreamSetup.picker).toEqual(
          Option.some({ selected: "", error: "", saving: false }),
        );
      }),
      message(got(SetupMessage.SelectedRepository({ repository: "owner/shed" }))),
      message(got(SetupMessage.ClickedSave())),
      model((m) => {
        expect(m.workstreamSetup.picker).toEqual(
          Option.some({ selected: "owner/shed", error: "", saving: true }),
        );
      }),
      StoryCommand.expectExact(
        SaveIssueRepository({ organization: "owner", repository: "owner/shed" }),
      ),
      StoryCommand.resolve(SaveIssueRepository, SetupMessage.Saved()),
      model((m) => {
        expect(Option.isNone(m.workstreamSetup.picker)).toBe(true);
      }),
      StoryCommand.expectExact(FetchCheckup({ organization: "owner" })),
      StoryCommand.resolve(
        FetchCheckup,
        SetupMessage.CheckupLoaded({ checkup: checkup({ issueRepository: "owner/shed" }) }),
      ),
      model((m) => {
        expect(Option.map(m.workstreamSetup.checkup, (found) => found.issueRepository)).toEqual(
          Option.some("owner/shed"),
        );
      }),
    );
  });

  it("sends the request of the Skills Workstream with the Create button", () => {
    story(
      update,
      given(loaded(checkup())),
      message(got(SetupMessage.ClickedCreate({ kind: "skills" }))),
      model((m) => {
        expect(m.workstreamSetup.creating).toEqual(Option.some("skills"));
      }),
      StoryCommand.expectExact(
        CreateWorkstream({
          organization: "owner",
          request: skillsWorkstreamRequest("owner/shop", "owner/mobius-skills"),
        }),
      ),
      StoryCommand.resolve(
        CreateWorkstream,
        SetupMessage.WorkstreamRequested({ organization: "owner" }),
      ),
      StoryCommand.expectNone(),
    );
  });

  it("sends no request while the organization has no issue repository", () => {
    story(
      update,
      given(loaded(noIssueRepository())),
      message(got(SetupMessage.ClickedCreate({ kind: "maintenance" }))),
      StoryCommand.expectNone(),
    );
  });
});
