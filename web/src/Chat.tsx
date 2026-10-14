import { useNavigate, useRouter } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { completeWorkstream, resumeIssue, setAutopilot, type NeedsHuman } from "@/api/api.gen";
import { ErrorBadge, List, Row } from "@/components/page";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import type { Workstreams } from "@/lib/workstreams";
import { AgentPanel } from "./AgentPanel";
import { Conversation } from "./Conversation";
import { link } from "./Inbox";
import { Markdown } from "./Markdown";
import { TopBar } from "./TopBar";

function NeedsHumanRow({
  issue,
  pending,
  onResume,
}: {
  issue: NeedsHuman;
  pending: boolean;
  onResume: () => void;
}) {
  const [clipped, setClipped] = useState(false);
  const measure = (element: HTMLDivElement) => {
    const observer = new ResizeObserver(() =>
      setClipped(element.scrollHeight > element.clientHeight),
    );
    observer.observe(element);
    return () => observer.disconnect();
  };
  return (
    <Row className="flex-wrap gap-y-2 py-2">
      <div className="flex min-w-0 grow basis-full flex-wrap items-center gap-x-2 gap-y-2 sm:flex-nowrap">
        <span className="flex min-w-0 grow basis-full flex-wrap items-baseline gap-x-2 sm:basis-0 sm:flex-nowrap">
          <a href={issue.url} target="_blank" rel="noreferrer" className={cn("shrink-0", link)}>
            #{issue.number}
          </a>
          {issue.pullRequestUrl && (
            <a
              href={issue.pullRequestUrl}
              target="_blank"
              rel="noreferrer"
              className={cn("shrink-0", link)}
            >
              PR #{issue.pullRequest}
            </a>
          )}
          <span className="grow basis-0 truncate sm:grow-0 sm:basis-auto">{issue.title}</span>
          {issue.reason && (
            <div
              ref={measure}
              className="line-clamp-2 min-w-0 basis-full text-sm text-muted-foreground sm:line-clamp-1 sm:grow sm:basis-0 [&>div]:block"
            >
              <Markdown text={issue.reason} />
            </div>
          )}
        </span>
        <div className="flex basis-full items-center gap-2 empty:hidden sm:basis-auto sm:shrink-0">
          {issue.question && (
            <Button size="sm" asChild>
              <a href={issue.url} target="_blank" rel="noreferrer">
                Answer
              </a>
            </Button>
          )}
          {issue.stopped && (
            <Button size="sm" pending={pending} onClick={onResume}>
              Resume
            </Button>
          )}
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
        </div>
      </div>
    </Row>
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
    <div className="grid grid-cols-1 gap-1 border-t py-2 md:px-4">
      {error && <ErrorBadge>{error}</ErrorBadge>}
      <List>
        {issues.map((issue) => (
          <NeedsHumanRow
            key={issue.number}
            issue={issue}
            pending={pending.includes(issue.number)}
            onResume={() => resume(issue)}
          />
        ))}
      </List>
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
  const router = useRouter();
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
    const pathname = router.state.location.pathname;
    completeWorkstream(owner, name, number)
      .then((res) => {
        if (res.status === 204) {
          if (router.state.location.pathname === pathname) {
            void navigate({ to: "/workstreams" });
          }
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
