import type { InboxItem } from "@/api/api.gen";
import { Button } from "@/components/ui/button";
import { Row } from "@/components/page";

// The pending actions and the questions of a chat, oldest first, above the composer.
export function ActionTray({
  rows,
  answer,
}: {
  rows: InboxItem[];
  answer: (item: InboxItem) => void;
}) {
  if (rows.length === 0) {
    return null;
  }
  return (
    <div className="grid grid-cols-1 gap-1 border-t py-2 md:px-4">
      <h3 className="px-4 text-xs font-semibold tracking-wide text-muted-foreground uppercase md:px-0">
        Waits for you ({rows.length})
      </h3>
      <ul aria-label="Pending actions" className="max-h-[35svh] divide-y overflow-y-auto md:-mx-2">
        {rows.map((item) => (
          <Row key={item.id} className="flex-wrap gap-y-1 py-2">
            <span className="min-w-0 grow basis-0 wrap-anywhere">{item.title}</span>
            <Button variant="outline" size="sm" onClick={() => answer(item)}>
              Answer
            </Button>
            {item.action?.error && (
              <p role="alert" className="basis-full text-sm text-destructive wrap-anywhere">
                {item.action.error}
              </p>
            )}
          </Row>
        ))}
      </ul>
    </div>
  );
}
