import { Effect, Queue, Schema, Stream } from "effect";
import type { Attribute, Html, HtmlBuilder } from "foldkit/html";
import { Mount } from "foldkit";
import { cn } from "cn";
import { roleName } from "@/lib/actions";
import { answerDraftKey, chatDraftKey } from "@/lib/draft";
import { voiceSupported } from "@/lib/voice";
import { buttonClass } from "@/ui/button";
import {
  arrowUpIcon,
  banIcon,
  checkIcon,
  micIcon,
  paperclipIcon,
  spinnerIcon,
  squareIcon,
  xIcon,
} from "@/ui/icons";
import { textareaClass } from "@/ui/textarea";
import { Message } from "./message";
import { type Model, voiceBusy } from "./model";
import type { DraftInboxItem, DraftVoice, InboxItem } from "./schemas";

type H = HtmlBuilder<Message>;
type ComposerMessage = ReturnType<
  typeof Message.SendSubmitted | typeof Message.AnswerSubmitted | typeof Message.ImagesChosen
>;

export const inputId = "chat-input";
export const answerInputId = "answer-input";
const pickerId = "chat-attach";
// The Mount of an image reads its file only at insert, so each file of a list needs its own key.
function fileKey(files: File[], index: number) {
  const file = files[index];
  const same = files
    .slice(0, index)
    .filter(
      (other) =>
        other.name === file.name &&
        other.size === file.size &&
        other.lastModified === file.lastModified,
    ).length;
  return `file-${file.name}-${file.size}-${file.lastModified}-${same}`;
}

const ShowFile = Mount.define("ShowFile", {
  args: { file: Schema.instanceOf(File) },
  messages: [Message.CompletedShowFile],
  execute: ({ element, file }) =>
    Effect.gen(function* () {
      yield* Effect.acquireRelease(
        Effect.sync(() => {
          const url = URL.createObjectURL(file);
          if (element instanceof HTMLImageElement) {
            element.src = url;
          }
          return url;
        }),
        (url) => Effect.sync(() => URL.revokeObjectURL(url)),
      );
      return Message.CompletedShowFile();
    }),
});

export function renderFileImage(
  h: H,
  files: File[],
  index: number,
  attributes: ReadonlyArray<Attribute<Message>>,
): Html {
  return h.img([
    h.Key(fileKey(files, index)),
    h.OnMount(ShowFile({ file: files[index] })),
    ...attributes,
  ]);
}

// A button that takes the focus closes the keyboard of a phone, and the button moves before the click.
function keepFocus(event: Event) {
  if (event.target instanceof Element && event.target.closest("[data-keep-focus]")) {
    event.preventDefault();
  }
}

// One Mount owns the behavior that needs the DOM event: Enter, a paste with a picture, a blur on iOS, the focus at
// the start, and a mouse down on a button that must not take the focus from the field.
export const ComposerBehavior = Mount.defineStream("ComposerBehavior", {
  args: {
    autoFocus: Schema.Boolean,
    enter: Schema.Literals(["send", "answer", "none"]),
    field: Schema.String,
  },
  messages: [Message.SendSubmitted, Message.AnswerSubmitted, Message.ImagesChosen],
  execute: ({ element, autoFocus, enter, field: fieldId }) =>
    Stream.callback<ComposerMessage>((queue) =>
      Effect.gen(function* () {
        const field = () => element.querySelector<HTMLTextAreaElement>(`#${fieldId}`);
        const keydown = (key: Event) => {
          if (!(key instanceof KeyboardEvent)) {
            return;
          }
          // On a touch screen, Enter adds a line and only the Send button sends. Safari gives the Enter that ends
          // an IME composition with isComposing false and keyCode 229.
          if (
            key.target === field() &&
            key.key === "Enter" &&
            !key.shiftKey &&
            !key.isComposing &&
            key.keyCode !== 229 &&
            !window.matchMedia("(pointer: coarse)").matches
          ) {
            key.preventDefault();
            if (enter === "send") {
              Queue.offerUnsafe(queue, Message.SendSubmitted());
            } else if (enter === "answer") {
              Queue.offerUnsafe(queue, Message.AnswerSubmitted());
            }
          }
        };
        const paste = (event: Event) => {
          const data = event instanceof ClipboardEvent ? event.clipboardData : null;
          if (!data || event.target !== field() || !element.querySelector(`#${pickerId}`)) {
            return;
          }
          const pasted = [...data.files].filter((file) => file.type.startsWith("image/"));
          // Spreadsheet and word processor apps put the text and a picture of the selection on the clipboard.
          if (pasted.length > 0 && !data.getData("text/plain")) {
            event.preventDefault();
            Queue.offerUnsafe(queue, Message.ImagesChosen({ files: pasted }));
          }
        };
        // Safari on iOS scrolls the page when the keyboard opens, and it can keep that offset after the keyboard closes.
        const blur = (event: Event) => {
          if (event.target === field()) {
            window.scrollTo(0, 0);
          }
        };
        // Foldkit renders in the next frame, so a tap right after a key sees the Send button of the empty draft.
        // The button is enabled at once.
        const typed = (event: Event) => {
          const target = event.target;
          const submit = element.querySelector<HTMLButtonElement>("button[type=submit]");
          if (
            target instanceof HTMLTextAreaElement &&
            target === field() &&
            submit &&
            target.value.trim()
          ) {
            submit.disabled = false;
          }
        };
        element.addEventListener("input", typed);
        element.addEventListener("keydown", keydown);
        element.addEventListener("paste", paste);
        element.addEventListener("focusout", blur);
        element.addEventListener("mousedown", keepFocus);
        if (autoFocus && window.matchMedia("(pointer: fine)").matches) {
          field()?.focus();
        }
        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            element.removeEventListener("input", typed);
            element.removeEventListener("keydown", keydown);
            element.removeEventListener("paste", paste);
            element.removeEventListener("focusout", blur);
            element.removeEventListener("mousedown", keepFocus);
          }),
        );
      }),
    ),
});

function attachedInboxItem(h: H, item: DraftInboxItem): Html {
  const name = item.reference || item.title;
  return h.div(
    [
      h.Key(`inbox-${item.id}`),
      h.Class(
        "grid min-w-0 gap-0.5 rounded-lg border bg-card px-2 py-1.5 text-sm relative w-56 max-w-full pr-9",
      ),
    ],
    [
      item.reference
        ? h.span([h.Class("truncate text-xs text-muted-foreground")], [item.reference])
        : h.empty,
      h.span([h.Class("truncate font-medium")], [item.title]),
      h.span([h.Class("truncate text-xs text-muted-foreground")], [item.reason ?? ""]),
      h.button(
        [
          h.Type("button"),
          h.AriaLabel(`Remove ${name}`),
          h.Attribute("title", `Remove ${name}`),
          h.DataAttribute("keep-focus", ""),
          h.Class(
            buttonClass({
              variant: "secondary",
              size: "icon-sm",
              className: "absolute top-0.5 right-0.5 rounded-full max-md:size-8",
            }),
          ),
          h.OnClick(Message.DraftInboxItemRemoved({ id: item.id })),
        ],
        [xIcon(h)],
      ),
    ],
  );
}

function thumbnail(h: H, files: File[], index: number): Html {
  return h.div(
    [h.Key(`thumb-${fileKey(files, index)}`), h.Class("relative")],
    [
      renderFileImage(h, files, index, [
        h.Alt(`Image ${index + 1}`),
        h.Class("size-16 rounded-lg border object-cover"),
      ]),
      h.button(
        [
          h.Type("button"),
          h.AriaLabel(`Remove image ${index + 1}`),
          h.Attribute("title", `Remove image ${index + 1}`),
          h.DataAttribute("keep-focus", ""),
          h.Class(
            buttonClass({
              variant: "secondary",
              size: "icon-sm",
              className: "absolute top-0.5 right-0.5 rounded-full max-md:size-8",
            }),
          ),
          h.OnClick(Message.DraftImageRemoved({ position: index })),
        ],
        [xIcon(h)],
      ),
    ],
  );
}

type Box = {
  field: string;
  target: "chat" | "answer";
  key: string;
  voiceCard: DraftVoice | null;
  label: string;
  placeholder: string;
  text: string;
  readOnly: boolean;
  onInput: (text: string) => Message;
  submit: Message | null;
  enter: "send" | "answer" | "none";
  autoFocus: boolean;
  images: File[] | null;
  inboxItems: DraftInboxItem[];
  actions: Html[];
  error: string;
};

const ScrollToEnd = Mount.define("ScrollToEnd", {
  messages: [Message.CompletedScrollLive],
  execute: ({ element }) =>
    Effect.sync(() => {
      element.scrollTop = element.scrollHeight;
      return Message.CompletedScrollLive();
    }),
});

function elapsed(model: Model) {
  const seconds = Math.max(0, Math.floor((model.now - model.voice.startedAt) / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

// The card of the voice in the draft of a field: the recording with its live text, the conversion, the failure, or the
// converted voice.
function voiceCard(h: H, model: Model, box: Box): Html {
  const recording = model.voice.recording && model.voice.key === box.key;
  const card = box.voiceCard;
  if (!recording && !card) {
    return h.empty;
  }
  const small = (label: string, message: Message) =>
    h.button(
      [
        h.Type("button"),
        h.DataAttribute("keep-focus", ""),
        h.Class(buttonClass({ variant: "outline", size: "xs" })),
        h.OnClick(message),
      ],
      [label],
    );
  return h.div(
    [
      h.Role("group"),
      h.AriaLabel("Voice recording"),
      h.Class(
        "flex w-72 max-w-full min-w-0 items-start gap-2 rounded-lg border bg-card px-2 py-1.5 text-sm",
      ),
    ],
    [
      micIcon(
        h,
        cn("mt-0.5 size-4 shrink-0 text-muted-foreground", recording && "text-destructive"),
      ),
      recording
        ? h.div(
            [h.Class("grid min-w-0 flex-1 gap-0.5")],
            [
              h.span([h.Class("font-medium")], ["Recording ", h.span([], [elapsed(model)])]),
              h.p(
                [
                  h.Key(`live-${model.voice.live}`),
                  h.OnMount(ScrollToEnd()),
                  h.Class(
                    "max-h-10 min-h-10 overflow-hidden text-xs/5 wrap-anywhere text-muted-foreground",
                  ),
                ],
                [model.voice.live],
              ),
            ],
          )
        : card?.state === "failed"
          ? h.div(
              [h.Class("grid min-w-0 flex-1 gap-1.5")],
              [
                h.p([h.Role("alert"), h.Class("text-destructive wrap-anywhere")], [card.error]),
                h.div(
                  [h.Class("flex gap-2")],
                  [
                    small("Send again", Message.VoiceResendClicked({ target: box.target })),
                    small("Discard", Message.VoiceRemoveClicked({ target: box.target })),
                  ],
                ),
              ],
            )
          : h.div(
              [h.Class("contents")],
              [
                h.span(
                  [h.Class("min-w-0 flex-1 font-medium")],
                  [card?.state === "converting" ? "Converting" : "Voice"],
                ),
                card?.state === "ready"
                  ? h.button(
                      [
                        h.Type("button"),
                        h.AriaLabel("Remove voice"),
                        h.Attribute("title", "Remove voice"),
                        h.DataAttribute("keep-focus", ""),
                        h.Class(
                          buttonClass({
                            variant: "secondary",
                            size: "icon-sm",
                            className: "-my-0.5 -mr-1 rounded-full max-md:size-8",
                          }),
                        ),
                        h.OnClick(Message.VoiceRemoveClicked({ target: box.target })),
                      ],
                      [xIcon(h)],
                    )
                  : h.empty,
              ],
            ),
    ],
  );
}

function voiceButton(h: H, model: Model, box: Box): Html {
  if (!voiceSupported) {
    return h.empty;
  }
  const recording = model.voice.recording;
  const label = recording ? "Stop voice input" : "Start voice input";
  return h.button(
    [
      h.Type("button"),
      h.AriaLabel(label),
      h.Attribute("title", label),
      h.AriaPressed(recording ? "true" : "false"),
      h.DataAttribute("keep-focus", ""),
      h.Disabled(voiceBusy(box.voiceCard) && !recording),
      h.Class(
        cn(
          buttonClass({
            variant: "ghost",
            size: "icon",
            className: "rounded-full text-muted-foreground max-md:size-11",
          }),
          recording &&
            "bg-destructive/10 text-destructive hover:bg-destructive/20 hover:text-destructive motion-safe:animate-pulse",
        ),
      ),
      h.OnClick(Message.VoiceButtonClicked({ target: box.target })),
    ],
    [recording ? squareIcon(h) : micIcon(h)],
  );
}

function voiceNotice(h: H, model: Model, box: Box): Html {
  const notice = model.voice.notice;
  return notice.key === box.key && notice.text
    ? h.p([h.Attribute("role", "alert"), h.Class("text-sm text-destructive")], [notice.text])
    : h.empty;
}

function composerBox(h: H, model: Model, box: Box): Html {
  const images = box.images ?? [];
  return h.form(
    [
      h.Class("grid gap-1 border-t p-2"),
      ...(box.submit ? [h.OnSubmit(box.submit)] : []),
      h.OnMount(ComposerBehavior({ autoFocus: box.autoFocus, enter: box.enter, field: box.field })),
    ],
    [
      box.images
        ? h.input([
            h.Id(pickerId),
            h.Type("file"),
            h.Accept("image/*"),
            h.Multiple(true),
            h.Hidden(true),
            h.OnFileChange((files) => Message.ImagesChosen({ files: [...files] })),
          ])
        : h.empty,
      h.div(
        [
          h.Class(
            "grid gap-1 rounded-2xl border border-input bg-background p-2 transition-colors has-[textarea:focus-visible]:border-ring dark:bg-input/30",
          ),
        ],
        [
          images.length > 0 ||
          box.inboxItems.length > 0 ||
          box.voiceCard ||
          (model.voice.recording && model.voice.key === box.key)
            ? h.div(
                [h.Class("flex flex-wrap gap-2 pb-1")],
                [
                  voiceCard(h, model, box),
                  ...box.inboxItems.map((item) => attachedInboxItem(h, item)),
                  ...images.map((_, index) => thumbnail(h, images, index)),
                ],
              )
            : h.empty,
          h.textarea([
            h.Id(box.field),
            h.DataAttribute("slot", "textarea"),
            h.Rows(1),
            h.AriaLabel(box.label),
            h.Placeholder(box.placeholder),
            h.Value(box.text),
            h.Readonly(box.readOnly),
            h.OnInput(box.onInput),
            h.Class(
              textareaClass(
                "max-h-40 min-h-9 w-full resize-none rounded-none border-0 px-1 py-1.5 focus-visible:ring-0 dark:bg-transparent",
              ),
            ),
          ]),
          h.div(
            [h.Class("flex items-center justify-between gap-2")],
            [
              box.images
                ? h.button(
                    [
                      h.Type("button"),
                      h.AriaLabel("Attach images"),
                      h.Attribute("title", "Attach images"),
                      h.DataAttribute("keep-focus", ""),
                      h.Class(
                        buttonClass({
                          variant: "ghost",
                          size: "icon",
                          className: "rounded-full text-muted-foreground max-md:size-11",
                        }),
                      ),
                      h.OnClick(Message.AttachClicked()),
                    ],
                    [paperclipIcon(h)],
                  )
                : h.empty,
              h.div(
                [h.Class("ml-auto flex items-center gap-1")],
                [voiceButton(h, model, box), ...box.actions],
              ),
            ],
          ),
        ],
      ),
      voiceNotice(h, model, box),
      box.error
        ? h.p([h.Attribute("role", "alert"), h.Class("text-sm text-destructive")], [box.error])
        : h.empty,
    ],
  );
}

export function renderComposer(h: H, model: Model): Html {
  const images = model.draftImages;
  const inboxItems = model.draftInboxItems;
  const empty = !model.draftText.trim() && images.length === 0 && inboxItems.length === 0;
  const showStop = empty && model.stoppable;
  const buttonPending = showStop && model.stopping;
  const cannotSend = empty || voiceBusy(model.draftVoice);
  const sendLabel = showStop ? "Stop the reply" : "Send";
  const sendDisabled = buttonPending || (showStop ? !model.stopReady : cannotSend);

  return composerBox(h, model, {
    field: inputId,
    target: "chat",
    key: chatDraftKey(model.organization, model.repository, model.workstream),
    voiceCard: model.draftVoice,
    label: `Message to the ${model.agent}`,
    placeholder: `Write to the ${model.agent}`,
    text: model.draftText,
    readOnly: !model.draftLoaded,
    onInput: (text) => Message.DraftTextChanged({ text }),
    submit: Message.SendSubmitted(),
    enter: "send",
    autoFocus: true,
    images,
    inboxItems,
    error: model.sendError,
    actions: [
      h.button(
        [
          // Foldkit renders in the next frame, so a tap right after the input can still see the old button.
          // A Stop button of type submit sends nothing, because the draft is empty then.
          h.Type("submit"),
          h.AriaLabel(sendLabel),
          h.Attribute("title", sendLabel),
          h.DataAttribute("keep-focus", ""),
          h.Disabled(sendDisabled),
          h.Class(
            cn(
              buttonClass({ size: "icon", className: "rounded-full max-md:size-11" }),
              showStop &&
                !model.stopReady &&
                !buttonPending &&
                "disabled:pointer-events-auto disabled:opacity-100",
              cannotSend &&
                !showStop &&
                "bg-muted text-muted-foreground hover:bg-muted disabled:opacity-100",
            ),
          ),
          ...(showStop ? [h.OnClick(Message.StopChatClicked())] : []),
        ],
        buttonPending
          ? [spinnerIcon(h)]
          : [showStop ? squareIcon(h, "size-3 fill-current") : arrowUpIcon(h)],
      ),
    ],
  });
}

// The field of the answer panel. A pending action has the buttons Reject and Approve, and each other item has the
// Send button.
export function renderPanelComposer(h: H, model: Model, item: InboxItem): Html {
  const action = item.action;
  const todo = item.todo;
  const selected = Object.values(model.selectedOptions).some((options) => options.length > 0);
  const decide = Boolean(action);
  const busy = model.requesting.includes(item.id);
  const holdsVoice = voiceBusy(model.answerVoice);
  const empty = !model.answerText.trim() && !(todo ? true : !action && selected);
  const cannotSend = empty || holdsVoice;
  const role = action ? (roleName[action.role] ?? action.role) : "";
  const label = action ? `Note for the ${role}` : todo ? "Note" : "Your answer";
  const placeholder = action
    ? `Add a note for the ${role} (optional)`
    : todo
      ? "Note (optional)"
      : item.questions.length > 0
        ? "Add a note (optional)"
        : "Write your answer";

  return composerBox(h, model, {
    field: answerInputId,
    target: "answer",
    key: answerDraftKey(item.id),
    voiceCard: model.answerVoice,
    label,
    placeholder,
    text: model.answerText,
    readOnly: !model.answerLoaded || busy,
    onInput: (text) => Message.AnswerTextChanged({ text }),
    submit: decide ? null : Message.AnswerSubmitted(),
    enter: decide ? "none" : "answer",
    autoFocus: false,
    images: null,
    inboxItems: [],
    error: model.answerErrorId === item.id ? model.answerError : "",
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
              h.AriaLabel("Send"),
              h.Attribute("title", "Send"),
              h.DataAttribute("keep-focus", ""),
              h.Disabled(busy || cannotSend),
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
  });
}
