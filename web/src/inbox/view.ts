import type { Html } from "foldkit/html";
import { Message, toGotSelectionMessage } from "@/chat/message";
import { type Model, quoteLabel } from "@/chat/model";
import {
  type H,
  renderAnswerDialog,
  renderItemRow,
  renderTodoListDialog,
  trayRows,
} from "@/chat/view";
import { badgeClass } from "@/ui/badge";
import { beginMarkdownRender } from "@/ui/markdown";
import { renderSelectionPopover } from "@/ui/selection";

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
        rows.map((row) =>
          renderItemRow(h, row, model, Message.ChatWithItemClicked({ item: row.item })),
        ),
      ),
      renderTodoListDialog(h, model),
      renderAnswerDialog(h, model),
      renderSelectionPopover(h, model.selection, toGotSelectionMessage, quoteLabel(model)),
    ],
  );
}
