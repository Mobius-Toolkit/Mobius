import { ArrowDownIcon, ChevronRightIcon } from "lucide-react";
import { use, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  getTranscript,
  listActiveAgents,
  startCheck,
  type ActiveAgent,
  type ActiveAgents,
  type Agent,
  type LiveEvents,
  type TranscriptEvents,
  type TranscriptLine,
} from "@/api/api.gen";
import { ErrorBadge, inset, List, PageHeader, rowClass, Section } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { lowLoadReason, queueState, queueText } from "@/lib/agents";
import { onEvent, retryDelay } from "@/lib/events";
import { LoginContext } from "@/lib/login";
import { atEnd } from "@/lib/scroll";
import { clock, dayClock, localTimes } from "@/lib/time";
import { cn } from "@/lib/utils";
import { BackButton } from "./BackButton";
import { ConnectionBadge } from "./Frame";
import { TopBar } from "./TopBar";

function numbered(number: number, title?: string | null) {
  return title ? `#${number} ${title}` : `#${number}`;
}

function AgentRow({ row, onOpen }: { row: ActiveAgent; onOpen: (agent: Agent) => void }) {
  const agent = row.agent;
  const state = queueState(agent.queueReason);
  return (
    <li>
      <button
        type="button"
        onClick={() => onOpen(agent)}
        className={cn(rowClass, "w-full gap-3 text-left hover:bg-muted")}
      >
        <span className={cn("size-2 shrink-0 rounded-full", state.dot)} />
        <span className="grid min-w-0 grow gap-0.5">
          <span>
            {agent.name} {agent.title}
          </span>
          <span className="text-sm text-muted-foreground">
            {[
              agent.role,
              agent.organization,
              (agent.workstream !== 0 || agent.issue != null) && agent.repository,
              dayClock(agent.startedAt),
              queueText(agent),
            ]
              .filter(Boolean)
              .join(" · ")}
          </span>
          {agent.workstream !== 0 && (
            <span className="truncate text-sm text-muted-foreground">
              Workstream {numbered(agent.workstream, row.workstreamTitle)}
            </span>
          )}
          {agent.issue != null && (
            <span className="truncate text-sm text-muted-foreground">
              Ticket {numbered(agent.issue, row.issueTitle)}
            </span>
          )}
          {row.pullRequest != null && (
            <span className="text-sm text-muted-foreground">Pull request #{row.pullRequest}</span>
          )}
        </span>
        {state.badge && <Badge variant="outline">{state.badge}</Badge>}
        <ChevronRightIcon className="size-4 shrink-0" />
      </button>
    </li>
  );
}

function TranscriptEntry({ line }: { line: TranscriptLine }) {
  const [open, setOpen] = useState(!line.folded);
  const [raw, setRaw] = useState(false);
  return (
    <li
      data-line={line.id}
      className={cn(
        "grid max-w-[85%] grid-cols-[minmax(0,1fr)] gap-1 rounded-xl border px-3 py-2",
        line.kind === "prompt"
          ? "justify-self-end border-transparent bg-secondary"
          : "justify-self-start bg-card",
        line.error && "text-destructive",
      )}
    >
      <div className="flex gap-2 text-xs text-muted-foreground">
        <span className="font-mono">{line.kind}</span>
        <span>{clock(line.time)}</span>
      </div>
      <span className="break-words">
        {localTimes(line.text)}
        {line.harnessToolName && (
          <span className="text-sm text-muted-foreground"> {line.harnessToolName}</span>
        )}
      </span>
      <span className="flex gap-1">
        {line.folded && line.body && (
          <Button variant="ghost" size="xs" onClick={() => setOpen(!open)}>
            {open ? "Hide" : "Show"}
          </Button>
        )}
        <Button variant="ghost" size="xs" onClick={() => setRaw(!raw)}>
          Raw
        </Button>
      </span>
      {open && line.body && (
        <pre className="overflow-x-auto rounded-md bg-muted p-2 text-xs whitespace-pre-wrap">
          {localTimes(line.body)}
        </pre>
      )}
      {raw && <pre className="overflow-x-auto rounded-md bg-muted p-2 text-xs">{line.raw}</pre>}
    </li>
  );
}

function StartCheckButton({ agent }: { agent: Agent }) {
  return agent.queueReason === lowLoadReason ? <StartCheckControl id={agent.id} /> : null;
}

// The state of this control ends with the wait, because the control unmounts when the reason changes.
function StartCheckControl({ id }: { id: number }) {
  const showLogin = use(LoginContext);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const start = () => {
    setStarting(true);
    startCheck(id)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 204) {
          setError("");
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setStarting(false));
  };
  return (
    <>
      <Button variant="outline" disabled={starting} onClick={start}>
        {starting && <Spinner aria-hidden />}
        Start the check now
      </Button>
      {error && (
        <Badge variant="destructive" className="h-auto justify-start whitespace-normal">
          {error}
        </Badge>
      )}
    </>
  );
}

function merge(list: TranscriptLine[], added: TranscriptLine[]) {
  const lines = new Map(list.map((line) => [line.id, line]));
  for (const line of added) {
    const known = lines.get(line.id);
    // The agent only adds text to a chunk, so the longer raw text is the newer text.
    if (!known || known.raw.length < line.raw.length) {
      lines.set(line.id, line);
    }
  }
  return [...lines.values()].toSorted((a, b) => a.id - b.id);
}

function TranscriptLog({
  agent,
  source,
  className,
}: {
  agent: Agent;
  source?: EventSource;
  className?: string;
}) {
  const showLogin = use(LoginContext);
  const [lines, setLines] = useState<TranscriptLine[]>();
  // older is true when the session has rows before the first loaded line.
  const [older, setOlder] = useState(false);
  const [error, setError] = useState<string>();
  const [behind, setBehind] = useState(false);
  const [opened, setOpened] = useState<EventSource>();
  const listRef = useRef<HTMLUListElement>(null);
  const pinned = useRef(true);
  const loadingOlder = useRef(false);
  // The id of the last line that the log got, to find the lines that came while the connection was down.
  const newest = useRef<number>(undefined);
  // The place of a line on the screen before the log gets older lines at its start.
  const anchor = useRef<{ id: number; offset: number }>(undefined);

  const fetchPage = useCallback(
    async (before?: number) => {
      const res = await getTranscript(agent.id, { before });
      if (res.status === 200) {
        return res.data.data;
      }
      if (res.status === 401) {
        showLogin();
      } else {
        setError(res.data.error);
      }
    },
    [agent.id, showLogin],
  );

  // The first load gets the newest page. A load after a reconnect gets pages until one has the last line that the log
  // got before.
  const load = useCallback(async () => {
    const known = newest.current;
    const newestPage = await fetchPage();
    if (!newestPage) {
      return;
    }
    const pages = [newestPage];
    if (known !== undefined) {
      while (pages[0].older && pages[0].lines[0].id > known) {
        const page = await fetchPage(pages[0].lines[0].id);
        if (!page) {
          return;
        }
        pages.unshift(page);
      }
    }
    const added = pages.flatMap((page) => page.lines);
    if (added.length > 0) {
      newest.current = Math.max(newest.current ?? 0, ...added.map((line) => line.id));
    }
    setLines((list) => merge(list ?? [], added));
    if (known === undefined) {
      setOlder(newestPage.older);
    }
  }, [fetchPage]);

  const loadOlder = useCallback(() => {
    const list = listRef.current;
    const first = lines?.[0];
    if (!list || !first || loadingOlder.current) {
      return;
    }
    loadingOlder.current = true;
    fetchPage(first.id)
      .then((page) => {
        if (!page) {
          return;
        }
        const element = list.querySelector(`[data-line="${first.id}"]`);
        if (element) {
          anchor.current = {
            id: first.id,
            offset: element.getBoundingClientRect().top - list.getBoundingClientRect().top,
          };
        }
        setLines((current) => merge(current ?? [], page.lines));
        setOlder(page.older);
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => {
        loadingOlder.current = false;
      });
  }, [fetchPage, lines]);

  // A line that comes while the connection is down is lost, so each connection reads the log. The stream opens again
  // with each new source, so a reconnect of the live events also reconnects the log.
  useEffect(() => {
    const reload = () => {
      load().catch((err: unknown) => setError(String(err)));
    };
    if (!source) {
      reload();
      return;
    }
    let stream: EventSource;
    let timer: ReturnType<typeof setTimeout>;
    // The browser connects again by itself only while the stream is CONNECTING. A stream that is CLOSED has an
    // answer that is not an event stream, so the log connects again after a delay.
    const connect = () => {
      const next = new EventSource(`/api/agents/${agent.id}/transcript/events`);
      next.addEventListener("open", () => {
        setOpened(source);
        reload();
      });
      next.addEventListener("error", () => {
        setOpened(undefined);
        if (next.readyState === EventSource.CLOSED) {
          timer = setTimeout(() => {
            reload();
            stream = connect();
          }, retryDelay);
        }
      });
      onEvent<TranscriptEvents, "transcript">(next, "transcript", (line) => {
        newest.current = Math.max(newest.current ?? 0, line.id);
        // A line before the first loaded line would leave a gap.
        setLines((list) =>
          list && list.length > 0 && line.id < list[0].id ? list : merge(list ?? [], [line]),
        );
      });
      return next;
    };
    stream = connect();
    return () => {
      clearTimeout(timer);
      stream.close();
    };
  }, [source, load, agent.id]);

  // The log gets the page before its first line when the Owner is less than one log height from the top.
  useEffect(() => {
    const list = listRef.current;
    if (list && older && list.scrollTop < list.clientHeight) {
      loadOlder();
    }
  }, [older, loadOlder]);

  // New data scrolls the log to the end while the Owner is at the end. Else the log stays, and the button shows.
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || !lines) {
      return;
    }
    if (anchor.current) {
      const element = list.querySelector(`[data-line="${anchor.current.id}"]`);
      if (element) {
        list.scrollTop +=
          element.getBoundingClientRect().top -
          list.getBoundingClientRect().top -
          anchor.current.offset;
      }
      anchor.current = undefined;
    } else if (pinned.current) {
      list.scrollTop = list.scrollHeight;
    } else {
      setBehind(true);
    }
  }, [lines]);

  return (
    <div className={cn("flex min-h-0 grow flex-col gap-4", className)}>
      {error && <Badge variant="destructive">{error}</Badge>}
      <div className="relative flex min-h-10 grow flex-col">
        <ul
          ref={listRef}
          onScroll={(event) => {
            pinned.current = atEnd(event.currentTarget);
            if (pinned.current) {
              setBehind(false);
            }
            if (older && event.currentTarget.scrollTop < event.currentTarget.clientHeight) {
              loadOlder();
            }
          }}
          className="grid min-h-0 grow grid-cols-[minmax(0,1fr)] content-start gap-2 overflow-y-auto"
        >
          {lines?.map((line) => (
            <TranscriptEntry key={line.id} line={line} />
          ))}
        </ul>
        {source && opened !== source && (
          <div className="absolute inset-x-0 top-0">
            <ConnectionBadge connection="connecting" />
          </div>
        )}
        {behind && (
          <Button
            variant="secondary"
            size="sm"
            className="absolute bottom-2 left-1/2 -translate-x-1/2 rounded-full shadow-md"
            onClick={() => {
              const list = listRef.current;
              if (list) {
                list.scrollTop = list.scrollHeight;
              }
              pinned.current = true;
              setBehind(false);
            }}
          >
            <ArrowDownIcon />
            New messages
          </Button>
        )}
      </div>
      <p className="text-sm text-muted-foreground">Read only. The Owner talks only to the Lead.</p>
    </div>
  );
}

export function Transcript({
  agent,
  source,
  onClose,
}: {
  agent: Agent;
  source?: EventSource;
  onClose: () => void;
}) {
  return (
    <Card className="min-h-0 max-md:max-h-[calc(100svh-10rem)] md:max-h-[calc(100svh-3rem)]">
      <CardHeader className="flex items-center gap-2">
        <BackButton parent="/agents" onBack={onClose} />
        <CardTitle className="line-clamp-2 min-w-0 grow">
          {agent.name} {agent.title}
        </CardTitle>
      </CardHeader>
      <div className="flex flex-wrap items-center gap-2 px-(--card-spacing) empty:hidden">
        <StartCheckButton agent={agent} />
      </div>
      <CardContent className="flex min-h-0 grow flex-col">
        <TranscriptLog agent={agent} source={source} />
      </CardContent>
    </Card>
  );
}

export function Agents({ source }: { source?: EventSource }) {
  const showLogin = use(LoginContext);
  const [agents, setAgents] = useState<ActiveAgents>();
  const [error, setError] = useState<string>();
  const [selected, setSelected] = useState<Agent>();
  const newest = useRef(0);

  // The list requests can finish in another order than they start. Only the answer to the newest request counts.
  const load = useCallback(() => {
    const request = ++newest.current;
    listActiveAgents()
      .then((res) => {
        if (request !== newest.current) {
          return;
        }
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setAgents(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [showLogin]);

  // An agent event that comes before the listener or while the connection is down is lost, so each connection reads
  // the list.
  useEffect(() => {
    load();
    if (!source) {
      return;
    }
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "agent">(source, "agent", load);
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source, load]);

  const live = agents?.groups
    .flatMap((group) => group.agents)
    .find((row) => row.agent.id === selected?.id)?.agent;
  return (
    <>
      <TopBar
        title={selected ? `${selected.name} ${selected.title}` : "Agents"}
        back="/settings"
        onBack={selected && (() => setSelected(undefined))}
      />
      {selected ? (
        <>
          <PageHeader
            title={`${selected.name} ${selected.title}`}
            back="/agents"
            onBack={() => setSelected(undefined)}
          />
          <div className={cn(inset, "flex flex-wrap items-center gap-2 empty:hidden")}>
            {live && <StartCheckButton agent={live} />}
          </div>
          <TranscriptLog
            agent={selected}
            source={source}
            className={cn(
              inset,
              "max-md:max-h-[calc(100svh-10rem)] md:max-h-[calc(100svh-9.875rem)]",
            )}
          />
        </>
      ) : (
        <>
          <PageHeader title="Agents" />
          {error && <ErrorBadge>{error}</ErrorBadge>}
          {agents?.groups.map((group) => (
            <Section key={group.name} title={`${group.name} ${group.count} / ${group.max}`}>
              <List>
                {group.agents.map((row) => (
                  <AgentRow key={row.agent.id} row={row} onOpen={setSelected} />
                ))}
              </List>
            </Section>
          ))}
        </>
      )}
    </>
  );
}
