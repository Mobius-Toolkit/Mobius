import { MicIcon, XIcon } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import type { DraftVoice } from "@/lib/draft";
import { cn } from "@/lib/utils";

const card =
  "flex w-72 max-w-full min-w-0 items-start gap-2 rounded-lg border bg-card px-2 py-1.5 text-sm";

function Elapsed({ since }: { since: number }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const seconds = Math.max(0, Math.floor((now - since) / 1000));
  return (
    <span>
      {Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, "0")}
    </span>
  );
}

// The two lines show the end of the live text.
function LiveText({ text }: { text: string }) {
  const box = useRef<HTMLParagraphElement>(null);
  useLayoutEffect(() => {
    if (box.current) {
      box.current.scrollTop = box.current.scrollHeight;
    }
  });
  return (
    <p
      ref={box}
      className="max-h-10 min-h-10 overflow-hidden text-xs/5 wrap-anywhere text-muted-foreground"
    >
      {text}
    </p>
  );
}

const keep = (event: { preventDefault: () => void }) => event.preventDefault();

export function VoiceDraftCard({
  recording,
  voice,
  resend,
  remove,
}: {
  recording: { startedAt: number; live: string } | undefined;
  voice: DraftVoice | null;
  resend: () => void;
  remove: () => void;
}) {
  return (
    <div role="group" aria-label="Voice recording" className={card}>
      <MicIcon
        className={cn(
          "mt-0.5 size-4 shrink-0 text-muted-foreground",
          recording && "text-destructive",
        )}
      />
      {recording ? (
        <div className="grid min-w-0 flex-1 gap-0.5">
          <span className="font-medium">
            Recording <Elapsed since={recording.startedAt} />
          </span>
          <LiveText text={recording.live} />
        </div>
      ) : voice?.state === "failed" ? (
        <div className="grid min-w-0 flex-1 gap-1.5">
          <p role="alert" className="text-destructive wrap-anywhere">
            {voice.error}
          </p>
          <div className="flex gap-2">
            <Button type="button" variant="outline" size="xs" onMouseDown={keep} onClick={resend}>
              Send again
            </Button>
            <Button type="button" variant="outline" size="xs" onMouseDown={keep} onClick={remove}>
              Discard
            </Button>
          </div>
        </div>
      ) : (
        <>
          <span className="min-w-0 flex-1 font-medium">
            {voice?.state === "converting" ? "Converting" : "Voice"}
          </span>
          {voice?.state === "ready" && (
            <Button
              type="button"
              variant="secondary"
              size="icon-sm"
              aria-label="Remove voice"
              title="Remove voice"
              className="-my-0.5 -mr-1 rounded-full max-md:size-8"
              onMouseDown={keep}
              onClick={remove}
            >
              <XIcon />
            </Button>
          )}
        </>
      )}
    </div>
  );
}
