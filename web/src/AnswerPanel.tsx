import { use, useEffect, useRef, useState } from "react";
import {
  answerInboxItem,
  approveAction,
  rejectAction,
  type InboxItem,
  type PendingAction,
  type Question,
} from "@/api/api.gen";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { roleName, type Decision } from "@/lib/actions";
import { answerDraftKey, saveDraftText } from "@/lib/draft";
import { LoginContext } from "@/lib/login";
import { useShell } from "@/lib/shell";
import { clock, dayClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { FullArgument } from "./ActionCard";
import { link } from "./Inbox";
import { Markdown } from "./Markdown";
import { MessageInput } from "./MessageInput";

// The keyboard of a phone covers the bottom of the layout viewport and does not resize it. --keyboard is the height
// of the covered part.
function useKeyboardHeight() {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) {
      return;
    }
    const update = () =>
      document.documentElement.style.setProperty(
        "--keyboard",
        `${Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop)}px`,
      );
    update();
    viewport.addEventListener("resize", update);
    viewport.addEventListener("scroll", update);
    return () => {
      viewport.removeEventListener("resize", update);
      viewport.removeEventListener("scroll", update);
      document.documentElement.style.removeProperty("--keyboard");
    };
  }, []);
}

// The options of a question as buttons. A click selects or clears an option. In a single choice, a click on an option
// clears the other option.
function Options({
  question,
  selected,
  toggle,
}: {
  question: Question;
  selected: number[];
  toggle: (option: number) => void;
}) {
  return (
    <div role="group" aria-label={question.text} className="grid gap-2">
      <p className="wrap-anywhere">
        <span className="font-medium">{question.text}</span>{" "}
        <span className="text-sm text-muted-foreground">
          {question.multiple ? "Select one or more." : "Select one."}
        </span>
      </p>
      {question.options.map((option, index) => {
        const on = selected.includes(index);
        return (
          <button
            key={index}
            type="button"
            aria-pressed={on}
            onClick={() => toggle(index)}
            className={cn(
              "flex w-full items-start gap-3 rounded-lg border p-3 text-left",
              on && "border-primary",
            )}
          >
            <span
              aria-hidden
              className={cn(
                "mt-0.5 size-4 shrink-0 border",
                question.multiple ? "rounded-sm" : "rounded-full",
                on ? "border-primary bg-primary" : "border-input",
              )}
            />
            <span className="grid min-w-0 gap-0.5">
              <span className="wrap-anywhere">{option.title}</span>
              {option.details && (
                <span className="text-sm text-muted-foreground wrap-anywhere">
                  {option.details}
                </span>
              )}
            </span>
          </button>
        );
      })}
    </div>
  );
}

export function AnswerPanel({
  item,
  input,
  workstream,
  onClose,
  onDecided,
}: {
  item: InboxItem;
  input: boolean;
  workstream: string;
  onClose: () => void;
  onDecided?: (action: PendingAction) => void;
}) {
  const showLogin = use(LoginContext);
  const { leaveInbox, updateAction, hideInbox, failInbox, inboxFailure, clearInboxFailure } =
    useShell();
  const [error, setError] = useState(inboxFailure?.id === item.id ? inboxFailure.error : "");
  const content = useRef<HTMLDivElement>(null);
  const [selected, setSelected] = useState<Record<number, number[]>>({});
  const selection = Object.values(selected).some((options) => options.length > 0);
  useKeyboardHeight();

  const toggle = (question: number, option: number) =>
    setSelected((current) => {
      const chosen = current[question] ?? [];
      if (chosen.includes(option)) {
        return { ...current, [question]: chosen.filter((other) => other !== option) };
      }
      return {
        ...current,
        [question]: item.questions[question].multiple ? [...chosen, option] : [option],
      };
    });

  const selectionText = (note: string) =>
    [
      ...item.questions.flatMap((question, index) => {
        const lines = question.options
          .filter((_, option) => selected[index]?.includes(option))
          .map((option) => `- ${option.title}`);
        return lines.length > 0 ? [[question.text, ...lines].join("\n")] : [];
      }),
      ...(note.trim() ? [note] : []),
    ].join("\n\n");

  useEffect(() => {
    if (inboxFailure?.id === item.id) {
      clearInboxFailure();
    }
  }, [inboxFailure, item.id, clearInboxFailure]);

  const answer = async (text: string) => {
    hideInbox(item);
    let failure = "";
    try {
      const res = await answerInboxItem(item.id, { text });
      if (res.status === 200) {
        onClose();
        return true;
      }
      if (res.status === 401) {
        showLogin();
      } else {
        failure = res.data.error;
      }
    } catch (err) {
      failure = String(err);
    }
    void saveDraftText(answerDraftKey(item.id), text);
    failInbox(item.id, failure);
    return false;
  };

  const decide = async (id: number, decision: Decision, message: string) => {
    hideInbox(item);
    let failure = "";
    try {
      const res = await (decision === "approve" ? approveAction : rejectAction)(id, { message });
      if (res.status === 200) {
        const decided = res.data.data;
        updateAction(decided);
        onDecided?.(decided);
        if (decided.state !== "pending") {
          leaveInbox(item.id);
          onClose();
          return true;
        }
        failure = decided.error;
      } else if (res.status === 401) {
        showLogin();
      } else {
        failure = res.data.error;
      }
    } catch (err) {
      failure = String(err);
    }
    failInbox(item.id, failure);
    return false;
  };

  const action = item.action;
  if (action && action.state !== "pending") {
    return null;
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        ref={content}
        aria-describedby={undefined}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          content.current?.focus();
        }}
        className="grid max-h-[calc(100dvh-var(--keyboard,0px)-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 p-0 max-sm:top-auto max-sm:bottom-(--keyboard,0px) max-sm:left-0 max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-b-none sm:max-h-[85dvh] sm:max-w-lg"
      >
        <DialogHeader className="gap-1 p-4 pr-10">
          <DialogTitle>{item.title || item.reason}</DialogTitle>
          {item.title && !action && <p className="text-sm text-muted-foreground">{item.reason}</p>}
        </DialogHeader>
        {action ? (
          <div className="grid min-w-0 content-start gap-2 overflow-y-auto px-4 pb-4">
            <span className="text-muted-foreground">
              {roleName[action.role]} · {clock(action.time)}
            </span>
            {action.arguments.map((argument) => (
              <FullArgument key={argument.name} {...argument} />
            ))}
          </div>
        ) : (
          <div className="grid content-start gap-2 overflow-y-auto px-4 pb-4">
            <Markdown text={item.text} />
            {item.questions.map((question, index) => (
              <Options
                key={index}
                question={question}
                selected={selected[index] ?? []}
                toggle={(option) => toggle(index, option)}
              />
            ))}
            <span className="text-muted-foreground">
              {workstream} · {dayClock(item.time)}
            </span>
            {item.reportUrl && (
              <a href={item.reportUrl} target="_blank" rel="noreferrer" className={link}>
                Approval report
              </a>
            )}
          </div>
        )}
        {input && action && (
          <MessageInput
            draftKey={answerDraftKey(item.id)}
            label={`Note for the ${roleName[action.role]}`}
            placeholder={`Add a note for the ${roleName[action.role]} (optional)`}
            images={false}
            error={error}
            onError={setError}
            stopping={false}
            onDecide={(decision, message) => decide(action.id, decision, message)}
          />
        )}
        {input && !action && (
          <MessageInput
            draftKey={answerDraftKey(item.id)}
            label="Your answer"
            placeholder={item.questions.length > 0 ? "Add a note (optional)" : "Write your answer"}
            selection={selection}
            images={false}
            error={error}
            onError={setError}
            stopping={false}
            onSend={(text) => answer(selectionText(text))}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
