import {
  ArrowUpIcon,
  ChevronRightIcon,
  MicIcon,
  PaperclipIcon,
  SquareIcon,
  XIcon,
} from "lucide-react";
import {
  Fragment,
  use,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { flushSync } from "react-dom";
import {
  getChat,
  getGetChatImageUrl,
  seeChat,
  sendChat,
  stopChat,
  type ChatMessage,
  type LiveEvents,
  type Workstream,
} from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Textarea } from "@/components/ui/textarea";
import { onEvent } from "@/lib/events";
import { fitImage, maxImages } from "@/lib/images";
import { LoginContext } from "@/lib/login";
import { atEnd } from "@/lib/scroll";
import { clock, dayLabel } from "@/lib/time";
import { sameChat } from "@/lib/unread";
import { cn } from "@/lib/utils";
import { useVoice } from "@/lib/voice";
import { Markdown } from "./Markdown";

const tooManyImages = `A message has at most ${maxImages} images.`;

function upsert(list: ChatMessage[], message: ChatMessage) {
  const known = list.find((other) => other.id === message.id);
  // The agent only adds text to a message, so the longer text is the newer text.
  if (known && known.text.length >= message.text.length) {
    return list;
  }
  return [...list.filter((other) => other.id !== message.id), message].toSorted(
    (a, b) => a.id - b.id,
  );
}

function Message({ message }: { message: ChatMessage }) {
  const event = message.author === "Event";
  const [summary, ...rest] = message.text.split("\n");
  const body = rest.join("\n").trim();
  return (
    <div
      data-message={message.id}
      className={cn(
        "grid max-w-[85%] grid-cols-[minmax(0,1fr)] gap-1 rounded-xl border px-3 py-2",
        message.author === "Owner"
          ? "justify-self-end border-transparent bg-secondary"
          : "justify-self-start bg-card",
        event && "border-dashed bg-transparent text-muted-foreground",
      )}
    >
      <div className="flex gap-2 text-xs text-muted-foreground">
        <span>{message.author === "tell_owner" ? "Lead" : message.author}</span>
        <span>{clock(message.time)}</span>
      </div>
      {event && body ? (
        <Collapsible>
          <CollapsibleTrigger className="group flex w-full min-w-0 items-start gap-1 text-left break-words">
            <ChevronRightIcon className="mt-0.5 size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
            <span className="min-w-0">{summary}</span>
          </CollapsibleTrigger>
          <CollapsibleContent className="pt-2">
            <Markdown text={body} />
          </CollapsibleContent>
        </Collapsible>
      ) : (
        message.text && <Markdown text={message.text} />
      )}
      {message.images > 0 && (
        <div className="flex flex-wrap gap-2">
          {Array.from({ length: message.images }, (_, position) => (
            <img
              key={position}
              src={getGetChatImageUrl(message.id, position)}
              alt={`Picture ${position + 1} of message ${message.id}`}
              className="max-h-48 max-w-full rounded-lg border object-contain"
            />
          ))}
        </div>
      )}
    </div>
  );
}

function DaySeparator({ label }: { label: string }) {
  return (
    <div
      role="separator"
      aria-label={label}
      className="flex items-center gap-3 text-xs text-muted-foreground"
    >
      <span className="h-px grow bg-border" />
      <span>{label}</span>
      <span className="h-px grow bg-border" />
    </div>
  );
}

function Thumbnail({
  file,
  position,
  remove,
}: {
  file: File;
  position: number;
  remove: () => void;
}) {
  const show = useCallback(
    (image: HTMLImageElement) => {
      const url = URL.createObjectURL(file);
      image.src = url;
      return () => URL.revokeObjectURL(url);
    },
    [file],
  );
  return (
    <div className="relative">
      <img
        ref={show}
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

// The chat of the agent: a Lead chat, or the Triager chat with the empty repository and the Workstream 0. unread is
// undefined until the first read of the unread counts.
export function Conversation({
  organization,
  repository,
  workstream,
  agent,
  source,
  unread,
  head,
  tail,
  brief,
  note,
  footer,
}: {
  organization: string;
  repository: string;
  workstream: number;
  agent: string;
  source?: EventSource;
  unread?: number;
  head: ReactNode;
  tail?: ReactNode;
  brief?: Workstream;
  note?: ReactNode;
  footer?: ReactNode;
}) {
  const showLogin = use(LoginContext);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [harness, setHarness] = useState("");
  const [writing, setWriting] = useState(false);
  const [failure, setFailure] = useState("");
  const [error, setError] = useState<string>();
  const [text, setText] = useState("");
  const [images, setImages] = useState<File[]>([]);
  const [sendError, setSendError] = useState("");
  const [sending, setSending] = useState(false);
  const [stopping, setStopping] = useState(false);
  // Two taps on Send in one turn of the page both come before the next render. Thus only the ref stops a second
  // message.
  const imagesRef = useRef<File[]>([]);
  const inFlight = useRef(false);
  const [briefOpen, setBriefOpen] = useState<boolean>();
  const listRef = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const pinned = useRef(true);
  // The scrollTop of the last scroll to the end. A scroll event can come after an image has made the list longer.
  const endTop = useRef<number>(undefined);
  const scrollToEnd = (list: HTMLElement) => {
    list.scrollTop = list.scrollHeight;
    endTop.current = list.scrollTop;
  };
  // The number of messages at the last scroll, or undefined before the first scroll.
  const scrolledCount = useRef<number>(undefined);
  const voice = useVoice((spoken) => {
    const field = input.current;
    if (!field) {
      return;
    }
    const focused = document.activeElement === field;
    const start = focused ? field.selectionStart : field.value.length;
    const end = focused ? field.selectionEnd : field.value.length;
    const before = field.value.slice(0, start);
    const after = field.value.slice(end);
    const lead = before === "" || /\s$/.test(before) ? before : `${before} `;
    const trail = after === "" || /^\s/.test(after) ? after : ` ${after}`;
    flushSync(() => setText(lead + spoken + trail));
    const cursor = lead.length + spoken.length;
    field.setSelectionRange(cursor, cursor);
  });

  const load = useCallback(() => {
    getChat({ organization, repository, workstream })
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          const chat = res.data.data;
          setMessages((list) => chat.messages.reduce(upsert, list));
          setHarness(chat.harness);
          setWriting(chat.writing);
          if (!chat.writing) {
            setStopping(false);
          }
          setLoaded(true);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [organization, repository, workstream, showLogin]);

  // A message that comes while the connection is down is lost, so each connection reads the chat.
  useEffect(() => {
    if (!source) {
      return;
    }
    const key = { organization, repository, workstream };
    load();
    source.addEventListener("open", load);
    const removeMessage = onEvent<LiveEvents, "message">(source, "message", (message) => {
      if (sameChat(message, key)) {
        setMessages((list) => upsert(list, message));
      }
    });
    const removeState = onEvent<LiveEvents, "chat">(source, "chat", (state) => {
      if (sameChat(state, key)) {
        setWriting(state.writing);
        if (!state.writing) {
          setStopping(false);
        }
        setFailure(state.error);
      }
    });
    return () => {
      source.removeEventListener("open", load);
      removeMessage();
      removeState();
    };
  }, [source, load, organization, repository, workstream]);

  const lastAgentMessage = messages.findLast((message) => message.author !== "Owner")?.id;
  useEffect(() => {
    if (unread && lastAgentMessage !== undefined) {
      // A failed call keeps the count, and the next message of the agent calls again.
      seeChat({
        organization,
        repository,
        workstream,
        message: lastAgentMessage,
      }).catch(() => {});
    }
  }, [unread, lastAgentMessage, organization, repository, workstream]);

  // The first scroll shows the first unread message, or the end. Then a new message scrolls to the end, and a longer
  // last message scrolls only while the Owner is at the end.
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || !loaded || unread === undefined) {
      return;
    }
    if (scrolledCount.current === undefined) {
      scrolledCount.current = messages.length;
      const unreadMessages = messages.filter(
        (message) => message.author !== "Owner" && message.author !== "Event",
      );
      const first = unread > 0 && unreadMessages[Math.max(0, unreadMessages.length - unread)];
      const element = first && list.querySelector(`[data-message="${first.id}"]`);
      if (element) {
        list.scrollTop += element.getBoundingClientRect().top - list.getBoundingClientRect().top;
        // The scroll event can come after the next render, and that render must not scroll to the end.
        pinned.current = atEnd(list);
        return;
      }
      scrollToEnd(list);
      return;
    }
    if (messages.length > scrolledCount.current) {
      pinned.current = true;
    }
    scrolledCount.current = messages.length;
    if (pinned.current) {
      scrollToEnd(list);
    }
  });

  // The ref has the images at once, so two calls that wait for fitImage count the images of each other.
  const changeImages = (change: (current: File[]) => File[]) => {
    imagesRef.current = change(imagesRef.current);
    setImages(imagesRef.current);
  };

  const restoreImages = (sentImages: File[]) => {
    const all = [...sentImages, ...imagesRef.current];
    changeImages(() => all.slice(0, maxImages));
    return all.length > maxImages;
  };

  const addImages = async (files: File[]) => {
    const room = maxImages - imagesRef.current.length;
    let problem = "";
    const added: File[] = [];
    for (const file of files.slice(0, room)) {
      try {
        added.push(await fitImage(file));
      } catch (err) {
        problem = err instanceof Error ? err.message : String(err);
      }
    }
    const all = [...imagesRef.current, ...added];
    changeImages(() => all.slice(0, maxImages));
    if (!problem && (files.length > room || all.length > maxImages)) {
      problem = tooManyImages;
    }
    setSendError(problem);
  };

  const empty = !text.trim() && images.length === 0;
  const showStop = empty && writing;
  const buttonPending = sending || (showStop && stopping);

  const send = () => {
    if (inFlight.current || empty) {
      return;
    }
    const sent = text;
    const sentImages = imagesRef.current;
    voice.abort();
    inFlight.current = true;
    setSending(true);
    setText("");
    changeImages(() => []);
    sendChat({ organization, repository, workstream, text: sent, images: sentImages })
      .then((res) => {
        if (res.status === 204) {
          setSendError("");
          return;
        }
        setText((current) => sent + current);
        const dropped = restoreImages(sentImages);
        if (res.status === 401) {
          showLogin();
          setSendError(dropped ? tooManyImages : "");
        } else {
          setSendError(dropped ? `${res.data.error} ${tooManyImages}` : res.data.error);
        }
      })
      .catch((err: unknown) => {
        setText((current) => sent + current);
        const dropped = restoreImages(sentImages);
        setSendError(dropped ? `${String(err)} ${tooManyImages}` : String(err));
      })
      .finally(() => {
        inFlight.current = false;
        setSending(false);
      });
  };

  const stop = () => {
    setStopping(true);
    stopChat({ organization, repository, workstream })
      .then((res) => {
        if (res.status !== 204) {
          setSendError(res.data.error);
          setStopping(false);
        }
      })
      .catch((err: unknown) => {
        setSendError(String(err));
        setStopping(false);
      });
  };

  return (
    <section className="flex min-h-0 min-w-0 grow flex-col">
      <header className="flex min-h-14 items-center gap-2 border-b px-4 py-2">
        {head}
        <span className="grow" />
        {harness && (
          <span className="truncate text-sm text-muted-foreground">
            {agent}: {harness}
          </span>
        )}
        {tail}
      </header>
      {brief && (
        <Collapsible
          open={briefOpen ?? (loaded && messages.length === 0)}
          onOpenChange={setBriefOpen}
          className="border-b px-4 py-2"
        >
          <CollapsibleTrigger className="group flex w-full items-center gap-2 text-left font-medium">
            <span className="grow truncate">{brief.title}</span>
            <ChevronRightIcon className="size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
          </CollapsibleTrigger>
          <CollapsibleContent className="max-h-[40svh] overflow-y-auto pt-2">
            <Markdown text={brief.brief} />
          </CollapsibleContent>
        </Collapsible>
      )}
      {note}
      <div
        ref={listRef}
        onScroll={(event) => {
          pinned.current =
            atEnd(event.currentTarget) || event.currentTarget.scrollTop === endTop.current;
        }}
        // An image has no height before it loads, and its load moves the end of the list.
        onLoadCapture={(event) => {
          if (pinned.current) {
            scrollToEnd(event.currentTarget);
          }
        }}
        className="grid min-h-0 grow grid-cols-[minmax(0,1fr)] content-start gap-3 overflow-y-auto bg-muted/40 p-4"
      >
        {error && <Badge variant="destructive">{error}</Badge>}
        {loaded && messages.length === 0 && (
          <p className="text-center text-sm text-muted-foreground">
            No messages. Write to start a chat session.
          </p>
        )}
        {messages.map((message, index) => {
          const label = dayLabel(message.time);
          return (
            <Fragment key={message.id}>
              {(index === 0 || dayLabel(messages[index - 1].time) !== label) && (
                <DaySeparator label={label} />
              )}
              <Message message={message} />
            </Fragment>
          );
        })}
        {writing && (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <span className="size-2 animate-pulse rounded-full bg-green-600" />
            The {agent} writes a reply.
          </p>
        )}
        {failure && (
          <Badge variant="destructive" className="h-auto w-full justify-start whitespace-normal">
            The chat session failed: {failure}
          </Badge>
        )}
      </div>
      {footer}
      <form
        className="grid gap-1 border-t p-2"
        onSubmit={(event) => {
          event.preventDefault();
          send();
        }}
      >
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
        <div className="grid gap-1 rounded-2xl border border-input bg-background p-2 transition-colors has-[textarea:focus-visible]:border-ring dark:bg-input/30">
          {images.length > 0 && (
            <div className="flex flex-wrap gap-2 pb-1">
              {images.map((image, index) => (
                <Thumbnail
                  key={index}
                  file={image}
                  position={index + 1}
                  remove={() => changeImages((current) => current.filter((_, i) => i !== index))}
                />
              ))}
            </div>
          )}
          <Textarea
            ref={input}
            rows={1}
            aria-label={`Message to the ${agent}`}
            placeholder={`Write to the ${agent}`}
            value={text}
            onChange={(event) => setText(event.target.value)}
            onPaste={(event) => {
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
            className="max-h-40 min-h-9 w-full resize-none rounded-none border-0 px-1 py-1.5 focus-visible:ring-0 dark:bg-transparent"
          />
          <div className="flex items-center justify-between gap-2">
            {/* A button that takes the focus closes the keyboard of a phone, and the button moves before the click. */}
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
            <div className="flex items-center gap-1">
              {voice.supported && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={voice.listening ? "Stop voice input" : "Start voice input"}
                  title={voice.listening ? "Stop voice input" : "Start voice input"}
                  aria-pressed={voice.listening}
                  className={cn(
                    "rounded-full text-muted-foreground max-md:size-11",
                    voice.listening &&
                      "bg-destructive/10 text-destructive hover:bg-destructive/20 hover:text-destructive motion-safe:animate-pulse",
                  )}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={voice.toggle}
                >
                  {voice.listening ? <SquareIcon /> : <MicIcon />}
                </Button>
              )}
              <Button
                type={showStop ? "button" : "submit"}
                size="icon"
                aria-label={showStop ? "Stop the reply" : "Send"}
                title={showStop ? "Stop the reply" : "Send"}
                pending={buttonPending}
                disabled={empty && !writing}
                className={cn(
                  "rounded-full max-md:size-11",
                  empty &&
                    !writing &&
                    "bg-muted text-muted-foreground hover:bg-muted disabled:opacity-100",
                )}
                onMouseDown={(event) => event.preventDefault()}
                onClick={showStop ? stop : undefined}
              >
                {buttonPending ? null : showStop ? (
                  <SquareIcon className="size-3 fill-current" />
                ) : (
                  <ArrowUpIcon />
                )}
              </Button>
            </div>
          </div>
        </div>
        {voice.error && (
          <p role="alert" className="text-sm text-destructive">
            {voice.error}
          </p>
        )}
        {sendError && (
          <p role="alert" className="text-sm text-destructive">
            {sendError}
          </p>
        )}
      </form>
    </section>
  );
}
