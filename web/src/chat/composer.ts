import { Effect, Queue, Schema, Stream } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { Mount } from "foldkit";
import type { DraftInboxItem } from "@/lib/draft";
import { voiceSupported } from "@/lib/voice";
import { arrowUpIcon, micIcon, paperclipIcon, spinnerIcon, squareIcon, xIcon } from "./icons";
import { Message } from "./message";
import type { Model } from "./model";
import { buttonClass, cn, textareaClass } from "./ui";

type H = HtmlBuilder<Message>;
type ComposerMessage = ReturnType<typeof Message.SendSubmitted | typeof Message.ImagesChosen>;

export const inputId = "chat-input";
const pickerId = "chat-attach";
const fileUrls = new WeakMap<File, string>();

function fileUrl(file: File) {
  let url = fileUrls.get(file);
  if (!url) {
    url = URL.createObjectURL(file);
    fileUrls.set(file, url);
  }
  return url;
}

// A button that takes the focus closes the keyboard of a phone, and the button moves before the click.
function keepFocus(event: Event) {
  if ((event.target as Element).closest("[data-keep-focus]")) {
    event.preventDefault();
  }
}

// One Mount owns the behavior that needs the DOM event: Enter, a paste with a picture, a blur on iOS, the focus at
// the start, and a mouse down on a button that must not take the focus from the field.
export const ComposerBehavior = Mount.defineStream("ComposerBehavior", {
  args: { autoFocus: Schema.Boolean },
  messages: [Message.SendSubmitted, Message.ImagesChosen],
  execute: ({ element, autoFocus }) =>
    Stream.callback<ComposerMessage>((queue) =>
      Effect.gen(function* () {
        const field = () => element.querySelector<HTMLTextAreaElement>(`#${inputId}`);
        const keydown = (event: Event) => {
          const key = event as KeyboardEvent;
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
            Queue.offerUnsafe(queue, Message.SendSubmitted());
          }
        };
        const paste = (event: Event) => {
          const data = (event as ClipboardEvent).clipboardData;
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
        element.addEventListener("keydown", keydown);
        element.addEventListener("paste", paste);
        element.addEventListener("focusout", blur);
        element.addEventListener("mousedown", keepFocus);
        if (autoFocus && window.matchMedia("(pointer: fine)").matches) {
          field()?.focus();
        }
        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
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

function thumbnail(h: H, file: File, index: number): Html {
  return h.div(
    [h.Key(`thumb-${index}`), h.Class("relative")],
    [
      h.img([
        h.Src(fileUrl(file)),
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

export function renderComposer(h: H, model: Model): Html {
  const images = model.draftImages as File[];
  const inboxItems = model.draftInboxItems as DraftInboxItem[];
  const empty = !model.draftText.trim() && images.length === 0 && inboxItems.length === 0;
  const showStop = empty && model.stoppable;
  const buttonPending = (showStop && model.stopping) || model.waitingForSend;
  const recording = model.voicePhase === "recording";
  const converting = model.voicePhase === "converting";
  const voiceLabel = converting
    ? "Converting the voice input"
    : recording
      ? "Stop voice input"
      : "Start voice input";
  const sendLabel = showStop ? "Stop the reply" : "Send";
  const sendDisabled = buttonPending || (showStop ? !model.stopReady : empty);

  return h.form(
    [
      h.Class("grid gap-1 border-t p-2"),
      h.OnSubmit(Message.SendSubmitted()),
      h.OnMount(ComposerBehavior({ autoFocus: true })),
    ],
    [
      h.input([
        h.Id(pickerId),
        h.Type("file"),
        h.Accept("image/*"),
        h.Multiple(true),
        h.Hidden(true),
        h.OnFileChange((files) => Message.ImagesChosen({ files: [...files] })),
      ]),
      h.div(
        [
          h.Class(
            "grid gap-1 rounded-2xl border border-input bg-background p-2 transition-colors has-[textarea:focus-visible]:border-ring dark:bg-input/30",
          ),
        ],
        [
          images.length > 0 || inboxItems.length > 0
            ? h.div(
                [h.Class("flex flex-wrap gap-2 pb-1")],
                [
                  ...inboxItems.map((item) => attachedInboxItem(h, item)),
                  ...images.map((file, index) => thumbnail(h, file, index)),
                ],
              )
            : h.empty,
          h.textarea([
            h.Id(inputId),
            h.DataAttribute("slot", "textarea"),
            h.Rows(1),
            h.AriaLabel(`Message to the ${model.agent}`),
            h.Placeholder(`Write to the ${model.agent}`),
            h.Value(model.draftText),
            h.Readonly(!model.draftLoaded || model.waitingForSend),
            h.OnInput((text) => Message.DraftTextChanged({ text })),
            h.Class(
              textareaClass(
                "max-h-40 min-h-9 w-full resize-none rounded-none border-0 px-1 py-1.5 focus-visible:ring-0 dark:bg-transparent",
              ),
            ),
          ]),
          h.div(
            [h.Class("flex items-center justify-between gap-2")],
            [
              h.button(
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
              ),
              h.div(
                [h.Class("ml-auto flex items-center gap-1")],
                [
                  voiceSupported
                    ? h.button(
                        [
                          h.Type("button"),
                          h.AriaLabel(voiceLabel),
                          h.Attribute("title", voiceLabel),
                          h.AriaPressed(recording ? "true" : "false"),
                          h.DataAttribute("keep-focus", ""),
                          h.Disabled(converting),
                          h.Class(
                            cn(
                              buttonClass({
                                variant: "ghost",
                                size: "icon",
                                className: "rounded-full text-muted-foreground max-md:size-11",
                              }),
                              recording &&
                                "bg-destructive/10 text-destructive hover:bg-destructive/20 hover:text-destructive motion-safe:animate-pulse",
                              converting && "disabled:pointer-events-auto disabled:opacity-100",
                            ),
                          ),
                          h.OnClick(Message.VoiceButtonClicked()),
                        ],
                        converting ? [spinnerIcon(h)] : [recording ? squareIcon(h) : micIcon(h)],
                      )
                    : h.empty,
                  h.button(
                    [
                      h.Type(showStop ? "button" : "submit"),
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
                          empty &&
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
              ),
            ],
          ),
        ],
      ),
      model.voiceError
        ? h.div(
            [h.Class("flex flex-wrap items-center gap-2")],
            [
              h.p(
                [h.Attribute("role", "alert"), h.Class("text-sm text-destructive")],
                [model.voiceError],
              ),
              ...(model.voiceCanResend
                ? [
                    h.button(
                      [
                        h.Type("button"),
                        h.DataAttribute("keep-focus", ""),
                        h.Class(buttonClass({ variant: "outline", size: "xs" })),
                        h.OnClick(Message.VoiceResendClicked()),
                      ],
                      ["Send again"],
                    ),
                    h.button(
                      [
                        h.Type("button"),
                        h.DataAttribute("keep-focus", ""),
                        h.Class(buttonClass({ variant: "outline", size: "xs" })),
                        h.OnClick(Message.VoiceDiscardClicked()),
                      ],
                      ["Discard"],
                    ),
                  ]
                : []),
            ],
          )
        : h.empty,
      model.sendError
        ? h.p(
            [h.Attribute("role", "alert"), h.Class("text-sm text-destructive")],
            [model.sendError],
          )
        : h.empty,
    ],
  );
}
