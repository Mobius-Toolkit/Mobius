import type { Html, HtmlBuilder } from "foldkit/html";
import { spinnerIcon, wifiOffIcon } from "./icons";

export type Connection = "connected" | "connecting" | "offline";

export function renderConnectionBadge<M>(h: HtmlBuilder<M>, connection: Connection): Html {
  return h.div(
    [h.Role("status"), h.Class("pointer-events-none flex justify-center pt-2")],
    [
      connection === "connecting"
        ? h.span(
            [
              h.Class(
                "flex items-center gap-1.5 rounded-full border border-amber-500/40 bg-amber-100 px-3 py-1 text-xs font-medium text-amber-900 shadow-md dark:bg-amber-950 dark:text-amber-200",
              ),
            ],
            [spinnerIcon(h, "size-3.5 animate-spin"), "Connecting…"],
          )
        : connection === "offline"
          ? h.span(
              [
                h.Class(
                  "flex items-center gap-1.5 rounded-full border border-destructive/40 bg-red-100 px-3 py-1 text-xs font-medium text-red-900 shadow-md dark:bg-red-950 dark:text-red-200",
                ),
              ],
              [wifiOffIcon(h, "size-3.5"), "Offline"],
            )
          : h.empty,
    ],
  );
}

// A badge of the live connection on top of a list that shows data from a stream.
export function renderConnecting<M>(h: HtmlBuilder<M>): Html {
  return h.div(
    [h.Role("status"), h.Class("pointer-events-none flex justify-center pt-2")],
    [
      h.span(
        [
          h.Class(
            "flex items-center gap-1.5 rounded-full border border-amber-500/40 bg-amber-100 px-3 py-1 text-xs font-medium text-amber-900 shadow-md dark:bg-amber-950 dark:text-amber-200",
          ),
        ],
        [spinnerIcon(h, "size-3.5 animate-spin"), "Connecting…"],
      ),
    ],
  );
}
