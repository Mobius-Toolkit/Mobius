import { Mount } from "foldkit";
import * as Scene from "foldkit/scene";
import { describe, it, vi } from "vitest";
import * as Selection from "@/ui/selection";
import { Message, toGotSelectionMessage } from "./message";
import { initialModel, type Model } from "./model";
import type { InboxItem, PendingAction } from "./schemas";
import { update } from "./update";
import { PanelOpened, renderAnswerDialog } from "./view";

// The voice library reads the document of the browser when it loads.
vi.mock("@/lib/voice", () => ({ resend: vi.fn(), stopVoice: vi.fn(), toggleVoice: vi.fn() }));

const action = (overrides: Partial<PendingAction>): PendingAction => ({
  id: 7,
  role: "lead",
  tool: "update_recurring_task",
  arguments: [
    { name: "id", value: "3", labels: [] },
    { name: "text", value: "Check the invoices.", labels: [] },
    { name: "cron", value: "0 10 * * 1-5", labels: [] },
  ],
  state: "pending",
  error: "",
  result: "",
  message: "",
  oldBrief: "",
  oldRecurringText: "",
  oldRecurringCron: "",
  sender: null,
  time: "2026-11-28T10:00:00Z",
  decidedAt: null,
  ...overrides,
});

const deleteAction = (overrides: Partial<PendingAction> = {}) =>
  action({
    tool: "delete_recurring_task",
    arguments: [{ name: "id", value: "3", labels: [] }],
    ...overrides,
  });

const inboxItem = (pending: PendingAction): InboxItem => ({
  id: 1,
  kind: "approval",
  action: pending,
  answer: "",
  blockingTodos: [],
  dismissedAt: null,
  questions: [],
  title: "Change the recurring task 3",
  reason: "",
  text: "",
  time: "2026-11-28T10:00:00Z",
  organization: "owner",
  repository: "owner/shop",
  workstreamRepository: "owner/shop",
  workstream: 14,
  issue: 0,
  mergeRefusal: "",
  merging: false,
  mergeQueuePlace: null,
  mergeQueueSize: 0,
  todo: null,
  withdrawn: false,
  link: "",
  pullRequest: 0,
  pullRequestUrl: "",
  reportUrl: "",
});

type Step = ReturnType<ReturnType<typeof Scene.expect>["toExist"]>;

// Each argument that the sheet shows has one selection watch.
const watches = (names: readonly string[]) =>
  names.map((name) =>
    Scene.Mount.resolve(
      Mount.mapMessage(Selection.Watch({ owner: `dialog:${name}` }), toGotSelectionMessage),
      Message.CompletedFocusPanel(),
    ),
  );

const show = (
  pending: PendingAction,
  overrides: Partial<Model>,
  shown: readonly string[],
  ...steps: ReadonlyArray<Step>
) =>
  Scene.scene(
    {
      update,
      view: (model: Model, h: Parameters<typeof renderAnswerDialog>[0]) =>
        renderAnswerDialog(h, model),
    },
    Scene.given({
      ...initialModel({
        organization: "owner",
        repository: "owner/shop",
        workstream: 14,
        agent: "lead",
      }),
      activePanel: {
        type: "details",
        item: inboxItem(pending),
        workstreamTitle: "Shop",
        fromTray: false,
      },
      ...overrides,
    }),
    Scene.Mount.resolve(PanelOpened, Message.CompletedFocusPanel()),
    ...watches(shown),
    ...steps,
  );

const inserted = Scene.selector("ins");
const removed = Scene.selector("del");
const exact = (text: string) => Scene.text(text, { exact: true });

const plans = { recurringActionText: "Check the plans.", recurringActionCron: "0 8 * * 1-5" };
const oldPlans = { oldRecurringText: "Check the plans.", oldRecurringCron: "0 8 * * 1-5" };
const textAndCron = ["text", "cron"];

describe("the sheet of a recurring task action", () => {
  it("shows no id for an update", () => {
    show(
      action({}),
      {},
      textAndCron,
      Scene.expect(exact("id")).toBeAbsent(),
      Scene.expect(exact("text")).toExist(),
      Scene.expect(exact("cron")).toExist(),
    );
  });

  it("shows no id for a delete", () => {
    show(
      deleteAction(),
      {},
      [],
      Scene.expect(exact("id")).toBeAbsent(),
      Scene.expect(exact("3")).toBeAbsent(),
    );
  });

  it("shows the diff of the text and the cron of a pending update against the current task", () => {
    show(
      action({}),
      { ...plans, showRemovedText: true },
      textAndCron,
      Scene.expect(removed).toExist(),
      Scene.expect(inserted).toExist(),
      Scene.expect(exact("plans")).toExist(),
      Scene.expect(exact("invoices")).toExist(),
      Scene.expect(exact("8")).toExist(),
      Scene.expect(exact("10")).toExist(),
    );
  });

  it("hides the removed text of the diff until the switch is on", () => {
    show(
      action({}),
      plans,
      textAndCron,
      Scene.expect(removed).toBeAbsent(),
      Scene.expect(inserted).toExist(),
      Scene.expect(Scene.role("switch", { name: "Show removed text" })).toExist(),
    );
  });

  it("shows the diff of a done update against the stored old values", () => {
    show(
      action({ state: "done", ...oldPlans }),
      { showRemovedText: true },
      [...textAndCron, "result"],
      Scene.expect(exact("plans")).toExist(),
      Scene.expect(exact("invoices")).toExist(),
      Scene.expect(exact("8")).toExist(),
      Scene.expect(exact("10")).toExist(),
    );
  });

  it("shows the plain values of an update when the old values are not known", () => {
    show(
      action({}),
      {},
      textAndCron,
      Scene.expect(inserted).toBeAbsent(),
      Scene.expect(exact("Check the invoices.")).toExist(),
      Scene.expect(exact("0 10 * * 1-5")).toExist(),
    );
  });

  it("shows the text and the cron of the task that a pending delete removes", () => {
    show(
      deleteAction(),
      plans,
      textAndCron,
      Scene.expect(exact("Check the plans.")).toExist(),
      Scene.expect(exact("0 8 * * 1-5")).toExist(),
    );
  });

  it("shows the stored text and cron of the task that a done delete removed", () => {
    show(
      deleteAction({ state: "done", ...oldPlans }),
      {},
      [...textAndCron, "result"],
      Scene.expect(exact("Check the plans.")).toExist(),
      Scene.expect(exact("0 8 * * 1-5")).toExist(),
    );
  });
});
