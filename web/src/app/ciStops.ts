import type { Html, HtmlBuilder } from "foldkit/html";
import type { CIStop } from "@/api/api.gen";
import type { Message } from "./message";

export function renderCIStops(h: HtmlBuilder<Message>, stops: readonly CIStop[]): Html[] {
  return stops.map((stop) =>
    h.keyed("div")(
      `${stop.repository} ${stop.issue}`,
      [
        h.Class(
          "grid gap-1 rounded-lg border border-destructive/40 bg-red-100 px-3 py-2 text-sm wrap-anywhere text-red-900 dark:bg-red-950 dark:text-red-200",
        ),
      ],
      [
        h.span([h.Class("font-medium")], ["Dispatch stopped: CI fails on ", stop.branch]),
        h.span([], [stop.repository, ': the workflow "', stop.workflow, '" fails.']),
        h.span(
          [],
          [
            "Task issue: ",
            h.a(
              [
                h.Href(stop.issueUrl),
                h.Attribute("target", "_blank"),
                h.Attribute("rel", "noreferrer"),
                h.Class("underline"),
              ],
              ["#", String(stop.issue)],
            ),
          ],
        ),
      ],
    ),
  );
}
