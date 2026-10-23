import { useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ActionLabel, ChatMessage, PendingAction } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { briefMarkdown } from "@/lib/briefDiff";
import { diffLines } from "@/lib/diff";
import { clock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { diffMarks } from "./diffMarks";
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

function BriefView({
  before,
  after,
  hideRemoved,
}: {
  before: string;
  after: string;
  hideRemoved: boolean;
}) {
  const marked = useMemo(
    () => briefMarkdown(diffLines(before, after), hideRemoved),
    [before, after, hideRemoved],
  );
  return <Markdown text={marked} overrides={diffMarks} />;
}

function RemovedToggle({ hidden, toggle }: { hidden: boolean; toggle: () => void }) {
  return (
    <Button variant="ghost" size="sm" className="justify-self-start" onClick={toggle}>
      {hidden ? "Show removed text" : "Hide removed text"}
    </Button>
  );
}

function textColor(color: string) {
  const [red, green, blue] = [0, 2, 4].map((start) => parseInt(color.slice(start, start + 2), 16));
  return (0.2126 * red + 0.7152 * green + 0.0722 * blue) / 255 > 0.453 ? "#000000" : "#ffffff";
}

function LabelChips({ labels }: { labels: ActionLabel[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {labels.map((label) =>
        label.color ? (
          <Badge
            key={label.name}
            className="wrap-anywhere"
            style={{ background: `#${label.color}`, color: textColor(label.color) }}
          >
            {label.name}
          </Badge>
        ) : (
          <Badge key={label.name} variant="outline" className="wrap-anywhere">
            {label.name}
          </Badge>
        ),
      )}
    </div>
  );
}

// before is the Brief that the new Brief replaces. With it, the argument shows the changes.
export function FullArgument({
  name,
  value,
  labels,
  before,
}: {
  name: string;
  value: string;
  labels: ActionLabel[];
  before?: string;
}) {
  const [hideRemoved, setHideRemoved] = useState(false);
  if (name === "labels") {
    return labels.length > 0 ? <LabelChips labels={labels} /> : null;
  }
  if (before !== undefined) {
    return (
      <div className="grid min-w-0 gap-0.5">
        <span className="text-xs text-muted-foreground">{name}</span>
        <BriefView before={before} after={value} hideRemoved={hideRemoved} />
        <RemovedToggle hidden={hideRemoved} toggle={() => setHideRemoved(!hideRemoved)} />
      </div>
    );
  }
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
  labels,
  before,
  open,
  toggle,
}: {
  name: string;
  value: string;
  labels: ActionLabel[];
  before?: string;
  open: boolean;
  toggle: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [long, setLong] = useState(false);
  const [hideRemoved, setHideRemoved] = useState(false);
  useLayoutEffect(() => {
    const content = box.current?.firstElementChild;
    if (!content) {
      return;
    }
    const observer = new ResizeObserver(() => setLong(content.clientHeight > collapsedHeight));
    observer.observe(content);
    return () => observer.disconnect();
  }, []);

  if (name === "labels" || (before === undefined && isShort(value))) {
    return <FullArgument name={name} value={value} labels={labels} />;
  }
  return (
    <div className="grid min-w-0 gap-0.5">
      <span className="text-xs text-muted-foreground">{name}</span>
      <div ref={box} className={cn("min-w-0", !open && "max-h-36 overflow-hidden")}>
        <div>
          {before === undefined ? (
            <Markdown text={value} />
          ) : (
            <BriefView before={before} after={value} hideRemoved={hideRemoved} />
          )}
        </div>
      </div>
      {(long || before !== undefined) && (
        <div className="flex flex-wrap gap-2">
          {long && (
            <Button variant="ghost" size="sm" onClick={toggle}>
              {open ? "Show less" : "Show all"}
            </Button>
          )}
          {before !== undefined && (
            <RemovedToggle hidden={hideRemoved} toggle={() => setHideRemoved(!hideRemoved)} />
          )}
        </div>
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
      {action.arguments
        .filter((argument) => !(action.tool === "message_lead" && argument.name === "workstream"))
        .map((argument) => (
          <Argument
            key={argument.name}
            {...argument}
            before={
              action.tool === "edit_brief" && argument.name === "brief" && action.oldBrief !== ""
                ? action.oldBrief
                : undefined
            }
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
