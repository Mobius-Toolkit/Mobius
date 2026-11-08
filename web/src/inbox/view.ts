import type { Html } from "foldkit/html";
import { Message } from "@/chat/message";
import type { Model } from "@/chat/model";
import { type H, renderAnswerDialog, renderItemRow, trayRows } from "@/chat/view";
import { badgeClass } from "@/ui/badge";
import { beginMarkdownRender } from "@/ui/markdown";

export function view(model: Model, h: H): Html {
  beginMarkdownRender();
  const rows = trayRows(model);

  return h.div(
    [h.Class("grid grid-cols-1 gap-2")],
    [
      model.trayError
        ? h.div(
            [h.Class("px-4 md:px-0")],
            [
              h.span(
                [
                  h.DataAttribute("slot", "badge"),
                  h.Class(badgeClass("destructive", "h-auto whitespace-normal")),
                ],
                [model.trayError],
              ),
            ],
          )
        : h.empty,
      rows.length === 0
        ? h.p([h.Class("px-4 text-muted-foreground md:px-0")], ["Nothing waits for you."])
        : h.empty,
      h.ul(
        [h.Class("divide-y md:-mx-2")],
        rows.map((item) => renderItemRow(h, item, model, Message.ChatWithItemClicked({ item }))),
      ),
      renderAnswerDialog(h, model),
    ],
  );
}
