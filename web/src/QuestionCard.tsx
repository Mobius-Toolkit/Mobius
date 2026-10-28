import { useState } from "react";
import type { ChatMessage, InboxItem, Workstream } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { clock } from "@/lib/time";
import { AnswerPanel } from "./AnswerPanel";

function state(question: InboxItem) {
  if (!question.dismissedAt) {
    return "Waits for your answer";
  }
  if (question.answer) {
    return "Answered";
  }
  return question.withdrawn ? "Withdrawn" : "No answer";
}

// The record of a question in the chat history. item is the Inbox item of the open question in the tray. The Owner
// answers in the dialog that the button opens.
export function QuestionCard({
  message,
  question,
  item,
  workstreams,
}: {
  message: ChatMessage;
  question: InboxItem;
  item?: InboxItem;
  workstreams?: Workstream[];
}) {
  const [panel, setPanel] = useState<"answer" | "details" | null>(null);
  // An answer hides the Inbox item while it runs, and the row of the tray shows the dialog again after a failure.
  if (panel === "answer" && !item) {
    setPanel(null);
  }
  const answer = question.dismissedAt ? question.answer : "";
  const workstream =
    question.repository === ""
      ? "Coordinator"
      : (workstreams?.find(
          (other) =>
            other.repository === question.workstreamRepository &&
            other.number === question.workstream,
        )?.title ?? `#${question.workstream}`);

  return (
    <div
      data-message={message.id}
      data-question={question.id}
      className="grid max-w-full grid-cols-[minmax(0,1fr)] gap-1 justify-self-stretch rounded-xl border bg-card px-3 py-2"
    >
      <div className="text-xs text-muted-foreground">
        {question.repository === "" ? "Coordinator" : "Lead"} · {clock(message.time)}
      </div>
      <p className="truncate">{message.text}</p>
      <div className="flex items-center gap-2">
        <div className="flex min-w-0 grow flex-wrap items-center gap-x-2 gap-y-1">
          <Badge variant="secondary">{state(question)}</Badge>
          {answer && (
            <span className="line-clamp-2 min-w-0 text-sm wrap-anywhere whitespace-pre-line">
              {answer}
            </span>
          )}
        </div>
        {!question.dismissedAt && item && (
          <Button size="sm" onClick={() => setPanel("answer")}>
            Answer
          </Button>
        )}
        {answer && (
          <Button variant="ghost" size="sm" onClick={() => setPanel("details")}>
            Details
          </Button>
        )}
      </div>
      {panel === "answer" && item && (
        <AnswerPanel item={item} input workstream={workstream} onClose={() => setPanel(null)} />
      )}
      {panel === "details" && (
        <AnswerPanel
          item={question}
          input={false}
          workstream={workstream}
          onClose={() => setPanel(null)}
        />
      )}
    </div>
  );
}
