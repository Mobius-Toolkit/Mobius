import { useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import {
  completeWorkstream,
  setAutopilot,
  type InboxItem,
  type WorkstreamAutopilot,
} from "@/api/api.gen";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { levelName } from "@/lib/autopilot";
import { showNotice } from "@/lib/notice";
import type { Workstreams } from "@/lib/workstreams";
import { AgentPanel } from "./AgentPanel";
import { AutopilotMenu, AutopilotSheet } from "./Autopilot";
import { Conversation } from "./Conversation";
import { TopBar } from "./TopBar";

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
  const [chosen, setChosen] = useState<{ from: WorkstreamAutopilot; to: WorkstreamAutopilot }>();
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

  const serverLevel = workstream?.autopilot ?? "off";
  if (chosen && chosen.from !== serverLevel) {
    setChosen(undefined);
  }
  const level = chosen ? chosen.to : serverLevel;

  const selectAutopilot = (to: WorkstreamAutopilot) => {
    setAutopilotBusy(true);
    setAutopilot(owner, name, number, { level: to })
      .then((res) => {
        if (res.status === 204) {
          setChosen({ from: serverLevel, to });
          setAutopilotError("");
          showNotice(`Autopilot of #${number}: ${levelName(to)}`);
        } else {
          setAutopilotError(res.data.error);
        }
      })
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
          showNotice(`Closed the Workstream #${number}`);
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

  const panel = <AgentPanel owner={owner} name={name} number={number} source={source} />;
  return (
    <div className="flex min-h-0 min-w-0 grow">
      <TopBar title={workstream?.title ?? ""} back="/workstreams">
        <span className="shrink-0 text-sm text-muted-foreground">#{number}</span>
        <AutopilotSheet
          level={level}
          disabled={!workstream || autopilotBusy}
          onSelect={selectAutopilot}
        />
        <Sheet>
          <SheetTrigger asChild>
            <Button variant="outline" size="sm" className="shrink-0">
              Tasks
            </Button>
          </SheetTrigger>
          <SheetContent
            side="bottom"
            aria-describedby={undefined}
            className="pt-2 data-[side=bottom]:h-[80svh]"
          >
            <SheetTitle className="sr-only">Tasks</SheetTitle>
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
            <AutopilotMenu
              level={level}
              disabled={!workstream || autopilotBusy}
              onSelect={selectAutopilot}
            />
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
        trayItems={inWorkstream}
        workstreams={workstreams.list}
      />
      <aside className="hidden w-80 shrink-0 flex-col border-l pt-2 md:flex">{panel}</aside>
    </div>
  );
}
