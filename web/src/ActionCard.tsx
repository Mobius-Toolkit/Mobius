import { useLayoutEffect, useRef, useState } from "react";
import type { ChatMessage, PendingAction } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { clock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { Markdown } from "./Markdown";

// The height of about six lines of text. A longer value shows only this part until the Owner opens it.
const collapsedHeight = 144;

function actionState(action: PendingAction) {
  if (action.state === "pending") {
    return action.error ? "Failed" : "Waits for the approval";
  }
  return {
    done: "Done",
    rejected: "Rejected",
    replaced: "Replaced",
    withdrawn: "Withdrawn by the agent",
  }[action.state];
}

const isShort = (value: string) => !value.includes("\n") && value.length <= 80;

export function FullArgument({ name, value }: { name: string; value: string }) {
  return (
    <div className="grid min-w-0 gap-0.5">
      <span className="text-xs text-muted-foreground">{name}</span>
      {isShort(value) ? <span className="wrap-anywhere">{value}</span> : <Markdown text={value} />}
    </div>
  );
}

function Argument({
  name,
  value,
  open,
  toggle,
}: {
  name: string;
  value: string;
  open: boolean;
  toggle: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [long, setLong] = useState(false);
  useLayoutEffect(() => {
    const content = box.current?.firstElementChild;
    if (!content) {
      return;
    }
    const observer = new ResizeObserver(() => setLong(content.clientHeight > collapsedHeight));
    observer.observe(content);
    return () => observer.disconnect();
  }, []);

  if (isShort(value)) {
    return <FullArgument name={name} value={value} />;
  }
  return (
    <div className="grid min-w-0 gap-0.5">
      <span className="text-xs text-muted-foreground">{name}</span>
      <div ref={box} className={cn("min-w-0", !open && "max-h-36 overflow-hidden")}>
        <Markdown text={value} />
      </div>
      {long && (
        <Button variant="ghost" size="sm" className="justify-self-start" onClick={toggle}>
          {open ? "Show less" : "Show all"}
        </Button>
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

// The record of the action in the chat history. The Owner decides in the panel that the tray opens.
export function ActionCard({ message, action }: { message: ChatMessage; action: PendingAction }) {
  const [open, setOpen] = useState<string[]>([]);
  const failed = action.state === "pending" && action.error !== "";

  return (
    <div
      data-message={message.id}
      data-action={action.id}
      className="grid max-w-full grid-cols-[minmax(0,1fr)] gap-2 justify-self-stretch rounded-xl border bg-card px-3 py-2"
    >
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span>{message.author}</span>
        <span>{clock(message.time)}</span>
      </div>
      <Badge variant="outline" className="justify-self-start font-mono">
        {action.tool}
      </Badge>
      {action.arguments.map((argument) => (
        <Argument
          key={argument.name}
          {...argument}
          open={open.includes(argument.name)}
          toggle={() =>
            setOpen((names) =>
              names.includes(argument.name)
                ? names.filter((name) => name !== argument.name)
                : [...names, argument.name],
            )
          }
        />
      ))}
      <Badge variant={failed ? "destructive" : "secondary"} className="justify-self-start">
        {actionState(action)}
      </Badge>
      {action.state === "done" && <Outcome label="Result" text={action.result} />}
      {failed && <Outcome label="Error" text={action.error} />}
      {(action.state === "done" || action.state === "rejected") && action.message && (
        <Outcome label="Your message" text={action.message} />
      )}
    </div>
  );
}
