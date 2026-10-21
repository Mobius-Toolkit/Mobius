import { use, useState } from "react";
import {
  dismiss,
  type dismissResponse,
  type InboxItem,
  InboxItemKind,
  mergeInboxItem,
  resumeIssue,
  type resumeIssueResponse,
  sendAgain,
  type sendAgainResponse,
  type Workstream,
} from "@/api/api.gen";
import { ErrorBadge, inset, List, Row } from "@/components/page";
import { Button } from "@/components/ui/button";
import { LoginContext } from "@/lib/login";
import { useShell } from "@/lib/shell";
import { cn } from "@/lib/utils";
import { AnswerPanel } from "./AnswerPanel";

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

export const link = "text-primary underline-offset-4 hover:underline";

export function InboxRow({
  item,
  workstreams,
  setError,
}: {
  item: InboxItem;
  workstreams?: Workstream[];
  setError: (error: string) => void;
}) {
  const showLogin = use(LoginContext);
  const [running, setRunning] = useState<"action" | "dismiss" | "merge" | null>(null);
  const [refusal, setRefusal] = useState("");
  const [answered, setAnswered] = useState<{ item: InboxItem; merging: boolean } | null>(null);
  const { inboxFailure } = useShell();
  const [panel, setPanel] = useState<"answer" | "details" | null>(
    inboxFailure?.id === item.id ? "answer" : null,
  );

  const act = (
    name: "action" | "dismiss",
    call: () => Promise<dismissResponse | resumeIssueResponse | sendAgainResponse>,
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

  const merge = () => {
    setRunning("merge");
    setRefusal("");
    mergeInboxItem(item.id)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setAnswered({ item, merging: res.data.data.merging });
        } else {
          setRefusal(res.data.error);
        }
        setRunning(null);
      })
      .catch((err: unknown) => {
        setRefusal(String(err));
        setRunning(null);
      });
  };

  const resume = () => {
    const [owner, name] = item.repository.split("/");
    act("action", () => resumeIssue(owner, name, item.issue));
  };

  const isPullRequest = item.kind === InboxItemKind.ready_for_review;
  const reference =
    item.action && !item.issue ? "" : isPullRequest ? `PR #${item.pullRequest}` : `#${item.issue}`;
  const referenceLink = isPullRequest ? item.pullRequestUrl : item.link;
  const merging = answered?.item === item ? answered.merging : item.merging;
  const refusalText = refusal || (merging ? "" : item.mergeRefusal);

  return (
    <Row className="flex-wrap gap-y-2 py-2">
      <div className="flex min-w-0 grow basis-full flex-wrap items-center gap-x-2 gap-y-2 sm:flex-nowrap">
        <span className="flex min-w-0 grow basis-full flex-wrap items-baseline gap-x-2 sm:basis-0 sm:flex-nowrap">
          {reference &&
            (referenceLink ? (
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
            ))}
          <span className="contents sm:flex sm:min-w-0 sm:items-baseline sm:gap-x-2">
            <span className="grow basis-0 truncate sm:max-w-full sm:shrink-0 sm:grow-0 sm:basis-auto">
              {item.title}
            </span>
            <span className="basis-full truncate text-sm text-muted-foreground sm:basis-auto">
              {item.reason}
            </span>
          </span>
        </span>
        <div className="flex basis-full items-center gap-2 sm:basis-auto sm:shrink-0 sm:gap-4">
          <div className="flex flex-wrap items-center gap-2 empty:hidden">
            {(item.kind === InboxItemKind.question || item.action) && (
              <Button size="sm" onClick={() => setPanel("answer")}>
                Answer
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
            {item.kind === InboxItemKind.stopped && (
              <Button variant="outline" size="sm" onClick={() => setPanel("answer")}>
                Answer
              </Button>
            )}
            {(item.kind === InboxItemKind.question || item.kind === InboxItemKind.stopped) && (
              <Button variant="outline" size="sm" asChild>
                <a href={item.link} target="_blank" rel="noreferrer">
                  Open GitHub
                </a>
              </Button>
            )}
            {item.kind === InboxItemKind.Lead_failed && (
              <Button
                size="sm"
                disabled={running !== null}
                pending={running === "action"}
                onClick={() => act("action", () => sendAgain(item.id))}
              >
                Send again
              </Button>
            )}
            {isPullRequest && (
              <Button size="sm" pending={running === "merge" || merging} onClick={merge}>
                {running === "merge" || merging ? "Merging" : "Merge"}
              </Button>
            )}
            {isPullRequest && (
              <Button variant="outline" size="sm" asChild>
                <a href={item.pullRequestUrl} target="_blank" rel="noreferrer">
                  Open PR
                </a>
              </Button>
            )}
          </div>
          <div className="ml-auto flex items-center gap-2">
            {item.kind !== InboxItemKind.question &&
              item.kind !== InboxItemKind.stopped &&
              !item.action && (
                <Button variant="ghost" size="sm" onClick={() => setPanel("details")}>
                  Details
                </Button>
              )}
            {item.kind === InboxItemKind.Lead_failed && (
              <Button
                variant="outline"
                size="sm"
                disabled={running !== null}
                pending={running === "dismiss"}
                onClick={() => act("dismiss", () => dismiss(item.id))}
              >
                Dismiss
              </Button>
            )}
          </div>
        </div>
      </div>
      {refusalText && (
        <p role="alert" className="basis-full text-sm text-destructive wrap-anywhere">
          {refusalText}
        </p>
      )}
      {panel && (
        <AnswerPanel
          item={item}
          input={panel === "answer"}
          workstream={
            workstreams?.find(
              (workstream) =>
                workstream.repository === item.repository && workstream.number === item.workstream,
            )?.title ?? `#${item.workstream}`
          }
          onClose={() => setPanel(null)}
        />
      )}
    </Row>
  );
}
