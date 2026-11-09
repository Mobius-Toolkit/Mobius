import type { Html, HtmlBuilder } from "foldkit/html";
import { spinnerIcon } from "./icons";

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
