import { Match } from "effect";
import type { HtmlBuilder, Html } from "foldkit/html";
import { cn } from "cn";
import * as Composer from "@/composer";
import { InboxItemKind } from "@/api/api.gen";
import { roleName } from "@/lib/actions";
import { answerDraftKey } from "@/lib/draft";
import { buttonClass } from "@/ui/button";
import { arrowUpIcon, banIcon, checkIcon } from "@/ui/icons";
import { Message } from "./message";
import { hasNote, type Model } from "./model";
import type { InboxItem } from "./schemas";

type H = HtmlBuilder<Message>;

export const answerInputId = "answer-input";

// The Mounts of the shared composer send only these Messages to the chat: the answer panel has no picker and no image.
export const fromComposerMount = (message: Composer.Message): Message =>
  Match.value(message).pipe(
    Match.tag("SendSubmitted", () => Message.AnswerSubmitted()),
    Match.tag("CompletedScrollLive", () => Message.CompletedScrollLive()),
    Match.orElse(() => Message.CompletedShowFile()),
  );

// The field of the answer panel. A pending action has the buttons Reject and Approve, and each other item has the
// Send button.
export function renderPanelComposer(h: H, model: Model, item: InboxItem): Html {
  const action = item.action;
  const note = hasNote(item);
  const selected = Object.values(model.selectedOptions).some((options) => options.length > 0);
  const decide = Boolean(action);
  const busy = model.requesting.includes(item.id);
  const holdsVoice = Composer.voiceBusy(model.answerVoice);
  const empty = !model.answerText.trim() && !(note ? true : !action && selected);
  const cannotSend = empty || holdsVoice;
  const role = action ? (roleName[action.role] ?? action.role) : "";
  const label = action ? `Note for the ${role}` : note ? "Note" : "Your answer";
  const sendLabel = item.kind === InboxItemKind.stopped ? "Resume" : "Send";
  const placeholder = action
    ? `Add a note for the ${role} (optional)`
    : note
      ? "Note (optional)"
      : item.questions.length > 0
        ? "Add a note (optional)"
        : "Write your answer";

  return Composer.composerBox(
    h,
    model.voice,
    model.now,
    {
      field: answerInputId,
      picker: Composer.pickerId("answer"),
      key: answerDraftKey(item.id),
      voiceCard: model.answerVoice,
      label,
      placeholder,
      text: model.answerText,
      readOnly: !model.answerLoaded || busy,
      onInput: (text) => Message.AnswerTextChanged({ text }),
      submit: decide ? null : Message.AnswerSubmitted(),
      enter: decide ? "none" : "send",
      autoFocus: false,
      images: null,
      inboxItems: [],
      selections: [],
      onVoiceButtonClick: Message.VoiceButtonClicked(),
      onVoiceResendClick: Message.VoiceResendClicked(),
      onVoiceRemoveClick: Message.VoiceRemoveClicked(),
      actions: decide
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
            h.button(
              [
                h.Type("submit"),
                h.AriaLabel(sendLabel),
                h.Attribute("title", sendLabel),
                h.DataAttribute("keep-focus", ""),
                h.Disabled(busy || cannotSend),
                ...(holdsVoice ? [h.DataAttribute("voice-busy", "")] : []),
                h.Class(
                  cn(
                    buttonClass({ size: "icon", className: "rounded-full max-md:size-11" }),
                    cannotSend &&
                      "bg-muted text-muted-foreground hover:bg-muted disabled:opacity-100",
                  ),
                ),
              ],
              [arrowUpIcon(h)],
            ),
          ],
      error: model.answerErrorId === item.id ? model.answerError : "",
    },
    fromComposerMount,
  );
}
