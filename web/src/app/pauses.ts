import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { dayClock } from "@/lib/time";
import { renderButton } from "@/ui/button";
import { renderErrorBadge } from "@/ui/page";
import { Message } from "./message";
import type { Model } from "./model";

export function renderPauses(h: HtmlBuilder<Message>, model: Model): Html[] {
  return model.pauses
    .filter((pause) => pause.users.length > 0)
    .map((pause) => {
      const isBusy = Option.isSome(model.pauseBusy) && model.pauseBusy.value === pause.harness;
      const error = Option.getOrElse(model.pauseError, () => "");

      return h.keyed("div")(
        `${pause.harness} ${pause.pausedUntil}`,
        [h.Class("grid gap-2")],
        [
          h.div(
            [
              h.Class(
                "flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-500/40 bg-amber-100 px-3 py-2 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200",
              ),
            ],
            [
              h.span([], [pause.harness, " paused until ", dayClock(pause.pausedUntil)]),
              renderButton(h, {
                label: "Resume now",
                size: "sm",
                disabled: isBusy,
                pending: isBusy,
                onClick: Message.ResumePauseRequested({ harness: pause.harness }),
              }),
            ],
          ),
          error ? renderErrorBadge(h, error) : h.empty,
        ],
      );
    });
}
