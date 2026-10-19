import { use, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { decideAction, type ActionMessage, type Decision, type PendingAction } from "@/lib/actions";
import { LoginContext } from "@/lib/login";
import { clock } from "@/lib/time";
import { Markdown } from "./Markdown";

function actionState(action: PendingAction) {
  if (action.state === "pending") {
    return action.error ? "Failed" : "Waits for the approval";
  }
  return { done: "Done", rejected: "Rejected", replaced: "Replaced" }[action.state];
}

export function ActionDialog({
  action,
  decision,
  onDecided,
  onClose,
}: {
  action: PendingAction;
  decision: Decision;
  onDecided: (action: PendingAction) => void;
  onClose: () => void;
}) {
  const showLogin = use(LoginContext);
  const [message, setMessage] = useState("");
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const label = decision === "approve" ? "Approve" : "Reject";

  const send = async () => {
    setRunning(true);
    setError("");
    try {
      const res = await decideAction(action.id, decision, message);
      if (res.status === 200) {
        onDecided(res.action);
        onClose();
        return;
      }
      if (res.status === 401) {
        showLogin();
      } else {
        setError(res.error);
      }
    } catch (err) {
      setError(String(err));
    }
    setRunning(false);
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>
            {label} <span className="font-mono">{action.tool}</span>
          </DialogTitle>
        </DialogHeader>
        <Textarea
          aria-label="Your message"
          placeholder="Write a message for the agent"
          value={message}
          onChange={(event) => setMessage(event.target.value)}
        />
        {error && (
          <p role="alert" className="text-sm text-destructive wrap-anywhere">
            {error}
          </p>
        )}
        <Button
          variant={decision === "approve" ? "default" : "outline"}
          pending={running}
          onClick={() => void send()}
        >
          {label}
        </Button>
      </DialogContent>
    </Dialog>
  );
}

function Argument({ name, value }: { name: string; value: string }) {
  return (
    <div className="grid min-w-0 gap-0.5">
      <span className="text-xs text-muted-foreground">{name}</span>
      {value.includes("\n") || value.length > 80 ? (
        <Markdown text={value} />
      ) : (
        <span className="wrap-anywhere">{value}</span>
      )}
    </div>
  );
}

function Outcome({ label, text }: { label: string; text: string }) {
  return (
    <div className="grid min-w-0 gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="wrap-anywhere whitespace-pre-line">{text}</span>
    </div>
  );
}

export function ActionCard({ message, action }: { message: ActionMessage; action: PendingAction }) {
  const [decided, setDecided] = useState<{ from: PendingAction; action: PendingAction }>();
  const [dialog, setDialog] = useState<Decision | null>(null);
  const shown = decided?.from === action ? decided.action : action;
  const failed = shown.state === "pending" && shown.error !== "";

  return (
    <div
      data-message={message.id}
      data-action={shown.id}
      className="grid max-w-full grid-cols-[minmax(0,1fr)] gap-2 justify-self-stretch rounded-xl border bg-card px-3 py-2"
    >
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span>{message.author}</span>
        <span>{clock(message.time)}</span>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className="font-mono">
          {shown.tool}
        </Badge>
        <Badge variant={failed ? "destructive" : "secondary"}>{actionState(shown)}</Badge>
      </div>
      {shown.arguments.map((argument) => (
        <Argument key={argument.name} {...argument} />
      ))}
      {shown.state === "done" && <Outcome label="Result" text={shown.result} />}
      {failed && <Outcome label="Error" text={shown.error} />}
      {shown.message && <Outcome label="Your message" text={shown.message} />}
      {shown.state === "pending" && (
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => setDialog("approve")}>
            Approve
          </Button>
          <Button variant="outline" size="sm" onClick={() => setDialog("reject")}>
            Reject
          </Button>
        </div>
      )}
      {dialog && (
        <ActionDialog
          action={shown}
          decision={dialog}
          onDecided={(next) => setDecided({ from: action, action: next })}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  );
}
