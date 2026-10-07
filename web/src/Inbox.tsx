import { use, useState } from "react";
import { dismiss, InboxItemKind, resume, type InboxItem, type Workstream } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { LoginContext } from "@/lib/login";
import { dayClock } from "@/lib/time";

export function Inbox({
  organization,
  items,
  workstreams,
}: {
  organization: string;
  items: InboxItem[];
  workstreams?: Workstream[];
}) {
  const showLogin = use(LoginContext);
  const [error, setError] = useState("");
  const [pending, setPending] = useState<{ id: number; call: typeof dismiss }[]>([]);
  const shown = items
    .filter((item) => item.organization === organization)
    .toSorted((a, b) => b.id - a.id);

  const act = (call: typeof dismiss, id: number) => {
    setPending((list) => [...list, { id, call }]);
    call(id)
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
        setPending((list) => list.filter((other) => other.id !== id));
      })
      .catch((err: unknown) => {
        setError(String(err));
        setPending((list) => list.filter((other) => other.id !== id));
      });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Inbox</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-2">
        {error && (
          <Badge variant="destructive" className="h-auto w-full justify-start whitespace-normal">
            {error}
          </Badge>
        )}
        {shown.length === 0 && <p className="text-muted-foreground">Nothing waits for you.</p>}
        <ul className="divide-y">
          {shown.map((item) => {
            const busy = pending.some((other) => other.id === item.id);
            const running = (call: typeof dismiss) =>
              pending.some((other) => other.id === item.id && other.call === call);
            return (
              <li key={item.id} className="flex flex-wrap items-center gap-x-3 gap-y-2 py-3">
                <Badge variant="secondary">{item.kind}</Badge>
                <div className="grid min-w-0 grow basis-60 gap-0.5">
                  <span className="break-words">{item.text}</span>
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
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  {item.kind === InboxItemKind.usage_limit ? (
                    <Button
                      size="sm"
                      disabled={busy}
                      pending={running(resume)}
                      onClick={() => act(resume, item.id)}
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
                    disabled={busy}
                    pending={running(dismiss)}
                    onClick={() => act(dismiss, item.id)}
                  >
                    Dismiss
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      </CardContent>
    </Card>
  );
}
