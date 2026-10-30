import { use, useCallback, useEffect, useId, useState } from "react";
import {
  listAgents,
  listTasks,
  resumeIssue,
  startIssue,
  stopIssue,
  type Agent,
  type LiveEvents,
  type TaskLine,
} from "@/api/api.gen";
import { PlayIcon, SquareIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { queueState, queueText } from "@/lib/agents";
import { onEvent } from "@/lib/events";
import { LoginContext } from "@/lib/login";
import { showNotice } from "@/lib/notice";
import { clock, dayClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { Transcript } from "./Agents";
import { Details } from "./Details";
import { Markdown } from "./Markdown";

function shownAgents(agents: Agent[], showStopped: boolean) {
  return agents
    .filter((agent) => showStopped || agent.endedAt === null)
    .toSorted((a, b) => b.id - a.id);
}

function AgentEntry({ agent, onOpen }: { agent: Agent; onOpen: (agent: Agent) => void }) {
  const state = queueState(agent.queueReason);
  const started = `${dayClock(agent.startedAt)}${agent.endedAt ? `–${clock(agent.endedAt)}` : ""}`;
  const detail = agent.queueReason ? `${started} · ${queueText(agent)}` : started;
  return (
    <li>
      <button
        type="button"
        onClick={() => onOpen(agent)}
        className="flex w-full items-center gap-3 rounded-lg p-2 text-left hover:bg-muted"
      >
        <span
          className={cn(
            "size-2 shrink-0 rounded-full",
            agent.endedAt ? "border border-muted-foreground" : state.dot,
          )}
        />
        <span className="grid min-w-0 grow gap-0.5">
          <span>
            <span className="font-medium">{agent.name}</span> {agent.title}
          </span>
          <span className="text-sm text-muted-foreground">
            {agent.harness} · {agent.model} · {detail}
          </span>
        </span>
        {agent.endedAt && <Badge variant="secondary">stopped</Badge>}
        {state.badge && <Badge variant="outline">{state.badge}</Badge>}
      </button>
    </li>
  );
}

function AgentList({
  owner,
  name,
  number,
  source,
}: {
  owner: string;
  name: string;
  number: number;
  source?: EventSource;
}) {
  const showLogin = use(LoginContext);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [error, setError] = useState<string>();
  const [showStopped, setShowStopped] = useState(false);
  const [selected, setSelected] = useState<Agent>();

  const load = useCallback(() => {
    listAgents(owner, name, number)
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
  }, [owner, name, number, showLogin]);

  // An agent event that comes before the listener or while the connection is down is lost, so each connection reads
  // the list.
  useEffect(() => {
    load();
    if (!source) {
      return;
    }
    load();
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "agent">(source, "agent", (agent) => {
      if (agent.workstreamRepository === `${owner}/${name}` && agent.workstream === number) {
        load();
      }
    });
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source, load, owner, name, number]);

  if (selected) {
    const live = agents.find((agent) => agent.id === selected.id) ?? selected;
    return <Transcript agent={live} source={source} onClose={() => setSelected(undefined)} />;
  }
  return (
    <div className="grid gap-2">
      {error && <Badge variant="destructive">{error}</Badge>}
      <div className="flex items-center gap-2 px-2">
        <Switch id="stopped-agents" checked={showStopped} onCheckedChange={setShowStopped} />
        <Label htmlFor="stopped-agents">Show stopped agents</Label>
      </div>
      <ul>
        {shownAgents(agents, showStopped).map((agent) => (
          <AgentEntry key={agent.id} agent={agent} onOpen={setSelected} />
        ))}
      </ul>
    </div>
  );
}

function TaskEntry({ line }: { line: TaskLine }) {
  const [starting, setStarting] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [error, setError] = useState("");
  const resumes = line.state === "needs-human";
  const stops =
    ["ready", "working", "review", "queued", "question"].includes(line.state) ||
    line.state.startsWith("waits for ");
  const reference = `${line.otherRepository ? line.repository : ""}#${line.number}`;
  const label = `${resumes ? "Resume" : "Start"} ${reference}`;
  const stopLabel = `Stop ${reference}`;
  const stop = () => {
    const [owner, name] = line.repository.split("/");
    setStopping(true);
    stopIssue(owner, name, line.number)
      .then((res) => setError(res.status === 204 ? "" : res.data.error))
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setStopping(false));
  };
  const start = () => {
    const [owner, name] = line.repository.split("/");
    setStarting(true);
    (resumes ? resumeIssue : startIssue)(owner, name, line.number)
      .then((res) => {
        setError(res.status === 204 ? "" : res.data.error);
        if (res.status === 204) {
          showNotice(`${resumes ? "Resumed" : "Started"} ${reference}`);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setStarting(false));
  };
  return (
    <li className="grid gap-1">
      <div className="flex items-center gap-1">
        <a
          href={line.url}
          target="_blank"
          rel="noreferrer"
          style={{ paddingLeft: `${0.5 + line.depth * 1.25}rem` }}
          className="flex min-w-0 grow flex-wrap items-center gap-x-2 gap-y-1 rounded-lg py-2 pr-2 hover:bg-muted"
        >
          <span
            className={cn(
              "min-w-0 grow wrap-anywhere",
              line.state === "closed" && "text-muted-foreground",
            )}
          >
            {reference} {line.title}
          </span>
          {line.blockedBy.map((blocker) => (
            <span
              key={`${blocker.repository}#${blocker.number}`}
              className="min-w-0 text-sm text-muted-foreground wrap-anywhere"
            >
              blocked by {blocker.repository}#{blocker.number}
              {blocker.workstreamTitle && ` (Workstream "${blocker.workstreamTitle}")`}
            </span>
          ))}
          <Badge variant={line.state === "open" ? "outline" : "secondary"}>{line.state}</Badge>
        </a>
        {(line.state === "open" || line.state === "triage" || resumes) &&
          line.blockedBy.length === 0 && (
            <Button
              size="icon"
              variant="ghost"
              className="shrink-0"
              aria-label={label}
              title={label}
              disabled={starting}
              onClick={start}
            >
              {starting ? <Spinner aria-hidden /> : <PlayIcon />}
            </Button>
          )}
        {stops && (
          <Button
            size="icon"
            variant="ghost"
            className="shrink-0"
            aria-label={stopLabel}
            title={stopLabel}
            disabled={stopping}
            onClick={stop}
          >
            {stopping ? <Spinner aria-hidden /> : <SquareIcon />}
          </Button>
        )}
      </div>
      {error && (
        <Badge variant="destructive" className="h-auto w-full justify-start whitespace-normal">
          {error}
        </Badge>
      )}
    </li>
  );
}

// Removes the closed tasks. A sub-issue of a removed task moves up one level for each removed ancestor.
function withoutClosed(lines: TaskLine[]): TaskLine[] {
  const removed: number[] = [];
  const open: TaskLine[] = [];
  for (const line of lines) {
    while (removed.length > 0 && removed[removed.length - 1] >= line.depth) {
      removed.pop();
    }
    if (line.state === "closed") {
      removed.push(line.depth);
    } else {
      open.push({ ...line, depth: line.depth - removed.length });
    }
  }
  return open;
}

function Tasks({
  owner,
  name,
  number,
  source,
}: {
  owner: string;
  name: string;
  number: number;
  source?: EventSource;
}) {
  const showLogin = use(LoginContext);
  const [lines, setLines] = useState<TaskLine[]>();
  const [error, setError] = useState<string>();
  const [showClosed, setShowClosed] = useState(false);
  const closedId = useId();

  const load = useCallback(() => {
    listTasks(owner, name, number)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setLines(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [owner, name, number, showLogin]);

  // The workstreams event also tells of a change of the tasks. An event that comes before the listener or while the
  // connection is down is lost, so each connection reads the tasks.
  useEffect(() => {
    load();
    if (!source) {
      return;
    }
    load();
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "workstreams">(source, "workstreams", load);
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source, load]);

  const shown = showClosed ? lines : lines && withoutClosed(lines);
  return (
    <div className="grid gap-2">
      {error && <Badge variant="destructive">{error}</Badge>}
      <div className="flex items-center gap-2 px-2">
        <Switch id={closedId} checked={showClosed} onCheckedChange={setShowClosed} />
        <Label htmlFor={closedId}>Show closed tasks</Label>
      </div>
      {shown?.length === 0 && <p className="px-2 text-sm text-muted-foreground">No tasks.</p>}
      <ul>
        {shown?.map((line) => (
          <TaskEntry key={`${line.url} ${line.state}`} line={line} />
        ))}
      </ul>
    </div>
  );
}

// The Tasks tab and the Details tab read their data each time they open.
export function AgentPanel({
  owner,
  name,
  number,
  source,
  brief,
  tab,
  onTabChange,
}: {
  owner: string;
  name: string;
  number: number;
  source?: EventSource;
  brief: string;
  tab: string;
  onTabChange: (tab: string) => void;
}) {
  return (
    <Tabs value={tab} onValueChange={onTabChange} className="min-h-0 grow">
      <TabsList variant="line" className="px-2">
        <TabsTrigger value="tasks">Tasks</TabsTrigger>
        <TabsTrigger value="agents">Agents</TabsTrigger>
        <TabsTrigger value="details">Details</TabsTrigger>
        <TabsTrigger value="brief">Brief</TabsTrigger>
      </TabsList>
      <TabsContent value="tasks" className="overflow-y-auto p-2">
        <Tasks owner={owner} name={name} number={number} source={source} />
      </TabsContent>
      <TabsContent value="agents" className="flex flex-col overflow-y-auto p-2">
        <AgentList owner={owner} name={name} number={number} source={source} />
      </TabsContent>
      <TabsContent value="details" className="overflow-y-auto p-2">
        <Details owner={owner} name={name} number={number} source={source} />
      </TabsContent>
      <TabsContent value="brief" className="overflow-y-auto p-2">
        <Markdown text={brief} />
      </TabsContent>
    </Tabs>
  );
}
