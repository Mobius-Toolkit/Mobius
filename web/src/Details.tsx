import { use, useCallback, useEffect, useId, useState } from "react";
import {
  clearWorkstreamImplementer,
  clearWorkstreamLead,
  closeWorkstream,
  getWorkstreamDetails,
  listRoles,
  setEvergreen,
  setWorkstreamImplementer,
  setWorkstreamLead,
  workstreamClosure,
  type ClosureItem,
  type IssueSetting,
  type LiveEvents,
  type NextSetting,
  type Role,
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
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { onEvent } from "@/lib/events";
import { bindingText } from "@/lib/harness";
import { LoginContext } from "@/lib/login";
import { showNotice } from "@/lib/notice";
import { dayLong } from "@/lib/time";
import { SettingSheet } from "./SettingSheet";

type Workstream = { owner: string; name: string; number: number };

type Session = "lead" | "implementer";

const sessionTitle: Record<Session, string> = {
  lead: "Lead",
  implementer: "Implementer of the Workstream",
};

function BindingLine({
  term,
  setting,
  onChange,
}: {
  term: string;
  setting: NextSetting;
  onChange: () => void;
}) {
  return (
    <>
      <dt className="text-muted-foreground">{term}</dt>
      <dd className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <span className="wrap-anywhere">{bindingText(setting)}</span>
        <span className="text-sm text-muted-foreground">{setting.own ? "Workstream" : "role"}</span>
        <Button variant="outline" size="sm" aria-label={`Change ${term}`} onClick={onChange}>
          Change
        </Button>
      </dd>
    </>
  );
}

function Confirmation({ owner, name, number }: Workstream) {
  const showLogin = use(LoginContext);
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
          showNotice(`Closed the Workstream #${number} as won't do`);
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
            <li
              key={`${item.kind} ${item.repository}#${item.number}`}
              className="flex items-baseline gap-2"
            >
              <Badge variant="secondary" className="shrink-0">
                {item.kind === "pullRequest" ? "pull request" : "issue"}
              </Badge>
              <span>
                {item.repository === `${owner}/${name}` ? "" : item.repository}#{item.number}{" "}
                {item.title}
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
  const [evergreenBusy, setEvergreenBusy] = useState(false);
  const [roles, setRoles] = useState<Role[]>();
  const [editing, setEditing] = useState<Session>();
  const evergreenId = useId();

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
    listRoles()
      .then((res) => {
        if (res.status === 200) {
          setRoles(res.data.data);
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

  const switchEvergreen = (on: boolean) => {
    setEvergreenBusy(true);
    setError(undefined);
    setEvergreen(owner, name, number, { on })
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 204) {
          setDetails((current) => current && { ...current, evergreen: on });
          showNotice(`Evergreen is ${on ? "on" : "off"} for #${number}`);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setEvergreenBusy(false));
  };

  const managed = details?.maintenance || details?.skills;
  const role = roles?.find((other) => other.role === editing);

  const changeSetting = (session: Session, setting: IssueSetting | null) => {
    const next = setting ? { ...setting, own: true } : role && { ...role, own: false };
    setDetails((current) => current && next && { ...current, [session]: next });
    setEditing(undefined);
  };

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
          <BindingLine term="Lead" setting={details.lead} onChange={() => setEditing("lead")} />
          <BindingLine
            term="Implementer"
            setting={details.implementer}
            onChange={() => setEditing("implementer")}
          />
        </dl>
      )}
      {details && editing && role && (
        <SettingSheet
          target={{
            title: sessionTitle[editing],
            own: details[editing].own ? details[editing] : null,
            fallback: role,
            fallbackSource: "role",
            save: (setting) =>
              (editing === "lead" ? setWorkstreamLead : setWorkstreamImplementer)(
                owner,
                name,
                number,
                setting,
              ),
            clear: () =>
              (editing === "lead" ? clearWorkstreamLead : clearWorkstreamImplementer)(
                owner,
                name,
                number,
              ),
          }}
          onChange={(setting) => changeSetting(editing, setting)}
          onClose={() => setEditing(undefined)}
        />
      )}
      <div className="grid gap-1">
        <div className="flex items-center gap-2">
          <Switch
            id={evergreenId}
            checked={details?.evergreen ?? false}
            disabled={!details || managed || evergreenBusy}
            onCheckedChange={switchEvergreen}
          />
          <Label htmlFor={evergreenId}>Evergreen</Label>
        </div>
        {managed && (
          <p className="text-sm text-muted-foreground">
            Mobius manages this Workstream, so this value cannot change.
          </p>
        )}
      </div>
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
    </div>
  );
}
