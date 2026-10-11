import { ChevronDownIcon } from "lucide-react";
import { use, useState } from "react";
import {
  dismiss,
  type dismissResponse,
  InboxItemKind,
  resumeIssue,
  type resumeIssueResponse,
  type InboxItem,
  type Workstream,
} from "@/api/api.gen";
import { ErrorBadge, inset, List, Row } from "@/components/page";
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
    .toSorted((a, b) => a.id - b.id);

  return (
    <div className="grid grid-cols-1 gap-2">
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

const link = "text-primary underline-offset-4 hover:underline";

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
  const [running, setRunning] = useState<"action" | "dismiss" | null>(null);
  const [open, setOpen] = useState(false);

  const act = (
    name: "action" | "dismiss",
    call: () => Promise<dismissResponse | resumeIssueResponse>,
  ) => {
    setRunning(name);
    call()
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

  const resume = () => {
    const [owner, name] = item.repository.split("/");
    act("action", () => resumeIssue(owner, name, item.issue));
  };

  const isPullRequest = item.kind === InboxItemKind.ready_for_review;
  const reference = isPullRequest ? `PR #${item.pullRequest}` : `#${item.issue}`;
  const referenceLink = isPullRequest ? item.pullRequestUrl : item.link;

  return (
    <Row className="flex-wrap gap-y-2 py-2">
      <div className="flex min-w-0 grow basis-full flex-wrap items-center gap-x-2 gap-y-2 sm:flex-nowrap">
        <span className="flex min-w-0 grow basis-full items-baseline gap-2 sm:basis-0">
          {referenceLink ? (
            <a
              href={referenceLink}
              target="_blank"
              rel="noreferrer"
              className={cn("shrink-0", link)}
            >
              {reference}
            </a>
          ) : (
            <span className="shrink-0">{reference}</span>
          )}
          <span className="truncate">{item.title}</span>
          <span className="truncate text-sm text-muted-foreground">{item.reason}</span>
        </span>
        <div className="flex shrink-0 items-center gap-2">
          {item.kind === InboxItemKind.question && (
            <Button size="sm" asChild>
              <a href={item.link} target="_blank" rel="noreferrer">
                Answer
              </a>
            </Button>
          )}
          {(item.kind === InboxItemKind.stopped || item.kind === InboxItemKind.lost) && (
            <Button
              size="sm"
              disabled={running !== null}
              pending={running === "action"}
              onClick={resume}
            >
              {item.kind === InboxItemKind.stopped ? "Resume" : "Start again"}
            </Button>
          )}
          {isPullRequest && (
            <Button size="sm" asChild>
              <a href={item.pullRequestUrl} target="_blank" rel="noreferrer">
                Open PR
              </a>
            </Button>
          )}
          <Button variant="ghost" size="sm" aria-expanded={open} onClick={() => setOpen(!open)}>
            Details
            <ChevronDownIcon className={cn("size-4", open && "rotate-180")} />
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={running !== null}
            pending={running === "dismiss"}
            onClick={() => act("dismiss", () => dismiss(item.id))}
          >
            Dismiss
          </Button>
        </div>
      </div>
      {open && (
        <div className="grid basis-full gap-1 text-sm">
          <span className="break-words whitespace-pre-wrap">{localTimes(item.text)}</span>
          <span className="text-muted-foreground">
            {workstreams?.find(
              (workstream) =>
                workstream.repository === item.repository && workstream.number === item.workstream,
            )?.title ?? `#${item.workstream}`}{" "}
            · {dayClock(item.time)}
          </span>
          {item.reportUrl && (
            <a href={item.reportUrl} target="_blank" rel="noreferrer" className={link}>
              Approval report
            </a>
          )}
        </div>
      )}
    </Row>
  );
}
