import { ChevronRightIcon } from "lucide-react";
import { useState } from "react";
import type { InboxItem, PendingAction, Workstream } from "@/api/api.gen";
import { ErrorBadge } from "@/components/page";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { InboxRow } from "./Inbox";

function savedCollapsed() {
  try {
    return localStorage.getItem("tray") === "collapsed";
  } catch {
    return false;
  }
}

function saveCollapsed(collapsed: boolean) {
  try {
    localStorage.setItem("tray", collapsed ? "collapsed" : "expanded");
  } catch {
    // The tray works with no saved choice.
  }
}

// The pending actions and the questions of a chat, oldest first, above the composer.
export function ActionTray({
  rows,
  workstreams,
  onDecided,
}: {
  rows: InboxItem[];
  workstreams?: Workstream[];
  onDecided: (action: PendingAction) => void;
}) {
  const [error, setError] = useState("");
  const [collapsed, setCollapsed] = useState(savedCollapsed);
  if (rows.length === 0) {
    return null;
  }
  return (
    <Collapsible
      open={!collapsed}
      onOpenChange={(open) => {
        setCollapsed(!open);
        saveCollapsed(!open);
      }}
      className="grid grid-cols-1 gap-1 border-t py-2 md:px-4"
    >
      <h3 className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
        <CollapsibleTrigger className="group flex w-full items-center gap-1 px-4 text-left uppercase md:px-0">
          Waits for you ({rows.length})
          <ChevronRightIcon className="size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
        </CollapsibleTrigger>
      </h3>
      <CollapsibleContent forceMount className="grid gap-1 data-[state=closed]:hidden">
        {error && <ErrorBadge>{error}</ErrorBadge>}
        <ul
          aria-label="Pending actions"
          className="max-h-[35svh] divide-y overflow-y-auto md:-mx-2"
        >
          {rows.map((item) => (
            <InboxRow
              key={item.id}
              item={item}
              workstreams={workstreams}
              setError={setError}
              onDecided={onDecided}
            />
          ))}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  );
}
