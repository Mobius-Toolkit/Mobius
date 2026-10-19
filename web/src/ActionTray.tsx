import { BanIcon, CheckIcon, XIcon } from "lucide-react";
import { use } from "react";
import { Button } from "@/components/ui/button";
import { Row } from "@/components/page";
import {
  decideAction,
  roleName,
  type Approval,
  type Decision,
  type PendingAction,
} from "@/lib/actions";
import { actionDraftKey } from "@/lib/draft";
import { LoginContext } from "@/lib/login";
import { MessageInput } from "./MessageInput";

export type Choice = { id: number; decision: Decision };

const decisionName: Record<Decision, string> = { approve: "Approve", reject: "Reject" };

// The pending actions of a chat, oldest first, above the composer. The Owner selects one choice of one row.
export function ActionTray({
  rows,
  choice,
  choose,
  show,
}: {
  rows: Approval[];
  choice?: Choice;
  choose: (id: number, decision: Decision) => void;
  show: (item: Approval) => void;
}) {
  if (rows.length === 0) {
    return null;
  }
  return (
    <div className="grid grid-cols-1 gap-1 border-t py-2 md:px-4">
      <h3 className="px-4 text-xs font-semibold tracking-wide text-muted-foreground uppercase md:px-0">
        Waits for you ({rows.length})
      </h3>
      <ul className="max-h-[35svh] divide-y overflow-y-auto md:-mx-2">
        {rows.map((item) => (
          <Row key={item.id} className="flex-wrap gap-y-1 py-2">
            <span className="flex min-w-0 grow basis-full flex-wrap items-baseline gap-x-2 sm:basis-0 sm:flex-nowrap">
              <span className="shrink-0 font-mono text-sm">{item.action.tool}</span>
              <span className="min-w-0 truncate">{item.title}</span>
            </span>
            <div className="flex basis-full items-center gap-2 sm:basis-auto sm:shrink-0">
              <Button variant="ghost" size="sm" onClick={() => show(item)}>
                Show
              </Button>
              <div className="ml-auto flex items-center gap-2">
                {(["approve", "reject"] as const).map((decision) => {
                  const selected = choice?.id === item.id && choice.decision === decision;
                  return (
                    <Button
                      key={decision}
                      size="sm"
                      variant={selected ? "default" : "outline"}
                      aria-pressed={selected}
                      onClick={() => choose(item.id, decision)}
                    >
                      {decisionName[decision]}
                    </Button>
                  );
                })}
              </div>
            </div>
            {item.action.error && (
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

// The composer in decision mode. The text is the optional message of the Owner with the decision.
export function DecisionInput({
  action,
  title,
  decision,
  error,
  onError,
  clear,
  decided,
}: {
  action: PendingAction;
  title: string;
  decision: Decision;
  error: string;
  onError: (error: string) => void;
  clear: () => void;
  decided: (action: PendingAction) => void;
}) {
  const showLogin = use(LoginContext);
  const agent = roleName[action.role];

  const send = async (message: string) => {
    try {
      const res = await decideAction(action.id, decision, message);
      if (res.status === 200) {
        decided(res.action);
        return res.action.state !== "pending";
      }
      if (res.status === 401) {
        showLogin();
      } else {
        onError(res.error);
      }
    } catch (err) {
      onError(String(err));
    }
    return false;
  };

  return (
    <MessageInput
      draftKey={actionDraftKey(action.id)}
      label={`Note for the ${agent}`}
      placeholder={`Add a note for the ${agent} (optional)`}
      heading={
        <div className="flex items-center gap-2 pl-1 text-sm">
          {decision === "approve" ? (
            <CheckIcon className="size-4 shrink-0" />
          ) : (
            <BanIcon className="size-4 shrink-0" />
          )}
          <span className="min-w-0 grow truncate">
            {decisionName[decision]} <span className="font-mono">{action.tool}</span> · {title}
          </span>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Clear the choice"
            title="Clear the choice"
            onMouseDown={(event) => event.preventDefault()}
            onClick={clear}
          >
            <XIcon />
          </Button>
        </div>
      }
      sendEmpty
      images={false}
      error={error}
      onError={onError}
      stopping={false}
      onSend={send}
    />
  );
}
