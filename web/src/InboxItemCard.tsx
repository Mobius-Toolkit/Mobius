import { XIcon } from "lucide-react";
import type { ChatInboxItem } from "@/api/api.gen";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const card = "grid min-w-0 gap-0.5 rounded-lg border bg-card px-2 py-1.5 text-sm";

function Lines({ item }: { item: ChatInboxItem }) {
  return (
    <>
      {item.reference && (
        <span className="truncate text-xs text-muted-foreground">{item.reference}</span>
      )}
      <span className="truncate font-medium">{item.title}</span>
      <span className="truncate text-xs text-muted-foreground">{item.reason}</span>
    </>
  );
}

export function InboxItemCard({ item }: { item: ChatInboxItem }) {
  if (!item.link) {
    return (
      <div className={card}>
        <Lines item={item} />
      </div>
    );
  }
  return (
    <a href={item.link} target="_blank" rel="noreferrer" className={cn(card, "hover:bg-accent")}>
      <Lines item={item} />
    </a>
  );
}

export function AttachedInboxItem({ item, remove }: { item: ChatInboxItem; remove: () => void }) {
  const name = item.reference || item.title;
  return (
    <div className={cn(card, "relative w-56 max-w-full pr-9")}>
      <Lines item={item} />
      <Button
        type="button"
        variant="secondary"
        size="icon-sm"
        aria-label={`Remove ${name}`}
        title={`Remove ${name}`}
        className="absolute top-0.5 right-0.5 rounded-full max-md:size-8"
        onMouseDown={(event) => event.preventDefault()}
        onClick={remove}
      >
        <XIcon />
      </Button>
    </div>
  );
}
