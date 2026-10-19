import {
  BanIcon,
  CheckCheckIcon,
  CheckIcon,
  ChevronRightIcon,
  CircleAlertIcon,
  ClockIcon,
  Trash2Icon,
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
import {
  type ActionMessage,
  type Approval,
  type Decision,
  type PendingAction,
} from "@/lib/actions";
import { chatDraftKey } from "@/lib/draft";
import { onEvent } from "@/lib/events";
import { LoginContext } from "@/lib/login";
import {
  addQueued,
  listQueued,
  newMessageId,
  postQueued,
  removeQueued,
  type Queued,
} from "@/lib/outbox";
import { atEnd } from "@/lib/scroll";
import { clock, clockSeconds, dayClock, dayLabel, localTimes } from "@/lib/time";
import { useShell } from "@/lib/shell";
import { sameChat } from "@/lib/unread";
import { cn } from "@/lib/utils";
import { ActionCard } from "./ActionCard";
import { ActionTray, DecisionInput, type Choice } from "./ActionTray";
import { AnswerPanel } from "./AnswerPanel";
import { FileImage } from "./FileImage";
import { Markdown } from "./Markdown";
import { MessageInput } from "./MessageInput";

// The wait after each failed send. The last wait repeats.
const retryDelays = [2000, 5000, 10_000, 30_000];

type Waiting = Queued & { sent?: boolean };
type Failure = { id: string; error: string; retryAt: number };

function upsert(list: ActionMessage[], message: ActionMessage) {
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

const Message = memo(function Message({
  message,
  opened,
}: {
  message: ActionMessage;
  opened: boolean;
}) {
  if (message.action) {
    return <ActionCard message={message} action={message.action} opened={opened} />;
  }
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
      {message.author === "Owner" && !message.deliveredAt && !message.stoppedAt && (
        <span className="text-xs text-muted-foreground">Waits for the agent</span>
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

// The chat of the agent: a Lead chat, or the Coordinator chat with the empty repository and the Workstream 0. unread is
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
  approvals = [],
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
  approvals?: Approval[];
}) {
  const showLogin = use(LoginContext);
  const { leaveInbox } = useShell();
  const [messages, setMessages] = useState<ActionMessage[]>([]);
  const [loaded, setLoaded] = useState(false);
  // older is true when the chat has messages before the first loaded message.
  const [older, setOlder] = useState(false);
  const [harness, setHarness] = useState("");
  const [writing, setWriting] = useState(false);
  const [stoppable, setStoppable] = useState(false);
  const [pausedUntil, setPausedUntil] = useState<string | null>(null);
  const [failure, setFailure] = useState("");
  const [error, setError] = useState<string>();
  const [sendError, setSendError] = useState("");
  const [stopping, setStopping] = useState(false);
  // The messages that the server did not store, or stored but the list does not have yet.
  const [queue, setQueue] = useState<Waiting[]>([]);
  const [queueLoaded, setQueueLoaded] = useState(false);
  // The last failed send of the first message that the server did not store.
  const [attempt, setAttempt] = useState<Failure>();
  // Ends the wait before the next try of the first message.
  const skipWait = useRef<() => void>(undefined);
  const [briefOpen, setBriefOpen] = useState<boolean>();
  const [choice, setChoice] = useState<Choice>();
  // The actions after a decision of the Owner, by action id. They show at once, before the list gets the new state.
  const [outcomes, setOutcomes] = useState<Record<number, PendingAction>>({});
  // How many times the Owner pressed Show on a card. A card with a new count opens its values.
  const [reveals, setReveals] = useState<Record<number, number>>({});
  const [shownItem, setShownItem] = useState<Approval>();
  const listRef = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  // The scrollTop of the last scroll to the end. A scroll event can come after an image has made the list longer.
  const endTop = useRef<number>(undefined);
  const scrollToEnd = useCallback((list: HTMLElement) => {
    list.scrollTop = list.scrollHeight;
    endTop.current = list.scrollTop;
  }, []);
  // The number of messages at the last scroll, or undefined before the first scroll.
  const scrolledCount = useRef<number>(undefined);
  const loadingOlder = useRef(false);
  // The id of the last message that the list got, to find the messages that came while the connection was down.
  const newest = useRef<number>(undefined);
  // The place of a message on the screen before the list gets older messages at its start.
  const anchor = useRef<{ id: number; offset: number }>(undefined);
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
    setStoppable(newestPage.stoppable);
    setPausedUntil(newestPage.pausedUntil);
    if (!newestPage.stoppable) {
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
        setStoppable(state.stoppable);
        setPausedUntil(state.pausedUntil);
        if (!state.stoppable) {
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
    if (!list || !loaded || !queueLoaded || !older) {
      return;
    }
    if (waiting || (openUnread !== undefined && list.scrollTop < list.clientHeight)) {
      loadOlder();
    }
  }, [loaded, queueLoaded, older, waiting, openUnread, loadOlder]);

  // The list gets shorter when the input gets taller, and the end of the list must stay in view.
  useEffect(() => {
    const list = listRef.current;
    if (!list) {
      return;
    }
    const observer = new ResizeObserver(() => {
      if (pinned.current) {
        scrollToEnd(list);
      }
    });
    observer.observe(list);
    return () => observer.disconnect();
  }, [scrollToEnd]);

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

  const send = (text: string, images: File[]) => {
    const item: Queued = {
      id: newMessageId(),
      organization,
      repository,
      workstream,
      text,
      images,
      time: new Date().toISOString(),
    };
    setQueue((current) => [...current, item]);
    addQueued(item).catch((err: unknown) => setSendError(String(err)));
  };

  const rows = approvals
    .map((item) => ({ ...item, action: outcomes[item.action.id] ?? item.action }))
    .filter((item) => item.action.state === "pending")
    .toSorted((a, b) => a.id - b.id);
  const selected = choice && rows.find((item) => item.id === choice.id);

  const choose = (id: number, decision: Decision) => {
    setSendError("");
    setChoice(choice?.id === id && choice.decision === decision ? undefined : { id, decision });
  };

  const showAction = (item: Approval) => {
    const id = item.action.id;
    const list = listRef.current;
    if (!list || !messages.some((message) => message.action?.id === id)) {
      setShownItem(item);
      return;
    }
    flushSync(() => setReveals((current) => ({ ...current, [id]: (current[id] ?? 0) + 1 })));
    list.querySelector(`[data-action="${id}"]`)?.scrollIntoView({ block: "start" });
  };

  const decided = (item: Approval, action: PendingAction) => {
    setOutcomes((current) => ({ ...current, [action.id]: action }));
    if (action.state !== "pending") {
      setChoice(undefined);
      leaveInbox(item.id);
    }
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
              <Message
                key={`${message.id}:${message.action ? (reveals[message.action.id] ?? 0) : 0}`}
                message={
                  message.action && outcomes[message.action.id]
                    ? { ...message, action: outcomes[message.action.id] }
                    : message
                }
                opened={message.action ? (reveals[message.action.id] ?? 0) > 0 : false}
              />
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
            {pausedUntil
              ? `The ${agent} waits for the usage limit until ${dayClock(pausedUntil)}.`
              : messages.some(
                    (message) =>
                      message.author === "Owner" && !message.deliveredAt && !message.stoppedAt,
                  )
                ? `The message waits for the ${agent}.`
                : `The ${agent} writes a reply.`}
          </p>
        )}
        {failure && (
          <Badge variant="destructive" className="h-auto w-full justify-start whitespace-normal">
            The chat session failed: {failure}
          </Badge>
        )}
      </div>
      {footer}
      <ActionTray rows={rows} choice={choice} choose={choose} show={showAction} />
      {choice && selected ? (
        <DecisionInput
          action={selected.action}
          title={selected.title}
          decision={choice.decision}
          error={sendError}
          onError={setSendError}
          clear={() => setChoice(undefined)}
          decided={(action) => decided(selected, action)}
        />
      ) : (
        <MessageInput
          draftKey={chatDraftKey(organization, repository, workstream)}
          label={`Message to the ${agent}`}
          placeholder={`Write to the ${agent}`}
          images
          error={sendError}
          onError={setSendError}
          stopping={stopping}
          onStop={stoppable ? stop : undefined}
          onSend={send}
        />
      )}
      {shownItem && (
        <AnswerPanel
          item={shownItem}
          input={false}
          workstream={brief?.title ?? agent}
          onClose={() => setShownItem(undefined)}
        />
      )}
    </section>
  );
}
