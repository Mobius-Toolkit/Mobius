import {
  ArrowUpIcon,
  BanIcon,
  CheckIcon,
  MicIcon,
  PaperclipIcon,
  SquareIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { Decision } from "@/lib/actions";
import {
  type DraftInboxItem,
  emptyDraft,
  loadDraft,
  saveDraftText,
  updateDraft,
  useDraft,
} from "@/lib/draft";
import { fitImage, maxImages } from "@/lib/images";
import { cn } from "@/lib/utils";
import { useVoice } from "@/lib/voice";
import { FileImage } from "./FileImage";
import { AttachedInboxItem } from "./InboxItemCard";

const stopDelay = 500;
const tooManyImages = `A message has at most ${maxImages} images.`;

function Thumbnail({
  file,
  position,
  remove,
}: {
  file: File;
  position: number;
  remove: () => void;
}) {
  return (
    <div className="relative">
      <FileImage
        file={file}
        alt={`Image ${position}`}
        className="size-16 rounded-lg border object-cover"
      />
      <Button
        type="button"
        variant="secondary"
        size="icon-sm"
        aria-label={`Remove image ${position}`}
        title={`Remove image ${position}`}
        className="absolute top-0.5 right-0.5 rounded-full max-md:size-8"
        onMouseDown={(event) => event.preventDefault()}
        onClick={remove}
      >
        <XIcon />
      </Button>
    </div>
  );
}

// The text field with the voice button and the Send button. The draft store keeps the text, the images and the Inbox
// items under draftKey, and the field is read-only until the store gives them. When onStop is set and the draft is
// empty, the Send button stops the reply. error is the text of a failure that the parent shows
// under the field. The input gives its own failures to onError. When onSend gives a promise, the draft gets its text,
// images and Inbox items back if the promise gives false. With onDecide, the field has no Send button and Enter sends nothing. The field has
// a Reject button and an Approve button, and each one gives the text, empty or not, to onDecide. Both buttons stay
// disabled while the promise runs. The field clears if the promise gives true.
export function MessageInput({
  draftKey,
  label,
  placeholder,
  images: withImages,
  error,
  onError,
  stopping,
  onStop,
  onSend,
  onDecide,
}: {
  draftKey: string;
  label: string;
  placeholder: string;
  images: boolean;
  error: string;
  onError: (error: string) => void;
  stopping: boolean;
  onStop?: () => void;
  onSend?: (text: string, images: File[], inboxItems: DraftInboxItem[]) => void | Promise<boolean>;
  onDecide?: (decision: Decision, text: string) => Promise<boolean>;
}) {
  const stored = useDraft(draftKey);
  const { text, images, inboxItems } = stored ?? emptyDraft;
  const setText = (value: string) => void saveDraftText(draftKey, value);
  // Two taps on Send in one turn of the page both come before the next render. Thus only the ref stops a second
  // message. The next render has the empty input and clears the ref.
  const justSent = useRef(false);
  const [deciding, setDeciding] = useState(false);
  useEffect(() => {
    justSent.current = false;
  });
  const input = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  // The text around the voice text of the run, and the value of the field after the last voice text.
  const voiceRegion = useRef<{ lead: string; trail: string; value: string }>(undefined);
  const placeVoice = (
    field: HTMLTextAreaElement,
    voiceText: string,
    lead: string,
    trail: string,
  ) => {
    const value = lead + voiceText + trail;
    const cursor = lead.length + voiceText.length;
    flushSync(() => setText(value));
    // The height of the text up to the end of the voice text gives the scroll position. setSelectionRange does not
    // scroll a field without the focus, and WebKit on iOS does not scroll it reliably.
    field.value = lead + voiceText;
    const top = field.scrollHeight - field.clientHeight;
    field.value = value;
    field.setSelectionRange(cursor, cursor);
    field.scrollTop = top;
    voiceRegion.current = { lead, trail, value: field.value };
  };
  const placeAtCursor = (field: HTMLTextAreaElement, voiceText: string) => {
    const focused = document.activeElement === field;
    const start = focused ? field.selectionStart : field.value.length;
    const end = focused ? field.selectionEnd : field.value.length;
    const before = field.value.slice(0, start);
    const after = field.value.slice(end);
    const lead = before === "" || /\s$/.test(before) ? before : `${before} `;
    const trail = after === "" || /^\s/.test(after) ? after : ` ${after}`;
    placeVoice(field, voiceText, lead, trail);
  };
  const voice = useVoice(
    (voiceText, live) => {
      const field = input.current;
      const region = voiceRegion.current;
      if (!field) {
        return;
      }
      if (live && region && field.value === region.value) {
        placeVoice(field, voiceText, region.lead, region.trail);
      } else if (voiceText) {
        placeAtCursor(field, voiceText);
      }
    },
    (voiceText, first) => {
      const field = input.current;
      const region = voiceRegion.current;
      if (!field) {
        return false;
      }
      if (first) {
        placeAtCursor(field, voiceText);
        return true;
      }
      if (!region || field.value !== region.value) {
        return false;
      }
      placeVoice(field, voiceText, region.lead, region.trail);
      return true;
    },
  );

  const removeImage = (index: number) =>
    void updateDraft(draftKey, (draft) => ({
      ...draft,
      images: draft.images.filter((_, i) => i !== index),
    }));

  const removeInboxItem = (id: number) =>
    void updateDraft(draftKey, (draft) => ({
      ...draft,
      inboxItems: draft.inboxItems.filter((item) => item.id !== id),
    }));

  const addImages = async (files: File[]) => {
    const room = maxImages - (await loadDraft(draftKey)).images.length;
    let problem = "";
    const added: File[] = [];
    for (const file of files.slice(0, room)) {
      try {
        added.push(await fitImage(file));
      } catch (err) {
        problem = err instanceof Error ? err.message : String(err);
      }
    }
    const all = [...(await loadDraft(draftKey)).images, ...added];
    void updateDraft(draftKey, (draft) => ({ ...draft, images: all.slice(0, maxImages) }));
    if (!problem && (files.length > room || all.length > maxImages)) {
      problem = tooManyImages;
    }
    onError(problem);
  };

  const empty = !text.trim() && images.length === 0 && inboxItems.length === 0;
  const showStop = empty && !!onStop;
  const buttonPending = showStop && stopping;
  // A tap on Send can land on the Stop button that replaces it. Thus Stop takes no tap in its first moments.
  const [stopReady, setStopReady] = useState(false);
  useEffect(() => {
    if (!showStop) {
      return;
    }
    const timer = setTimeout(() => setStopReady(true), stopDelay);
    return () => {
      clearTimeout(timer);
      setStopReady(false);
    };
  }, [showStop]);

  const voiceLabel = voice.converting
    ? "Converting the voice input"
    : voice.recording
      ? "Stop voice input"
      : "Start voice input";

  const send = () => {
    if (!onSend || empty || justSent.current) {
      return;
    }
    voice.abort();
    justSent.current = true;
    void updateDraft(draftKey, () => emptyDraft);
    onError("");
    const result = onSend(text, images, inboxItems);
    if (result instanceof Promise) {
      void result.then((done) => {
        if (!done) {
          // The Chat button of an Inbox row can add an item while the message goes out.
          void updateDraft(draftKey, (draft) => ({
            text,
            images,
            inboxItems: [
              ...inboxItems,
              ...draft.inboxItems.filter((item) => !inboxItems.some(({ id }) => id === item.id)),
            ],
          }));
        }
      });
    }
  };

  const decide = async (decision: Decision) => {
    if (!onDecide || justSent.current) {
      return;
    }
    voice.abort();
    justSent.current = true;
    setDeciding(true);
    onError("");
    const done = await onDecide(decision, text);
    setDeciding(false);
    if (done) {
      setText("");
    }
  };

  return (
    <form
      className="grid gap-1 border-t p-2"
      onSubmit={(event) => {
        event.preventDefault();
        send();
      }}
    >
      {withImages && (
        <input
          ref={picker}
          type="file"
          accept="image/*"
          multiple
          hidden
          onChange={(event) => {
            void addImages([...(event.target.files ?? [])]);
            event.target.value = "";
          }}
        />
      )}
      <div className="grid gap-1 rounded-2xl border border-input bg-background p-2 transition-colors has-[textarea:focus-visible]:border-ring dark:bg-input/30">
        {(images.length > 0 || inboxItems.length > 0) && (
          <div className="flex flex-wrap gap-2 pb-1">
            {inboxItems.map((item) => (
              <AttachedInboxItem
                key={item.id}
                item={item}
                remove={() => removeInboxItem(item.id)}
              />
            ))}
            {images.map((image, index) => (
              <Thumbnail
                key={index}
                file={image}
                position={index + 1}
                remove={() => removeImage(index)}
              />
            ))}
          </div>
        )}
        <Textarea
          ref={input}
          rows={1}
          aria-label={label}
          placeholder={placeholder}
          value={text}
          readOnly={!stored}
          onChange={(event) => setText(event.target.value)}
          // Safari on iOS scrolls the page when the keyboard opens, and it can keep that offset after the keyboard closes.
          onBlur={() => window.scrollTo(0, 0)}
          onPaste={(event) => {
            if (!withImages) {
              return;
            }
            const pasted = [...event.clipboardData.files].filter((file) =>
              file.type.startsWith("image/"),
            );
            // Spreadsheet and word processor apps put the text and a picture of the selection on the clipboard.
            if (pasted.length > 0 && !event.clipboardData.getData("text/plain")) {
              event.preventDefault();
              void addImages(pasted);
            }
          }}
          onKeyDown={(event) => {
            // On a touch screen, Enter adds a line and only the Send button sends. Safari gives the Enter that
            // ends an IME composition with isComposing false and keyCode 229.
            if (
              event.key === "Enter" &&
              !event.shiftKey &&
              !event.nativeEvent.isComposing &&
              event.keyCode !== 229 &&
              !window.matchMedia("(pointer: coarse)").matches
            ) {
              event.preventDefault();
              send();
            }
          }}
          className="chat-text! max-h-40 min-h-9 w-full resize-none rounded-none border-0 px-1 py-1.5 focus-visible:ring-0 dark:bg-transparent"
        />
        <div className="flex items-center justify-between gap-2">
          {withImages && (
            // A button that takes the focus closes the keyboard of a phone, and the button moves before the click.
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Attach images"
              title="Attach images"
              className="rounded-full text-muted-foreground max-md:size-11"
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => picker.current?.click()}
            >
              <PaperclipIcon />
            </Button>
          )}
          <div className="ml-auto flex items-center gap-1">
            {voice.supported && (
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={voiceLabel}
                title={voiceLabel}
                aria-pressed={voice.recording}
                pending={voice.converting}
                className={cn(
                  "rounded-full text-muted-foreground max-md:size-11",
                  voice.recording &&
                    "bg-destructive/10 text-destructive hover:bg-destructive/20 hover:text-destructive motion-safe:animate-pulse",
                  voice.converting && "disabled:pointer-events-auto disabled:opacity-100",
                )}
                onMouseDown={(event) => event.preventDefault()}
                onClick={voice.toggle}
              >
                {voice.converting ? null : voice.recording ? <SquareIcon /> : <MicIcon />}
              </Button>
            )}
            {onDecide ? (
              <>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label="Reject"
                  title="Reject"
                  disabled={deciding}
                  className="rounded-full text-muted-foreground max-md:size-11"
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => void decide("reject")}
                >
                  <BanIcon />
                </Button>
                <Button
                  type="button"
                  disabled={deciding}
                  className="rounded-full max-md:h-11 max-md:px-4"
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => void decide("approve")}
                >
                  <CheckIcon />
                  Approve
                </Button>
              </>
            ) : (
              <Button
                type={showStop ? "button" : "submit"}
                size="icon"
                aria-label={showStop ? "Stop the reply" : "Send"}
                title={showStop ? "Stop the reply" : "Send"}
                pending={buttonPending}
                disabled={showStop ? !stopReady : empty}
                className={cn(
                  "rounded-full max-md:size-11",
                  showStop &&
                    !stopReady &&
                    !buttonPending &&
                    "disabled:pointer-events-auto disabled:opacity-100",
                  empty &&
                    !showStop &&
                    "bg-muted text-muted-foreground hover:bg-muted disabled:opacity-100",
                )}
                onMouseDown={(event) => event.preventDefault()}
                onClick={showStop ? onStop : undefined}
              >
                {buttonPending ? null : showStop ? (
                  <SquareIcon className="size-3 fill-current" />
                ) : (
                  <ArrowUpIcon />
                )}
              </Button>
            )}
          </div>
        </div>
      </div>
      {voice.error && (
        <p role="alert" className="text-sm text-destructive">
          {voice.error}
        </p>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
    </form>
  );
}
