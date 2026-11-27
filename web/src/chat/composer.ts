import type { HtmlBuilder, Html } from "foldkit/html";
import * as Composer from "@/composer";
import { InboxItemKind } from "@/api/api.gen";
import { roleName } from "@/lib/actions";
import { answerDraftKey } from "@/lib/draft";
import { buttonClass } from "@/ui/button";
import { arrowUpIcon, banIcon, checkIcon, spinnerIcon } from "@/ui/icons";
import { Message, toGotAnswerComposerMessage } from "./message";
import { hasNote, type Model } from "./model";
import type { InboxItem } from "./schemas";

type H = HtmlBuilder<Message>;

function fieldTexts(item: InboxItem) {
  const action = item.action;
  const note = hasNote(item);
  const role = action ? (roleName[action.role] ?? action.role) : "";
  return {
    label: action ? `Note for the ${role}` : note ? "Note" : "Your answer",
    placeholder: action
      ? `Add a note for the ${role} (optional)`
      : note
        ? "Note (optional)"
        : item.questions.length > 0
          ? "Add a note (optional)"
          : "Write your answer",
  };
}

// The composer of the answer panel of an item. It shows the draft of the item and sends it with the request of the
// chat.
export function newAnswerComposer(model: Model, item: InboxItem, error = ""): Composer.Model {
  return {
    ...Composer.initialModel({
      draftKey: answerDraftKey(item.id),
      instance: "answer",
      sends: "parent",
      ...fieldTexts(item),
      organization: model.organization,
      repository: model.repository,
      workstream: model.workstream,
    }),
    sendError: error,
  };
}

// The field of the answer panel. A pending action has the buttons Reject and Approve, and each other item has the
// Send button.
export function renderPanelComposer(
  h: H,
  model: Model,
  composer: Composer.Model,
  item: InboxItem,
  timeOf: (messageId: number) => string | undefined,
): Html {
  const action = item.action;
  const note = hasNote(item);
  const selected = Object.values(model.selectedOptions).some((options) => options.length > 0);
  const decide = Boolean(action);
  const running = model.trayRunning[item.id];
  const busy = model.requesting.includes(item.id) || running !== undefined;
  const holdsVoice = Composer.voiceBusy(composer.draftVoice);
  const empty = Composer.isEmptyDraft(composer) && !(note ? true : !action && selected);
  const cannotSend = empty || holdsVoice;
  const sendLabel = item.kind === InboxItemKind.stopped ? "Resume" : "Send";

  const actions = () =>
    decide
      ? [
          h.button(
            [
              h.Type("button"),
              h.AriaLabel("Reject"),
              h.Attribute("title", "Reject"),
              h.DataAttribute("keep-focus", ""),
              h.Disabled(busy || holdsVoice),
              h.Class(
                buttonClass({
                  variant: "ghost",
                  size: "icon",
                  className: "rounded-full text-muted-foreground max-md:size-11",
                }),
              ),
              h.OnClick(Message.ActionDecided({ decision: "reject" })),
            ],
            [banIcon(h, "size-4")],
          ),
          h.button(
            [
              h.Type("button"),
              h.DataAttribute("keep-focus", ""),
              h.Disabled(busy || holdsVoice),
              h.Class(buttonClass({ className: "rounded-full max-md:h-11 max-md:px-4" })),
              h.OnClick(Message.ActionDecided({ decision: "approve" })),
            ],
            [checkIcon(h, "size-4"), "Approve"],
          ),
        ]
      : [
          ...(item.todo?.dismissable
            ? [
                h.button(
                  [
                    h.Type("button"),
                    h.DataAttribute("keep-focus", ""),
                    h.Disabled(busy || holdsVoice),
                    h.Class(
                      buttonClass({
                        variant: "outline",
                        className: "rounded-full max-md:h-11 max-md:px-4",
                      }),
                    ),
                    h.OnClick(Message.TrayActionClicked({ item, kind: "dismiss" })),
                  ],
                  [...(running === "dismiss" ? [spinnerIcon(h)] : []), "Dismiss"],
                ),
              ]
            : []),
          Composer.sendButton(h, {
            label: sendLabel,
            disabled: busy || cannotSend,
            muted: cannotSend,
            voiceBusy: holdsVoice,
            content: [arrowUpIcon(h)],
          }),
        ];

  return h.submodel({
    slotId: "answer-composer",
    model: composer,
    view: Composer.view,
    viewInputs: {
      stoppable: false,
      stopping: false,
      stopReady: false,
      locked: busy,
      autoFocus: false,
      selectionTime: timeOf,
      actions,
    },
    toParentMessage: toGotAnswerComposerMessage,
  });
}
