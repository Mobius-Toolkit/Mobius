import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import { micIcon } from "./icons";

export function formatDuration(seconds: number) {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

export const voiceCardLabel = "Voice recording";

// The header of the card has no duration when the recording has none, as in a message that an older version stored.
export const voiceHeader = (label: string, seconds: number) =>
  seconds > 0 ? `${label} ${formatDuration(seconds)}` : label;

// The body has two lines in each state, so the card keeps its height.
export const voiceBodyClass = "h-10 overflow-hidden text-xs/5 wrap-anywhere text-muted-foreground";

export const voiceIcon = <Msg>(h: HtmlBuilder<Msg>, recording = false): Html =>
  micIcon(h, cn("size-4", recording && "text-destructive"));

// The fields of a card that shows the text of a recording with its duration.
export function voiceCardFields<Msg>(
  h: HtmlBuilder<Msg>,
  voice: { seconds: number; text: string },
) {
  return {
    label: voiceCardLabel,
    icon: voiceIcon(h),
    header: voiceHeader("Voice", voice.seconds),
    body: h.span([h.Class(cn(voiceBodyClass, "line-clamp-2"))], [voice.text]),
    removeLabel: "Remove voice",
  };
}
