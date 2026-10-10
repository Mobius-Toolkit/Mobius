import { use, useCallback, useEffect, useState } from "react";
import {
  listAgents,
  listTasks,
  resumeIssue,
  startIssue,
  type Agent,
  type LiveEvents,
  type TaskLine,
} from "@/api/api.gen";
import { PlayIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { queueState, queueText } from "@/lib/agents";
import { onEvent } from "@/lib/events";
import { LoginContext } from "@/lib/login";
import { clock, dayClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { Transcript } from "./Agents";

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
      if (agent.repository === `${owner}/${name}` && agent.workstream === number) {
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

function TaskEntry({ owner, name, line }: { owner: string; name: string; line: TaskLine }) {
  const [started, setStarted] = useState(false);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const state = started && line.state === "open" ? "ready" : line.state;
  const resumes = line.state === "needs-human";
  const label = `${resumes ? "Resume" : "Start"} #${line.number}`;
  const start = () => {
    setStarting(true);
    (resumes ? resumeIssue : startIssue)(owner, name, line.number)
      .then((res) => {
        if (res.status === 204) {
          setError("");
          setStarted(true);
        } else {
          setError(res.data.error);
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
          <span className={cn("grow", state === "closed" && "text-muted-foreground")}>
            #{line.number} {line.title}
          </span>
          {line.blockedBy.map((blocker) => (
            <span key={blocker.number} className="text-sm text-muted-foreground">
              blocked by #{blocker.number}
              {blocker.workstreamTitle && ` (Workstream "${blocker.workstreamTitle}")`}
            </span>
          ))}
          <Badge variant={state === "open" ? "outline" : "secondary"}>{state}</Badge>
        </a>
        {!started &&
          (state === "open" || resumes) &&
          !line.otherRepository &&
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
        <Switch id="closed-tasks" checked={showClosed} onCheckedChange={setShowClosed} />
        <Label htmlFor="closed-tasks">Show closed tasks</Label>
      </div>
      {shown?.length === 0 && <p className="px-2 text-sm text-muted-foreground">No tasks.</p>}
      <ul>
        {shown?.map((line) => (
          <TaskEntry key={`${line.url} ${line.state}`} owner={owner} name={name} line={line} />
        ))}
      </ul>
    </div>
  );
}

// The Tasks tab reads the tasks each time it opens.
export function AgentPanel({
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
  return (
    <Tabs defaultValue="agents" className="min-h-0 grow">
      <TabsList variant="line" className="px-2">
        <TabsTrigger value="agents">Agents</TabsTrigger>
        <TabsTrigger value="tasks">Tasks</TabsTrigger>
      </TabsList>
      <TabsContent value="agents" className="flex flex-col overflow-y-auto p-2">
        <AgentList owner={owner} name={name} number={number} source={source} />
      </TabsContent>
      <TabsContent value="tasks" className="overflow-y-auto p-2">
        <Tasks owner={owner} name={name} number={number} source={source} />
      </TabsContent>
    </Tabs>
  );
}
