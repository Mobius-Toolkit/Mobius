import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { completeWorkstream, resumeIssue, setAutopilot, type NeedsHuman } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import type { Workstreams } from "@/lib/workstreams";
import { AgentPanel } from "./AgentPanel";
import { Conversation } from "./Conversation";
import { Markdown } from "./Markdown";
import { TopBar } from "./TopBar";

function Reason({ issue }: { issue: NeedsHuman }) {
  const text = useRef<HTMLDivElement>(null);
  const [clipped, setClipped] = useState(false);
  useEffect(() => {
    const element = text.current;
    if (!element) {
      return;
    }
    const observer = new ResizeObserver(() =>
      setClipped(element.scrollHeight > element.clientHeight),
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return (
    <>
      {clipped && (
        <Dialog>
          <DialogTrigger asChild>
            <Button variant="link" size="sm">
              Details
            </Button>
          </DialogTrigger>
          <DialogContent
            aria-describedby={undefined}
            className="grid max-h-[85dvh] grid-rows-[auto_minmax(0,1fr)] sm:max-w-lg"
          >
            <DialogTitle className="pr-8">
              #{issue.number} {issue.title}
            </DialogTitle>
            <div className="overflow-y-auto">
              <Markdown text={issue.reason} />
            </div>
          </DialogContent>
        </Dialog>
      )}
      <div ref={text} className="line-clamp-2 w-full text-sm text-muted-foreground [&>div]:block">
        <Markdown text={issue.reason} />
      </div>
    </>
  );
}

function NeedsHumanList({
  issues,
  onChange,
}: {
  issues: NeedsHuman[];
  onChange: () => Promise<unknown>;
}) {
  const [error, setError] = useState("");
  const [pending, setPending] = useState<number[]>([]);
  if (issues.length === 0) {
    return null;
  }
  const resume = (issue: NeedsHuman) => {
    const [owner, name] = issue.repository.split("/");
    setPending((numbers) => [...numbers, issue.number]);
    resumeIssue(owner, name, issue.number)
      .then((res) => setError(res.status === 204 ? "" : res.data.error))
      .catch((err: unknown) => setError(String(err)))
      .finally(() =>
        onChange().then(() =>
          setPending((numbers) => numbers.filter((number) => number !== issue.number)),
        ),
      );
  };
  return (
    <div className="grid gap-1 border-t px-4 py-2">
      {error && (
        <Badge variant="destructive" className="h-auto w-full justify-start whitespace-normal">
          {error}
        </Badge>
      )}
      {issues.map((issue) => (
        <div key={issue.number} className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <a
            href={issue.url}
            target="_blank"
            rel="noreferrer"
            className="min-w-0 basis-full text-primary underline-offset-4 hover:underline md:flex-1"
          >
            #{issue.number} {issue.title}
          </a>
          {issue.pullRequestUrl && (
            <a
              href={issue.pullRequestUrl}
              target="_blank"
              rel="noreferrer"
              className="shrink-0 text-primary underline-offset-4 hover:underline"
            >
              PR #{issue.pullRequest}
            </a>
          )}
          {issue.question && (
            <Button size="sm" asChild>
              <a href={issue.url} target="_blank" rel="noreferrer">
                Answer
              </a>
            </Button>
          )}
          {issue.stopped && (
            <Button
              size="sm"
              pending={pending.includes(issue.number)}
              onClick={() => resume(issue)}
            >
              Resume
            </Button>
          )}
          {issue.reason && <Reason key={issue.reason} issue={issue} />}
        </div>
      ))}
    </div>
  );
}

export function Chat({
  owner,
  name,
  number,
  workstreams,
  unread,
  source,
}: {
  owner: string;
  name: string;
  number: number;
  workstreams: Workstreams;
  unread?: number;
  source?: EventSource;
}) {
  const repository = `${owner}/${name}`;
  const workstream = workstreams.list?.find(
    (other) => other.repository === repository && other.number === number,
  );
  const [autopilotBusy, setAutopilotBusy] = useState(false);
  const [autopilotError, setAutopilotError] = useState("");
  const [closeBusy, setCloseBusy] = useState(false);
  const [closeError, setCloseError] = useState("");
  const navigate = useNavigate();
  const missing = workstreams.list !== undefined && !workstream;

  useEffect(() => {
    if (missing) {
      void navigate({ to: "/workstreams", replace: true });
    }
  }, [missing, navigate]);

  const switchAutopilot = (on: boolean) => {
    setAutopilotBusy(true);
    setAutopilot(owner, name, number, { on })
      .then((res) => setAutopilotError(res.status === 204 ? "" : res.data.error))
      .catch((err: unknown) => setAutopilotError(String(err)))
      .finally(() => {
        setAutopilotBusy(false);
        workstreams.load();
      });
  };

  const close = () => {
    setCloseBusy(true);
    setCloseError("");
    completeWorkstream(owner, name, number)
      .then((res) => {
        if (res.status === 204) {
          void navigate({ to: "/workstreams" });
        } else {
          setCloseError(res.data.error);
          setCloseBusy(false);
        }
      })
      .catch((err: unknown) => {
        setCloseError(String(err));
        setCloseBusy(false);
      });
  };

  const autopilot = (id: string) => (
    <>
      <Switch
        id={id}
        checked={workstream?.autopilot ?? false}
        disabled={!workstream || autopilotBusy}
        onCheckedChange={switchAutopilot}
      />
      <Label htmlFor={id}>Autopilot</Label>
    </>
  );
  const panel = <AgentPanel owner={owner} name={name} number={number} source={source} />;
  return (
    <div className="flex min-h-0 min-w-0 grow">
      <TopBar title={workstream?.title ?? ""} back="/workstreams">
        <span className="shrink-0 text-sm text-muted-foreground">#{number}</span>
        {autopilot("autopilot-top-bar")}
        <Sheet>
          <SheetTrigger asChild>
            <Button variant="outline" size="sm" className="ml-auto shrink-0">
              Agents
            </Button>
          </SheetTrigger>
          <SheetContent
            side="bottom"
            aria-describedby={undefined}
            className="pt-2 data-[side=bottom]:h-[80svh]"
          >
            <SheetTitle className="sr-only">Agents and tasks</SheetTitle>
            {panel}
          </SheetContent>
        </Sheet>
      </TopBar>
      <Conversation
        organization={owner}
        repository={repository}
        workstream={number}
        agent="Lead"
        source={source}
        unread={unread}
        brief={workstream}
        head={
          <>
            <h2 className="min-w-0 truncate text-base font-semibold">{workstream?.title}</h2>
            <span className="text-sm text-muted-foreground">#{number}</span>
            {autopilot("autopilot")}
          </>
        }
        note={
          <>
            {autopilotError && (
              <p className="border-b px-4 py-2 text-sm text-destructive">{autopilotError}</p>
            )}
            {workstream?.allTasksClosed && (
              <div className="flex flex-wrap items-center gap-3 border-b px-4 py-2">
                <span>All tasks are closed.</span>
                <Button size="sm" pending={closeBusy} onClick={close}>
                  Close Workstream
                </Button>
                {closeError && <span className="text-sm text-destructive">{closeError}</span>}
              </div>
            )}
          </>
        }
        footer={
          <NeedsHumanList
            issues={workstreams.needsHuman.filter(
              (issue) => issue.repository === repository && issue.workstream === number,
            )}
            onChange={workstreams.load}
          />
        }
      />
      <aside className="hidden w-80 shrink-0 flex-col border-l pt-2 md:flex">{panel}</aside>
    </div>
  );
}
