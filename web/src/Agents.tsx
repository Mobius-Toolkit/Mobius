import { ArrowDownIcon, ChevronLeftIcon, ChevronRightIcon } from "lucide-react";
import { use, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  getTranscript,
  listActiveAgents,
  startCheck,
  type ActiveAgent,
  type ActiveAgents,
  type Agent,
  type LiveEvents,
  type TranscriptLine,
} from "@/api/api.gen";
import { ErrorBadge, inset, List, PageHeader, rowClass, Section } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { lowLoadReason, queueState, queueText } from "@/lib/agents";
import { onEvent } from "@/lib/events";
import { LoginContext } from "@/lib/login";
import { atEnd } from "@/lib/scroll";
import { clock, dayClock } from "@/lib/time";
import { cn } from "@/lib/utils";
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
        {line.text}
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
          {line.body}
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

function upsert(list: TranscriptLine[], line: TranscriptLine) {
  const known = list.find((other) => other.id === line.id);
  // The agent only adds text to a chunk, so the longer body is the newer body.
  if (known && known.body.length >= line.body.length) {
    return list;
  }
  return [...list.filter((other) => other.id !== line.id), line].toSorted((a, b) => a.id - b.id);
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
  const [error, setError] = useState<string>();
  const [behind, setBehind] = useState(false);
  const listRef = useRef<HTMLUListElement>(null);
  const pinned = useRef(true);

  const load = useCallback(() => {
    getTranscript(agent.id)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setLines((list) => res.data.data.reduce(upsert, list ?? []));
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [agent.id, showLogin]);

  // A line that comes while the connection is down is lost, so each connection reads the log.
  useEffect(() => {
    load();
    if (!source) {
      return;
    }
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "transcript">(source, "transcript", (line) => {
      if (line.session === agent.id) {
        setLines((list) => upsert(list ?? [], line));
      }
    });
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source, load, agent.id]);

  // New data scrolls the log to the end while the Owner is at the end. Else the log stays, and the button shows.
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || !lines) {
      return;
    }
    if (pinned.current) {
      list.scrollTop = list.scrollHeight;
    } else {
      setBehind(true);
    }
  }, [lines]);

  return (
    <div className={cn("flex min-h-0 grow flex-col gap-4", className)}>
      {error && <Badge variant="destructive">{error}</Badge>}
      <div className="relative flex min-h-0 grow flex-col">
        <ul
          ref={listRef}
          onScroll={(event) => {
            pinned.current = atEnd(event.currentTarget);
            if (pinned.current) {
              setBehind(false);
            }
          }}
          className="grid min-h-0 grow grid-cols-[minmax(0,1fr)] content-start gap-2 overflow-y-auto"
        >
          {lines?.map((line) => (
            <TranscriptEntry key={line.id} line={line} />
          ))}
        </ul>
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
      <CardHeader>
        <CardTitle className="truncate">
          {agent.name} {agent.title}
        </CardTitle>
        <CardAction className="flex flex-wrap items-center justify-end gap-1">
          <StartCheckButton agent={agent} />
          <Button variant="ghost" onClick={onClose}>
            <ChevronLeftIcon />
            Agents
          </Button>
        </CardAction>
      </CardHeader>
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

  const load = useCallback(() => {
    listActiveAgents()
      .then((res) => {
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
    load();
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
      >
        {agents && !selected && (
          <span className="ml-auto text-muted-foreground">
            {agents.count} / {agents.max}
          </span>
        )}
      </TopBar>
      {selected ? (
        <>
          <PageHeader title={`${selected.name} ${selected.title}`}>
            <Button variant="ghost" onClick={() => setSelected(undefined)}>
              <ChevronLeftIcon />
              Agents
            </Button>
          </PageHeader>
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
          <PageHeader title="Agents">
            {agents && (
              <span className="text-muted-foreground">
                {agents.count} / {agents.max}
              </span>
            )}
          </PageHeader>
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
