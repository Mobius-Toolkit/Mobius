import { useNavigate } from "@tanstack/react-router";
import { FileTextIcon } from "lucide-react";
import { useEffect, useState, useSyncExternalStore } from "react";
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
import { AutopilotMenu, AutopilotSheet } from "./Autopilot";
import { Conversation } from "./Conversation";
import { Panel } from "./Panel";
import type { Tab } from "./panel/schemas";
import { TopBar } from "./TopBar";

const wideQuery = "(min-width: 768px)";

function subscribeWide(listener: () => void) {
  const query = window.matchMedia(wideQuery);
  query.addEventListener("change", listener);
  return () => query.removeEventListener("change", listener);
}

const currentWide = () => window.matchMedia(wideQuery).matches;

export function Chat({
  owner,
  name,
  number,
  workstreams,
  inbox,
}: {
  owner: string;
  name: string;
  number: number;
  workstreams: Workstreams;
  inbox: InboxItem[];
}) {
  const repository = `${owner}/${name}`;
  const inWorkstream = inbox
    .filter((item) => item.workstreamRepository === repository && item.workstream === number)
    .toSorted((a, b) => a.id - b.id);
  const workstream = workstreams.list?.find(
    (other) => other.repository === repository && other.number === number,
  );
  const [chosen, setChosen] = useState<{ from: WorkstreamAutopilot; to: WorkstreamAutopilot }>();
  const [tab, setTab] = useState<Tab>("tasks");
  const [sheetOpen, setSheetOpen] = useState(false);
  const wide = useSyncExternalStore(subscribeWide, currentWide);
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

  const panel = (instance: string, onTaskAttached?: () => void) => (
    <Panel
      owner={owner}
      name={name}
      number={number}
      instance={instance}
      tab={tab}
      onTabChange={setTab}
      onTaskAttached={onTaskAttached}
    />
  );
  return (
    <div className="flex min-h-0 min-w-0 grow">
      <TopBar title={workstream?.title ?? ""} back="/workstreams">
        <span className="shrink-0 text-sm text-muted-foreground">#{number}</span>
        <AutopilotSheet
          level={level}
          disabled={!workstream || autopilotBusy}
          onSelect={selectAutopilot}
        />
        <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
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
            {panel("sheet", () => setSheetOpen(false))}
          </SheetContent>
        </Sheet>
      </TopBar>
      <Conversation
        organization={owner}
        repository={repository}
        workstream={number}
        agent="Lead"
        brief={workstream}
        head={
          <>
            <h2 className="min-w-0 truncate text-base font-semibold">{workstream?.title}</h2>
            <span className="text-sm text-muted-foreground">#{number}</span>
            <div className="ml-auto flex shrink-0 items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                className="shrink-0"
                aria-label="Brief"
                title="Brief"
                onClick={() => setTab("brief")}
              >
                <FileTextIcon />
                <span className="hidden lg:inline">Brief</span>
              </Button>
              <AutopilotMenu
                level={level}
                disabled={!workstream || autopilotBusy}
                onSelect={selectAutopilot}
              />
            </div>
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
      />
      <aside className="hidden w-80 shrink-0 flex-col border-l pt-2 md:flex">
        {wide && panel("side")}
      </aside>
    </div>
  );
}
