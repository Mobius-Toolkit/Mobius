import { use, useState } from "react";
import { dismiss, InboxItemKind, resume, type InboxItem, type Workstream } from "@/api/api.gen";
import { ErrorBadge, inset, List, Row } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { LoginContext } from "@/lib/login";
import { dayClock, localTimes } from "@/lib/time";
import { cn } from "@/lib/utils";

export function Inbox({
  organization,
  items,
  workstreams,
}: {
  organization: string;
  items: InboxItem[];
  workstreams?: Workstream[];
}) {
  const [error, setError] = useState("");
  const shown = items
    .filter((item) => item.organization === organization)
    .toSorted((a, b) => b.id - a.id);

  return (
    <div className="grid gap-2">
      {error && <ErrorBadge>{error}</ErrorBadge>}
      {shown.length === 0 && (
        <p className={cn("text-muted-foreground", inset)}>Nothing waits for you.</p>
      )}
      <List>
        {shown.map((item) => (
          <InboxRow key={item.id} item={item} workstreams={workstreams} setError={setError} />
        ))}
      </List>
    </div>
  );
}

function InboxRow({
  item,
  workstreams,
  setError,
}: {
  item: InboxItem;
  workstreams?: Workstream[];
  setError: (error: string) => void;
}) {
  const showLogin = use(LoginContext);
  const [running, setRunning] = useState<typeof dismiss | null>(null);

  const act = (call: typeof dismiss) => {
    setRunning(() => call);
    call(item.id)
      .then((res) => {
        if (res.status === 204) {
          setError("");
          return;
        }
        if (res.status === 401) {
          showLogin();
        } else {
          setError(res.data.error);
        }
        setRunning(null);
      })
      .catch((err: unknown) => {
        setError(String(err));
        setRunning(null);
      });
  };

  return (
    <Row className="flex-wrap gap-x-3 gap-y-2 py-3">
      <Badge variant="secondary">{item.kind}</Badge>
      <div className="grid min-w-0 grow basis-60 gap-0.5">
        <span className="break-words">{localTimes(item.text)}</span>
        <span className="text-sm text-muted-foreground">
          {item.kind !== InboxItemKind.usage_limit && (
            <>
              {workstreams?.find(
                (workstream) =>
                  workstream.repository === item.repository &&
                  workstream.number === item.workstream,
              )?.title ?? `#${item.workstream}`}{" "}
              · #{item.issue} ·{" "}
            </>
          )}
          {dayClock(item.time)}
          {item.pausedUntil && ` · paused until ${dayClock(item.pausedUntil)}`}
        </span>
      </div>
      <div className="flex items-center gap-2">
        {item.kind === InboxItemKind.usage_limit ? (
          <Button
            size="sm"
            disabled={running !== null}
            pending={running === resume}
            onClick={() => act(resume)}
          >
            Resume now
          </Button>
        ) : (
          item.link && (
            <Button asChild variant="link" size="sm">
              <a href={item.link} target="_blank" rel="noreferrer">
                Open on GitHub
              </a>
            </Button>
          )
        )}
        <Button
          variant="outline"
          size="sm"
          disabled={running !== null}
          pending={running === dismiss}
          onClick={() => act(dismiss)}
        >
          Dismiss
        </Button>
      </div>
    </Row>
  );
}
