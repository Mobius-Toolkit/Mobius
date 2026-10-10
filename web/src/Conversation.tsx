import {
  ArrowUpIcon,
  BanIcon,
  CheckCheckIcon,
  CheckIcon,
  ChevronRightIcon,
  CircleAlertIcon,
  ClockIcon,
  MicIcon,
  PaperclipIcon,
  SquareIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import {
  Fragment,
  memo,
  use,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { flushSync } from "react-dom";
import {
  getChat,
  getGetChatImageUrl,
  seeChat,
  stopChat,
  type Chat,
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
import { addQueued, listQueued, postQueued, removeQueued, type Queued } from "@/lib/outbox";
import { atEnd } from "@/lib/scroll";
import { clock, clockSeconds, dayLabel, localTimes } from "@/lib/time";
import { sameChat } from "@/lib/unread";
import { cn } from "@/lib/utils";
import { useVoice } from "@/lib/voice";
import { Markdown } from "./Markdown";

const tooManyImages = `A message has at most ${maxImages} images.`;

// The wait after each failed send. The last wait repeats.
const retryDelays = [2000, 5000, 10_000, 30_000];

type Waiting = Queued & { sent?: boolean };
type Failure = { id: string; error: string; retryAt: number };

function newMessageId() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function upsert(list: ChatMessage[], message: ChatMessage) {
  const known = list.find((other) => other.id === message.id);
  // The agent only adds text to a message, so the longer text is the newer text. A message never loses a time.
  if (
    known &&
    (known.text.length > message.text.length ||
      (known.deliveredAt && !message.deliveredAt) ||
      (known.stoppedAt && !message.stoppedAt))
  ) {
    return list;
  }
  return [...list.filter((other) => other.id !== message.id), message].toSorted(
    (a, b) => a.id - b.id,
  );
}

const countsAsUnread = (message: ChatMessage) =>
  message.author !== "Owner" && message.author !== "Event";

function OwnerState({ message }: { message: ChatMessage }) {
  if (message.stoppedAt) {
    const label = `Stopped, the agent did not get it, at ${clock(message.stoppedAt)}`;
    return (
      <span role="img" aria-label={label} title={label}>
        <BanIcon className="size-3.5" />
      </span>
    );
  }
  if (message.deliveredAt) {
    const label = `Delivered to the agent at ${clock(message.deliveredAt)}`;
    return (
      <span role="img" aria-label={label} title={label}>
        <CheckCheckIcon className="size-3.5" />
      </span>
    );
  }
  return (
    <span role="img" aria-label="Stored, waits for the agent" title="Stored, waits for the agent">
      <CheckIcon className="size-3.5" />
    </span>
  );
}

const Message = memo(function Message({ message }: { message: ChatMessage }) {
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
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span>{message.author === "tell_owner" ? "Lead" : message.author}</span>
        <span>{clock(message.time)}</span>
        {message.author === "Owner" && <OwnerState message={message} />}
      </div>
      {event && body ? (
        <Collapsible>
          <CollapsibleTrigger className="group flex w-full min-w-0 items-start gap-1 text-left break-words">
            <ChevronRightIcon className="mt-0.5 size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
            <span className="min-w-0">{localTimes(summary)}</span>
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
});

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

function FileImage({ file, alt, className }: { file: File; alt: string; className: string }) {
  const show = useCallback(
    (image: HTMLImageElement) => {
      const url = URL.createObjectURL(file);
      image.src = url;
      return () => URL.revokeObjectURL(url);
    },
    [file],
  );
  return <img ref={show} alt={alt} className={className} />;
}

function QueuedMessage({
  item,
  failure,
  deletable,
  retry,
  remove,
}: {
  item: Waiting;
  failure?: Failure;
  deletable: boolean;
  retry: () => void;
  remove: () => void;
}) {
  return (
    <div
      data-queued={item.id}
      className="grid max-w-[85%] grid-cols-[minmax(0,1fr)] gap-1 justify-self-end rounded-xl border border-transparent bg-secondary px-3 py-2"
    >
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span>Owner</span>
        <span>{clock(item.time)}</span>
        {failure ? (
          <span
            role="img"
            aria-label="Send failed"
            title="Send failed"
            className="text-destructive"
          >
            <CircleAlertIcon className="size-3.5" />
          </span>
        ) : (
          <span role="img" aria-label="Not sent yet" title="Not sent yet">
            <ClockIcon className="size-3.5" />
          </span>
        )}
      </div>
      {item.text && <Markdown text={item.text} />}
      {item.images.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {item.images.map((file, position) => (
            <FileImage
              key={position}
              file={file}
              alt={`Picture ${position + 1} of an unsent message`}
              className="max-h-48 max-w-full rounded-lg border object-contain"
            />
          ))}
        </div>
      )}
      {failure && (
        <p role="alert" className="grid gap-0.5 text-xs text-destructive">
          <span>{failure.error}</span>
          <span>Next try at {clockSeconds(failure.retryAt)}</span>
        </p>
      )}
      {(failure || deletable) && (
        <div className="flex gap-2">
          {failure && (
            <Button type="button" variant="outline" size="sm" onClick={retry}>
              Retry now
            </Button>
          )}
          {deletable && (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label="Delete the message"
              title="Delete the message"
              onClick={remove}
            >
              <Trash2Icon />
            </Button>
          )}
        </div>
      )}
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
  brief?: Workstream;
  note?: ReactNode;
  footer?: ReactNode;
}) {
  const showLogin = use(LoginContext);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loaded, setLoaded] = useState(false);
  // older is true when the chat has messages before the first loaded message.
  const [older, setOlder] = useState(false);
  const [harness, setHarness] = useState("");
  const [writing, setWriting] = useState(false);
  const [failure, setFailure] = useState("");
  const [error, setError] = useState<string>();
  const [text, setText] = useState("");
  const [images, setImages] = useState<File[]>([]);
  const [sendError, setSendError] = useState("");
  const [stopping, setStopping] = useState(false);
  // The messages that the server did not store, or stored but the list does not have yet.
  const [queue, setQueue] = useState<Waiting[]>([]);
  const [queueLoaded, setQueueLoaded] = useState(false);
  // The last failed send of the first message that the server did not store.
  const [attempt, setAttempt] = useState<Failure>();
  // Ends the wait before the next try of the first message.
  const skipWait = useRef<() => void>(undefined);
  // Two taps on Send in one turn of the page both come before the next render. Thus only the ref stops a second
  // message. The next render has the empty input and clears the ref.
  const justSent = useRef(false);
  useEffect(() => {
    justSent.current = false;
  });
  const imagesRef = useRef<File[]>([]);
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
  const loadingOlder = useRef(false);
  // The id of the last message that the list got, to find the messages that came while the connection was down.
  const newest = useRef<number>(undefined);
  // The place of a message on the screen before the list gets older messages at its start.
  const anchor = useRef<{ id: number; offset: number }>(undefined);
  // The place of the voice text in the field. value and cursor describe what the voice input wrote last.
  const voiceInsert = useRef<{ lead: string; trail: string; value: string; cursor: number }>(
    undefined,
  );
  const voice = useVoice(
    (voiceText, first) => {
      const field = input.current;
      if (!field) {
        return;
      }
      let insert = first ? undefined : voiceInsert.current;
      if (!voiceText) {
        return;
      }
      if (!insert) {
        const focused = document.activeElement === field;
        const start = focused ? field.selectionStart : field.value.length;
        const end = focused ? field.selectionEnd : field.value.length;
        const before = field.value.slice(0, start);
        const after = field.value.slice(end);
        insert = {
          lead: before === "" || /\s$/.test(before) ? before : `${before} `,
          trail: after === "" || /^\s/.test(after) ? after : ` ${after}`,
          value: "",
          cursor: 0,
        };
      }
      const value = insert.lead + voiceText + insert.trail;
      const cursor = insert.lead.length + voiceText.length;
      voiceInsert.current = { ...insert, value, cursor };
      flushSync(() => setText(value));
      // The height of the text up to the end of the voice text gives the scroll position. setSelectionRange does not
      // scroll a field without the focus, and WebKit on iOS does not scroll it reliably.
      field.value = insert.lead + voiceText;
      const top = field.scrollHeight - field.clientHeight;
      field.value = value;
      field.setSelectionRange(cursor, cursor);
      field.scrollTop = top;
    },
    () => {
      const field = input.current;
      const insert = voiceInsert.current;
      return (
        !!field &&
        !!insert &&
        (field.value !== insert.value ||
          field.selectionStart !== insert.cursor ||
          field.selectionEnd !== insert.cursor)
      );
    },
  );

  const fetchPage = useCallback(
    async (before?: number): Promise<Chat | undefined> => {
      const res = await getChat({ organization, repository, workstream, before });
      if (res.status === 200) {
        return res.data.data;
      }
      if (res.status === 401) {
        showLogin();
      } else {
        setError(res.data.error);
      }
    },
    [organization, repository, workstream, showLogin],
  );

  // The first load gets the newest page. A load after a reconnect gets pages until one has the last message that the
  // list got before.
  const load = useCallback(async () => {
    const known = newest.current;
    const newestPage = await fetchPage();
    if (!newestPage) {
      return;
    }
    const pages = [newestPage];
    if (known !== undefined) {
      while (pages[0].older && pages[0].messages[0].id > known) {
        const page = await fetchPage(pages[0].messages[0].id);
        if (!page) {
          return;
        }
        pages.unshift(page);
      }
    }
    const added = pages.flatMap((page) => page.messages);
    newest.current = Math.max(newest.current ?? 0, ...added.map((message) => message.id));
    setMessages((list) => added.reduce(upsert, list));
    if (known === undefined) {
      setOlder(newestPage.older);
    }
    setHarness(newestPage.harness);
    setWriting(newestPage.writing);
    if (!newestPage.writing) {
      setStopping(false);
    }
    setLoaded(true);
  }, [fetchPage]);

  const loadOlder = useCallback(() => {
    const list = listRef.current;
    if (!list || loadingOlder.current) {
      return;
    }
    const first = messages[0].id;
    loadingOlder.current = true;
    fetchPage(first)
      .then((page) => {
        if (!page) {
          return;
        }
        const element = list.querySelector(`[data-message="${first}"]`);
        if (element && scrolledCount.current !== undefined) {
          anchor.current = {
            id: first,
            offset: element.getBoundingClientRect().top - list.getBoundingClientRect().top,
          };
        }
        setMessages((current) => page.messages.reduce(upsert, current));
        setOlder(page.older);
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => {
        loadingOlder.current = false;
      });
  }, [fetchPage, messages]);

  // A message that comes while the connection is down is lost, so each connection reads the chat.
  useEffect(() => {
    if (!source) {
      return;
    }
    const key = { organization, repository, workstream };
    const reload = () => {
      load().catch((err: unknown) => setError(String(err)));
    };
    reload();
    source.addEventListener("open", reload);
    const removeMessage = onEvent<LiveEvents, "message">(source, "message", (message) => {
      if (sameChat(message, key)) {
        newest.current = Math.max(newest.current ?? 0, message.id);
        // A message before the first loaded message would leave a gap.
        setMessages((list) =>
          list.length > 0 && message.id < list[0].id ? list : upsert(list, message),
        );
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
      source.removeEventListener("open", reload);
      removeMessage();
      removeState();
    };
  }, [source, load, organization, repository, workstream]);

  const storedIds = useMemo(
    () => new Set(messages.map((message) => message.browserId)),
    [messages],
  );
  const pending = queue.filter((item) => !storedIds.has(item.id));
  const count = messages.length + pending.length;
  const next = pending.find((item) => !item.sent);

  useEffect(() => {
    listQueued({ organization, repository, workstream })
      .then((stored) =>
        setQueue((current) => [
          ...stored.filter((item) => !current.some((other) => other.id === item.id)),
          ...current,
        ]),
      )
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setQueueLoaded(true));
  }, [organization, repository, workstream]);

  useEffect(() => {
    const stored = queue.filter((item) => storedIds.has(item.id));
    if (stored.length === 0) {
      return;
    }
    for (const item of stored) {
      removeQueued(item.id)
        .then(() => setQueue((current) => current.filter((other) => other.id !== item.id)))
        .catch((err: unknown) => setError(String(err)));
    }
  }, [queue, storedIds]);

  // The effect sends the first message that the server did not store, and tries again until the server stores it.
  // A stored message leaves the browser store at once. It stays on the screen until the load that follows ends.
  useEffect(() => {
    if (!queueLoaded || !next) {
      return;
    }
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const deliver = async () => {
      for (let failures = 0; ; failures++) {
        setAttempt(undefined);
        let failed: string | undefined;
        try {
          const res = await postQueued(next, controller.signal);
          failed = res.error;
          if (res.status === 401) {
            showLogin();
          }
        } catch (err) {
          failed = String(err);
        }
        if (controller.signal.aborted) {
          return;
        }
        if (failed === undefined) {
          removeQueued(next.id).catch((err: unknown) => setError(String(err)));
          setQueue((current) =>
            current.map((item) => (item.id === next.id ? { ...item, sent: true } : item)),
          );
          load()
            .catch((err: unknown) => setError(String(err)))
            .finally(() => setQueue((current) => current.filter((item) => item.id !== next.id)));
          return;
        }
        const delay = retryDelays[Math.min(failures, retryDelays.length - 1)];
        setAttempt({ id: next.id, error: failed, retryAt: Date.now() + delay });
        await new Promise<void>((resolve) => {
          timer = setTimeout(resolve, delay);
          skipWait.current = resolve;
        });
      }
    };
    void deliver();
    return () => {
      controller.abort();
      clearTimeout(timer);
      skipWait.current = undefined;
    };
  }, [queueLoaded, next, load, showLogin, skipWait]);

  // The count of unread messages when the chat opened. The first scroll and the mark as seen wait until the list has
  // the first unread message.
  const [openUnread, setOpenUnread] = useState<number>();
  if (openUnread === undefined && unread !== undefined) {
    setOpenUnread(unread);
  }
  const waiting =
    older && openUnread !== undefined && messages.filter(countsAsUnread).length < openUnread;
  const lastAgentMessage = messages.findLast((message) => message.author !== "Owner")?.id;
  useEffect(() => {
    if (!waiting && unread && lastAgentMessage !== undefined) {
      // A failed call keeps the count, and the next message of the agent calls again.
      seeChat({
        organization,
        repository,
        workstream,
        message: lastAgentMessage,
      }).catch(() => {});
    }
  }, [waiting, unread, lastAgentMessage, organization, repository, workstream]);

  // The first scroll waits for the older pages up to the first unread message. Later, the list gets the page before
  // its first message when the Owner is less than one list height from the top.
  useEffect(() => {
    const list = listRef.current;
    if (!list || !loaded || !older) {
      return;
    }
    if (waiting || (openUnread !== undefined && list.scrollTop < list.clientHeight)) {
      loadOlder();
    }
  }, [loaded, older, waiting, openUnread, loadOlder]);

  // The first scroll shows the first unread message, or the end. Then a new message scrolls to the end, and a longer
  // last message scrolls only while the Owner is at the end.
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || !loaded || !queueLoaded || unread === undefined) {
      return;
    }
    if (scrolledCount.current === undefined) {
      if (waiting) {
        return;
      }
      const unreadMessages = messages.filter(countsAsUnread);
      scrolledCount.current = count;
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
    if (anchor.current) {
      const element = list.querySelector(`[data-message="${anchor.current.id}"]`);
      if (element) {
        list.scrollTop +=
          element.getBoundingClientRect().top -
          list.getBoundingClientRect().top -
          anchor.current.offset;
      }
      anchor.current = undefined;
      scrolledCount.current = count;
      return;
    }
    if (count > scrolledCount.current) {
      pinned.current = true;
    }
    scrolledCount.current = count;
    if (pinned.current) {
      scrollToEnd(list);
    }
  });

  // The ref has the images at once, so two calls that wait for fitImage count the images of each other.
  const changeImages = (change: (current: File[]) => File[]) => {
    imagesRef.current = change(imagesRef.current);
    setImages(imagesRef.current);
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
  const buttonPending = showStop && stopping;

  const send = () => {
    if (empty || justSent.current) {
      return;
    }
    const item: Queued = {
      id: newMessageId(),
      organization,
      repository,
      workstream,
      text,
      images: imagesRef.current,
      time: new Date().toISOString(),
    };
    voice.abort();
    justSent.current = true;
    setText("");
    changeImages(() => []);
    setSendError("");
    setQueue((current) => [...current, item]);
    addQueued(item).catch((err: unknown) => setSendError(String(err)));
  };

  const discard = (item: Waiting) => {
    removeQueued(item.id)
      .then(() => setQueue((current) => current.filter((other) => other.id !== item.id)))
      .catch((err: unknown) => setError(String(err)));
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
      <header className="hidden min-h-14 items-center gap-2 border-b px-4 py-2 md:flex">
        {head}
        <span className="grow" />
        {harness && (
          <span className="truncate text-sm text-muted-foreground">
            {agent}: {harness}
          </span>
        )}
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
          if (
            older &&
            scrolledCount.current !== undefined &&
            event.currentTarget.scrollTop < event.currentTarget.clientHeight
          ) {
            loadOlder();
          }
        }}
        // An image has no height before it loads, and its load moves the end of the list.
        onLoadCapture={(event) => {
          if (pinned.current) {
            scrollToEnd(event.currentTarget);
          }
        }}
        className="grid min-h-0 grow grid-cols-[minmax(0,1fr)] content-start gap-3 overflow-y-auto overscroll-contain bg-muted/40 p-4"
      >
        {error && <Badge variant="destructive">{error}</Badge>}
        {loaded && queueLoaded && count === 0 && (
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
        {pending.map((item, index) => {
          const previous = index === 0 ? messages.at(-1) : pending[index - 1];
          const label = dayLabel(item.time);
          return (
            <Fragment key={item.id}>
              {(!previous || dayLabel(previous.time) !== label) && <DaySeparator label={label} />}
              <QueuedMessage
                item={item}
                failure={attempt?.id === item.id ? attempt : undefined}
                deletable={!item.sent && (item !== next || attempt?.id === item.id)}
                retry={() => skipWait.current?.()}
                remove={() => discard(item)}
              />
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
            // Safari on iOS scrolls the page when the keyboard opens, and it can keep that offset after the keyboard closes.
            onBlur={() => window.scrollTo(0, 0)}
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
