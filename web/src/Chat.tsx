import { useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { completeWorkstream, setAutopilot, type InboxItem, type Workstream } from "@/api/api.gen";
import { ErrorBadge, List } from "@/components/page";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import type { Workstreams } from "@/lib/workstreams";
import { AgentPanel } from "./AgentPanel";
import { Conversation } from "./Conversation";
import { InboxRow } from "./Inbox";
import { TopBar } from "./TopBar";

function WorkstreamInbox({
  items,
  workstreams,
}: {
  items: InboxItem[];
  workstreams?: Workstream[];
}) {
  const [error, setError] = useState("");
  if (items.length === 0) {
    return null;
  }
  return (
    <div className="grid grid-cols-1 gap-1 border-t py-2 md:px-4">
      {error && <ErrorBadge>{error}</ErrorBadge>}
      <List>
        {items.map((item) => (
          <InboxRow key={item.id} item={item} workstreams={workstreams} setError={setError} />
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
  inbox,
  unread,
  source,
}: {
  owner: string;
  name: string;
  number: number;
  workstreams: Workstreams;
  inbox: InboxItem[];
  unread?: number;
  source?: EventSource;
}) {
  const repository = `${owner}/${name}`;
  const inWorkstream = inbox
    .filter((item) => item.workstreamRepository === repository && item.workstream === number)
    .toSorted((a, b) => a.id - b.id);
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
        if (res.status !== 204) {
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
            {workstream?.allTasksClosed && !workstream.evergreen && (
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
          <WorkstreamInbox
            items={inWorkstream.filter((item) => !item.action)}
            workstreams={workstreams.list}
          />
        }
        trayItems={inWorkstream.filter((item) => item.action?.role === "lead")}
      />
      <aside className="hidden w-80 shrink-0 flex-col border-l pt-2 md:flex">{panel}</aside>
    </div>
  );
}
