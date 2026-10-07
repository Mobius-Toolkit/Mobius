import { use, useCallback, useEffect, useState } from "react";
import {
  listAgents,
  listTasks,
  startIssue,
  type Agent,
  type LiveEvents,
  type TaskLine,
} from "@/api/api.gen";
import { PlayIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { onEvent } from "@/lib/events";
import { LoginContext } from "@/lib/login";
import { clock, dayClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { paused, Transcript } from "./Agents";

type Row = { agent: Agent; depth: number };

function compareTreePaths(a: number[], b: number[]) {
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    if (a[i] !== b[i]) {
      return b[i] - a[i];
    }
  }
  return a.length - b.length;
}

// Gives the agents in tree order with their depth: each agent follows its parent, and the newest agent comes first
// among its siblings. An agent whose parent is not in the list has depth 0.
function treeRows(agents: Agent[]): Row[] {
  const byId = new Map(agents.map((agent) => [agent.id, agent]));
  const treePath = (agent: Agent): number[] => {
    const parent = agent.parent === null ? undefined : byId.get(agent.parent);
    return parent ? [...treePath(parent), agent.id] : [agent.id];
  };
  return agents
    .map((agent) => ({ agent, path: treePath(agent) }))
    .toSorted((a, b) => compareTreePaths(a.path, b.path))
    .map(({ agent, path }) => ({ agent, depth: path.length - 1 }));
}

// With no stopped agents shown, a stopped agent stays only when an agent below it on any level is live, so that each
// live agent keeps its place below its parent.
function shownAgents(agents: Agent[], showStopped: boolean) {
  if (showStopped) {
    return agents;
  }
  const byId = new Map(agents.map((agent) => [agent.id, agent]));
  const kept = new Set<number>();
  for (const live of agents.filter((agent) => agent.endedAt === null)) {
    let next: Agent | undefined = live;
    while (next && !kept.has(next.id)) {
      kept.add(next.id);
      next = next.parent === null ? undefined : byId.get(next.parent);
    }
  }
  return agents.filter((agent) => kept.has(agent.id));
}

function AgentEntry({ row, onOpen }: { row: Row; onOpen: (agent: Agent) => void }) {
  const agent = row.agent;
  const detail =
    agent.queueReason ||
    `${dayClock(agent.startedAt)}${agent.endedAt ? `–${clock(agent.endedAt)}` : ""}`;
  return (
    <li>
      <button
        type="button"
        onClick={() => onOpen(agent)}
        style={{ paddingLeft: `${0.5 + row.depth * 1.25}rem` }}
        className="flex w-full items-center gap-3 rounded-lg py-2 pr-2 text-left hover:bg-muted"
      >
        <span
          className={cn(
            "size-2 shrink-0 rounded-full",
            agent.queueReason
              ? "bg-amber-500"
              : agent.endedAt
                ? "border border-muted-foreground"
                : "bg-green-600",
          )}
        />
        <span className="grid min-w-0 grow gap-0.5">
          <span>
            <span className="font-medium">{agent.name}</span> {agent.title}
          </span>
          <span className="truncate text-sm text-muted-foreground">
            {agent.harness} · {agent.model} · {detail}
          </span>
        </span>
        {agent.endedAt && <Badge variant="secondary">stopped</Badge>}
        {agent.queueReason && (
          <Badge variant="outline">
            {agent.queueReason.startsWith(paused) ? "paused" : "queued"}
          </Badge>
        )}
      </button>
    </li>
  );
}

function AgentTree({
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

  useEffect(load, [load]);

  // An agent event that comes while the connection is down is lost, so each connection reads the tree.
  useEffect(() => {
    if (!source) {
      return;
    }
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
    return <Transcript agent={selected} source={source} onClose={() => setSelected(undefined)} />;
  }
  return (
    <div className="grid gap-2">
      {error && <Badge variant="destructive">{error}</Badge>}
      <div className="flex items-center gap-2 px-2">
        <Switch id="stopped-agents" checked={showStopped} onCheckedChange={setShowStopped} />
        <Label htmlFor="stopped-agents">Show stopped agents</Label>
      </div>
      <ul>
        {treeRows(shownAgents(agents, showStopped)).map((row) => (
          <AgentEntry key={row.agent.id} row={row} onOpen={setSelected} />
        ))}
      </ul>
    </div>
  );
}

function TaskEntry({ owner, name, line }: { owner: string; name: string; line: TaskLine }) {
  const [started, setStarted] = useState(false);
  const [error, setError] = useState("");
  const state = started && line.state === "open" ? "ready" : line.state;
  const start = () => {
    startIssue(owner, name, line.number)
      .then((res) => {
        if (res.status === 204) {
          setError("");
          setStarted(true);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
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
        {state === "open" && !line.otherRepository && line.blockedBy.length === 0 && (
          <Button
            size="icon"
            variant="ghost"
            className="shrink-0"
            aria-label={`Start #${line.number}`}
            onClick={start}
          >
            <PlayIcon />
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

  useEffect(load, [load]);

  // The workstreams event also tells of a change of the tasks.
  useEffect(() => {
    if (!source) {
      return;
    }
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "workstreams">(source, "workstreams", load);
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source, load]);

  const shown = showClosed ? lines : lines?.filter((line) => line.state !== "closed");
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
          <TaskEntry key={line.url} owner={owner} name={name} line={line} />
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
      <TabsContent value="agents" className="overflow-y-auto p-2">
        <AgentTree owner={owner} name={name} number={number} source={source} />
      </TabsContent>
      <TabsContent value="tasks" className="overflow-y-auto p-2">
        <Tasks owner={owner} name={name} number={number} source={source} />
      </TabsContent>
    </Tabs>
  );
}
