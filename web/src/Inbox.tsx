import { useNavigate } from "@tanstack/react-router";
import { use, useState } from "react";
import {
  dismiss,
  type dismissResponse,
  type InboxItem,
  InboxItemKind,
  mergeInboxItem,
  type PendingAction,
  resumeIssue,
  type resumeIssueResponse,
  sendAgain,
  type sendAgainResponse,
  type Workstream,
} from "@/api/api.gen";
import { ErrorBadge, inset, List, Row } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { addDraftInboxItem, chatDraftKey } from "@/lib/draft";
import { LoginContext } from "@/lib/login";
import { issueRef, showNotice } from "@/lib/notice";
import { useShell } from "@/lib/shell";
import { cn } from "@/lib/utils";
import { chatParams } from "@/lib/workstreams";
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
  onDecided,
}: {
  item: InboxItem;
  workstreams?: Workstream[];
  setError: (error: string) => void;
  onDecided?: (action: PendingAction) => void;
}) {
  const showLogin = use(LoginContext);
  const { organization, selectOrganization } = useShell();
  const navigate = useNavigate();
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
    notice?: string,
  ) => {
    setRunning(name);
    call()
      .then((res) => {
        if (res.status === 204) {
          setError("");
          if (notice) {
            showNotice(notice);
          }
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
          showNotice(
            `Started the merge of PR ${issueRef(item.repository, item.pullRequest, item.workstreamRepository)}`,
          );
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
    act(
      "action",
      () => resumeIssue(owner, name, item.issue),
      `Resumed ${issueRef(item.repository, item.issue, item.workstreamRepository)}`,
    );
  };

  const isPullRequest = item.kind === InboxItemKind.ready_for_review;
  const isTodo = item.kind === InboxItemKind.todo;
  const reference = !item.issue
    ? ""
    : isPullRequest || isTodo
      ? `PR #${item.pullRequest}`
      : `#${item.issue}`;
  const referenceLink = isPullRequest || isTodo ? item.pullRequestUrl : item.link;
  const chat = async () => {
    const inWorkstream = item.workstream !== 0;
    const key = inWorkstream
      ? chatDraftKey(
          item.workstreamRepository.split("/")[0],
          item.workstreamRepository,
          item.workstream,
        )
      : chatDraftKey(item.organization, "", 0);
    await addDraftInboxItem(key, {
      id: item.id,
      reference,
      title: item.title,
      reason: item.reason,
      link: referenceLink,
    });
    if (inWorkstream) {
      await navigate({
        to: "/workstreams/$owner/$name/$number",
        params: chatParams({ repository: item.workstreamRepository, number: item.workstream }),
      });
      return;
    }
    if (item.organization !== organization) {
      selectOrganization(item.organization);
    }
    await navigate({ to: "/chat" });
  };
  const merging = answered?.item === item ? answered.merging : item.merging;
  const refusalText = refusal || (merging ? "" : item.mergeRefusal) || item.action?.error;

  return (
    <Row className="flex-wrap gap-y-2 py-2">
      <div className="flex min-w-0 grow basis-full flex-wrap items-center gap-x-2 gap-y-2 sm:flex-nowrap">
        <span className="flex min-w-0 grow basis-full flex-wrap items-baseline gap-x-2 sm:basis-0 sm:flex-nowrap">
          {item.todo && (
            <Badge variant={item.todo.mandatory ? "default" : "outline"}>
              Todo · {item.todo.mandatory ? "mandatory" : "optional"}
            </Badge>
          )}
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
            <span className="grow basis-0 truncate sm:max-w-full sm:shrink sm:grow-0 sm:basis-auto">
              {item.title}
            </span>
            <span className="basis-full truncate text-sm text-muted-foreground sm:shrink-0 sm:basis-auto">
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
            {(item.kind === InboxItemKind.question || item.kind === InboxItemKind.stopped) &&
              item.link && (
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
            {isTodo && (
              <Button size="sm" onClick={() => setPanel("answer")}>
                Complete
              </Button>
            )}
            {isPullRequest && (
              <Button size="sm" pending={running === "merge" || merging} onClick={merge}>
                {running === "merge" || merging ? "Merging" : "Merge"}
              </Button>
            )}
            {(isPullRequest || isTodo) && (
              <Button variant="outline" size="sm" asChild>
                <a href={item.pullRequestUrl} target="_blank" rel="noreferrer">
                  Open PR
                </a>
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={() => void chat()}>
              Chat
            </Button>
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
                onClick={() =>
                  act("dismiss", () => dismiss(item.id), `Dismissed: ${item.title || item.reason}`)
                }
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
            item.workstream === 0
              ? "Coordinator"
              : (workstreams?.find(
                  (workstream) =>
                    workstream.repository === item.workstreamRepository &&
                    workstream.number === item.workstream,
                )?.title ?? `#${item.workstream}`)
          }
          onClose={() => setPanel(null)}
          onDecided={onDecided}
        />
      )}
    </Row>
  );
}
