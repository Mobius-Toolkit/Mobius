import { Link } from "@tanstack/react-router";
import { useId, useMemo, useState } from "react";
import {
  InboxItemKind,
  type ActionLabel,
  type ChatMessage,
  type InboxItem,
  type PendingAction,
} from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { actionState, roleName } from "@/lib/actions";
import { briefMarkdown } from "@/lib/briefDiff";
import { diffLines } from "@/lib/diff";
import { clock } from "@/lib/time";
import { chatParams } from "@/lib/workstreams";
import { AnswerPanel } from "./AnswerPanel";
import { diffMarks } from "./diffMarks";
import { link } from "./Inbox";
import { Markdown } from "./Markdown";

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

function RemovedSwitch({
  checked,
  onCheckedChange,
}: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  const id = useId();
  return (
    <div className="flex items-center gap-2">
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} />
      <Label htmlFor={id}>Show removed text</Label>
    </div>
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
  const [showRemoved, setShowRemoved] = useState(false);
  if (name === "labels") {
    return labels.length > 0 ? <LabelChips labels={labels} /> : null;
  }
  if (before !== undefined) {
    return (
      <div className="grid min-w-0 gap-0.5">
        <span className="text-xs text-muted-foreground">{name}</span>
        <BriefView before={before} after={value} hideRemoved={!showRemoved} />
        <RemovedSwitch checked={showRemoved} onCheckedChange={setShowRemoved} />
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

export function Outcome({ label, text }: { label: string; text: string }) {
  return (
    <div className="grid min-w-0 gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="wrap-anywhere whitespace-pre-line">{text}</span>
    </div>
  );
}

// The read-only dialog of a decided action has no Inbox item, so it gets an item that holds only the action.
const detailsItem = (message: ChatMessage, action: PendingAction): InboxItem => ({
  action,
  answer: "",
  dismissedAt: null,
  id: 0,
  issue: 0,
  kind: InboxItemKind.approval,
  link: "",
  mergeRefusal: "",
  merging: false,
  organization: "",
  pullRequest: 0,
  pullRequestUrl: "",
  questions: [],
  reason: "",
  reportUrl: "",
  repository: "",
  text: "",
  time: action.time,
  title: message.text,
  todo: null,
  withdrawn: false,
  workstream: 0,
  workstreamRepository: "",
});

export function ActionSender({
  action,
  onNavigate,
}: {
  action: PendingAction;
  onNavigate?: () => void;
}) {
  return (
    <span className="text-muted-foreground wrap-anywhere">
      {roleName[action.role]}
      {action.sender && (
        <>
          {" of "}
          <Link
            to="/workstreams/$owner/$name/$number"
            params={chatParams(action.sender)}
            onClick={onNavigate}
            className={link}
          >
            {`#${action.sender.number}${action.sender.title && ` ${action.sender.title}`}`}
          </Link>
        </>
      )}{" "}
      · {clock(action.time)}
    </span>
  );
}

// The record of the action in the chat history. item is the Inbox item of a pending action. The Owner decides in the
// dialog that the button opens.
export function ActionCard({
  message,
  action,
  item,
  onDecided,
}: {
  message: ChatMessage;
  action: PendingAction;
  item?: InboxItem;
  onDecided: (action: PendingAction) => void;
}) {
  const [panel, setPanel] = useState<"answer" | "details" | null>(null);
  // A decision hides the Inbox item while it runs, and the row of the tray shows the dialog again after a failure.
  if (panel === "answer" && !item) {
    setPanel(null);
  }
  const pending = action.state === "pending";
  const failed = pending && action.error !== "";
  const outcome = failed ? action.error : action.state === "done" ? action.result : "";

  return (
    <div
      data-message={message.id}
      data-action={action.id}
      className="grid max-w-full grid-cols-[minmax(0,1fr)] gap-1 justify-self-stretch rounded-xl border bg-card px-3 py-2"
    >
      <div className="text-xs">
        <ActionSender action={action} />
      </div>
      <p className="line-clamp-2 wrap-anywhere">{message.text}</p>
      <div className="flex items-center gap-2">
        <div className="flex min-w-0 grow flex-wrap items-center gap-x-2 gap-y-1">
          <Badge variant={failed ? "destructive" : "secondary"}>{actionState(action)}</Badge>
          {outcome && (
            <span className="line-clamp-2 min-w-0 text-sm wrap-anywhere whitespace-pre-line">
              {outcome}
            </span>
          )}
        </div>
        {pending ? (
          item && (
            <Button size="sm" onClick={() => setPanel("answer")}>
              Answer
            </Button>
          )
        ) : (
          <Button variant="ghost" size="sm" onClick={() => setPanel("details")}>
            Details
          </Button>
        )}
      </div>
      {panel === "answer" && item && (
        <AnswerPanel item={item} input onClose={() => setPanel(null)} onDecided={onDecided} />
      )}
      {panel === "details" && (
        <AnswerPanel
          item={detailsItem(message, action)}
          input={false}
          onClose={() => setPanel(null)}
        />
      )}
    </div>
  );
}
