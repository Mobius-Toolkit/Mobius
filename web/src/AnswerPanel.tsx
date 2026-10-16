import { use, useEffect, useState } from "react";
import { answerInboxItem, type InboxItem } from "@/api/api.gen";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { answerDraftKey } from "@/lib/draft";
import { LoginContext } from "@/lib/login";
import { dayClock } from "@/lib/time";
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

export function AnswerPanel({
  item,
  input,
  workstream,
  onClose,
}: {
  item: InboxItem;
  input: boolean;
  workstream: string;
  onClose: () => void;
}) {
  const showLogin = use(LoginContext);
  const [error, setError] = useState("");
  useKeyboardHeight();

  const answer = async (text: string) => {
    try {
      const res = await answerInboxItem(item.id, { text });
      if (res.status === 200) {
        onClose();
        return true;
      }
      if (res.status === 401) {
        showLogin();
      } else {
        setError(res.data.error);
      }
    } catch (err) {
      setError(String(err));
    }
    return false;
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        aria-describedby={undefined}
        className="grid max-h-[calc(100dvh-var(--keyboard,0px)-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 p-0 max-sm:top-auto max-sm:bottom-(--keyboard,0px) max-sm:left-0 max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-b-none sm:max-h-[85dvh] sm:max-w-lg"
      >
        <DialogHeader className="gap-1 p-4 pr-10">
          <DialogTitle>{item.title || item.reason}</DialogTitle>
          {item.title && <p className="text-sm text-muted-foreground">{item.reason}</p>}
        </DialogHeader>
        <div className="grid content-start gap-2 overflow-y-auto px-4 pb-4">
          <Markdown text={item.text} />
          <span className="text-muted-foreground">
            {workstream} · {dayClock(item.time)}
          </span>
          {item.reportUrl && (
            <a href={item.reportUrl} target="_blank" rel="noreferrer" className={link}>
              Approval report
            </a>
          )}
        </div>
        {input && (
          <MessageInput
            draftKey={answerDraftKey(item.id)}
            label="Your answer"
            placeholder="Write your answer"
            images={false}
            error={error}
            onError={setError}
            stopping={false}
            onSend={answer}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
