import { XIcon } from "lucide-react";
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

// The pending actions of a chat, oldest first, above the composer. The Owner selects one choice of one row. With a
// choice, the tray shows only that row.
export function ActionTray({
  rows,
  choice,
  choose,
  clear,
  show,
}: {
  rows: Approval[];
  choice?: Choice;
  choose: (id: number, decision: Decision) => void;
  clear: () => void;
  show: (item: Approval) => void;
}) {
  if (rows.length === 0) {
    return null;
  }
  const chosen = choice && rows.find((item) => item.id === choice.id);
  const shown = chosen ? [chosen] : rows;
  return (
    <div className="grid grid-cols-1 gap-1 border-t py-2 md:px-4">
      <div className="flex items-center gap-2 px-4 md:px-0">
        <h3
          className={`min-w-0 grow text-xs font-semibold tracking-wide text-muted-foreground wrap-anywhere ${chosen ? "" : "uppercase"}`}
        >
          {chosen && choice
            ? `${decisionName[choice.decision]}: ${chosen.title}`
            : `Waits for you (${rows.length})`}
        </h3>
        {chosen && (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Close"
            title="Close"
            onClick={clear}
          >
            <XIcon />
          </Button>
        )}
      </div>
      <ul aria-label="Pending actions" className="max-h-[35svh] divide-y overflow-y-auto md:-mx-2">
        {shown.map((item) => (
          <Row key={item.id} className="flex-wrap gap-y-1 py-2">
            {!chosen && (
              <span className="min-w-0 grow basis-full wrap-anywhere sm:basis-0">{item.title}</span>
            )}
            <div className="flex basis-full items-center gap-2 sm:basis-auto sm:shrink-0">
              <Button variant="ghost" size="sm" onClick={() => show(item)}>
                Show
              </Button>
              {!chosen && (
                <div className="ml-auto flex items-center gap-2">
                  {(["approve", "reject"] as const).map((decision) => (
                    <Button
                      key={decision}
                      size="sm"
                      variant="outline"
                      onClick={() => choose(item.id, decision)}
                    >
                      {decisionName[decision]}
                    </Button>
                  ))}
                </div>
              )}
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
  decision,
  error,
  onError,
  decided,
}: {
  action: PendingAction;
  decision: Decision;
  error: string;
  onError: (error: string) => void;
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
      placeholder={
        decision === "approve"
          ? `Add a note for the ${agent} (optional)`
          : `Tell the ${agent} why (optional)`
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
