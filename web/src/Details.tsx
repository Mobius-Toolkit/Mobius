import { use, useCallback, useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import {
  closeWorkstream,
  getWorkstreamDetails,
  workstreamClosure,
  type ClosureItem,
  type LiveEvents,
  type WorkstreamDetails,
} from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { onEvent } from "@/lib/events";
import { LoginContext } from "@/lib/login";
import { dayLong } from "@/lib/time";

type Workstream = { owner: string; name: string; number: number };

function Confirmation({ owner, name, number }: Workstream) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const [items, setItems] = useState<ClosureItem[]>();
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    workstreamClosure(owner, name, number)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setItems(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [owner, name, number, showLogin]);

  const close = () => {
    setBusy(true);
    setError(undefined);
    closeWorkstream(owner, name, number)
      .then((res) => {
        if (res.status === 204) {
          void navigate({ to: "/workstreams" });
        } else {
          setError(res.data.error);
          setBusy(false);
        }
      })
      .catch((err: unknown) => {
        setError(String(err));
        setBusy(false);
      });
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle>Close the Workstream</DialogTitle>
        <DialogDescription>
          Mobius closes these items as "won't do". Each item gets a comment and the label
          mobius:wont-do.
        </DialogDescription>
      </DialogHeader>
      <div className="grid min-h-0 gap-2 overflow-y-auto">
        {error && (
          <Badge variant="destructive" className="h-auto w-full justify-start whitespace-normal">
            {error}
          </Badge>
        )}
        {!items && !error && <p className="text-muted-foreground">Mobius reads the items.</p>}
        <ul className="grid gap-1">
          {items?.map((item) => (
            <li key={`${item.kind} ${item.number}`} className="flex items-baseline gap-2">
              <Badge variant="secondary" className="shrink-0">
                {item.kind === "pullRequest" ? "pull request" : "issue"}
              </Badge>
              <span>
                #{item.number} {item.title}
              </span>
            </li>
          ))}
        </ul>
      </div>
      <DialogFooter>
        <DialogClose asChild>
          <Button variant="outline">Cancel</Button>
        </DialogClose>
        <Button variant="destructive" disabled={!items} pending={busy} onClick={close}>
          Close as won't do
        </Button>
      </DialogFooter>
    </>
  );
}

export function Details({
  owner,
  name,
  number,
  source,
}: Workstream & {
  source?: EventSource;
}) {
  const showLogin = use(LoginContext);
  const [details, setDetails] = useState<WorkstreamDetails>();
  const [error, setError] = useState<string>();
  const [confirming, setConfirming] = useState(false);

  const load = useCallback(() => {
    getWorkstreamDetails(owner, name, number)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setDetails(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [owner, name, number, showLogin]);

  // The workstreams event also tells of a change of the tasks. An event that comes before the listener or while the
  // connection is down is lost, so each connection reads the details.
  useEffect(() => {
    load();
    if (!source) {
      return;
    }
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "workstreams">(source, "workstreams", load);
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source, load]);

  return (
    <div className="grid gap-4 px-2">
      {error && <Badge variant="destructive">{error}</Badge>}
      {details && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2">
          <dt className="text-muted-foreground">Created</dt>
          <dd>{dayLong(details.createdAt)}</dd>
          <dt className="text-muted-foreground">Completed tasks</dt>
          <dd>{details.completedTasks}</dd>
          <dt className="text-muted-foreground">Open tasks</dt>
          <dd>{details.openTasks}</dd>
        </dl>
      )}
      {details?.open && (
        <>
          <Button
            variant="destructive"
            className="justify-self-start"
            onClick={() => setConfirming(true)}
          >
            Close
          </Button>
          <Dialog open={confirming} onOpenChange={setConfirming}>
            <DialogContent className="flex max-h-[calc(100svh-2rem)] flex-col sm:max-w-lg">
              <Confirmation owner={owner} name={name} number={number} />
            </DialogContent>
          </Dialog>
        </>
      )}
    </div>
  );
}
