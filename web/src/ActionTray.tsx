import { useState } from "react";
import type { InboxItem, PendingAction, Workstream } from "@/api/api.gen";
import { ErrorBadge } from "@/components/page";
import { InboxRow } from "./Inbox";

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
  if (rows.length === 0) {
    return null;
  }
  return (
    <div className="grid grid-cols-1 gap-1 border-t py-2 md:px-4">
      <h3 className="px-4 text-xs font-semibold tracking-wide text-muted-foreground uppercase md:px-0">
        Waits for you ({rows.length})
      </h3>
      {error && <ErrorBadge>{error}</ErrorBadge>}
      <ul aria-label="Pending actions" className="max-h-[35svh] divide-y overflow-y-auto md:-mx-2">
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
    </div>
  );
}
