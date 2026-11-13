import { Effect, Queue, Schema, Stream } from "effect";
import { Mount } from "foldkit";
import * as Command from "foldkit/command";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineMessageUnion } from "foldkit/message";
import type * as Update from "foldkit/update";
import type { ChatSelection } from "@/api/api.gen";
import { clock } from "@/lib/time";
import { buttonClass } from "./button";
import { messageSquareQuoteIcon, xIcon } from "./icons";

const blockSelector = "p, li, blockquote, pre, td, h1, h2, h3, h4, h5, h6";

function blockOf(root: Element, node: Node) {
  const block = (node instanceof Element ? node : node.parentElement)?.closest(blockSelector);
  return block && root.contains(block) ? block : null;
}

const blockText = (block: Element) => (block.textContent ?? "").trim();

// The text of the block of the selection. A selection over more than one block gives each block from the block of the
// start to the block of the end, with an empty line between two blocks. A block that holds other blocks gives its text
// through them.
export function selectionContext(root: Element, range: Range) {
  const first = blockOf(root, range.startContainer);
  const last = blockOf(root, range.endContainer);
  if (!first || !last) {
    return range.toString().trim();
  }
  if (first === last) {
    return blockText(first);
  }
  const blocks = [...root.querySelectorAll(blockSelector)].filter(
    (block) => block === first || block === last || !block.querySelector(blockSelector),
  );
  const from = blocks.indexOf(first);
  // A selection that ends at the start of a block does not contain that block.
  const to = Math.max(from, blocks.indexOf(last) - (range.endOffset === 0 ? 1 : 0));
  return blocks
    .slice(from, to + 1)
    .map(blockText)
    .join("\n\n");
}

const Rect = Schema.Struct({
  left: Schema.Number,
  right: Schema.Number,
  top: Schema.Number,
  bottom: Schema.Number,
});

export const Message = defineMessageUnion({
  Selected: {
    owner: Schema.String,
    text: Schema.String,
    context: Schema.String,
    rect: Rect,
  },
  Cleared: { owner: Schema.String },
  AddClicked: {},
  CompletedClearSelection: {},
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  AddRequested: { owner: Schema.String, text: Schema.String, context: Schema.String },
});

export type OutMessage = typeof OutMessage.Type;

export const Model = Schema.NullOr(
  Schema.Struct({
    owner: Schema.String,
    text: Schema.String,
    context: Schema.String,
    rect: Rect,
  }),
);

export type Model = typeof Model.Type;

function readSelection(element: Element) {
  const selection = document.getSelection();
  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) {
    return null;
  }
  const range = selection.getRangeAt(0);
  const text = range.toString().trim();
  if (!text || !element.contains(range.startContainer) || !element.contains(range.endContainer)) {
    return null;
  }
  const lines = [...range.getClientRects()].filter((rect) => rect.width > 0);
  const { left, right, top, bottom } = lines.at(-1) ?? range.getBoundingClientRect();
  return { text, context: selectionContext(element, range), rect: { left, right, top, bottom } };
}

type WatchMessage = ReturnType<typeof Message.Selected> | ReturnType<typeof Message.Cleared>;

// Gives a Message while a selection that is not empty starts and ends inside the element, and one Message when the
// selection leaves the element. owner tells the Messages of one element from the Messages of another.
export const Watch = Mount.defineStream("SelectionWatch", {
  args: { owner: Schema.String },
  messages: [Message.Selected, Message.Cleared],
  execute: ({ element, owner }) =>
    Stream.callback<WatchMessage>((queue) =>
      Effect.gen(function* () {
        let active = false;
        const report = () => {
          const found = readSelection(element);
          if (found) {
            active = true;
            Queue.offerUnsafe(queue, Message.Selected({ owner, ...found }));
          } else if (active) {
            active = false;
            Queue.offerUnsafe(queue, Message.Cleared({ owner }));
          }
        };
        document.addEventListener("selectionchange", report);
        // The popover is fixed, so it follows the selection when a list scrolls.
        window.addEventListener("scroll", report, true);
        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            document.removeEventListener("selectionchange", report);
            window.removeEventListener("scroll", report, true);
          }),
        );
      }),
    ),
});

export const ClearSelection = Command.define("ClearSelection", {
  messages: [Message.CompletedClearSelection],
  execute: Effect.sync(() => {
    document.getSelection()?.removeAllRanges();
    return Message.CompletedClearSelection();
  }),
});

type UpdateReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    Selected: ({ owner, text, context, rect }) => ({ model: { owner, text, context, rect } }),
    Cleared: ({ owner }) => ({ model: model?.owner === owner ? null : model }),
    AddClicked: () =>
      model
        ? {
            model: null,
            commands: [ClearSelection()],
            outMessage: OutMessage.AddRequested({
              owner: model.owner,
              text: model.text,
              context: model.context,
            }),
          }
        : { model },
    CompletedClearSelection: () => ({ model }),
  });
}

const popoverGap = 8;

// The popover is below the last line of the selection, because iOS and Android show their menu above it. The CSS
// functions keep it inside the viewport.
export function renderSelectionPopover<Msg>(
  h: HtmlBuilder<Msg>,
  model: Model,
  toParent: (message: Message) => Msg,
): Html {
  if (!model) {
    return h.empty;
  }
  const { rect } = model;
  return h.div(
    [
      h.Key("selection-popover"),
      h.DataAttribute("selection-popover", ""),
      h.Class("fixed z-50 rounded-full border bg-popover p-0.5 shadow-md"),
      h.Style({
        top: `min(${rect.bottom + popoverGap}px, calc(100dvh - 3.25rem))`,
        left: `clamp(0.5rem, calc(${(rect.left + rect.right) / 2}px - 1.25rem), calc(100vw - 3.25rem))`,
      }),
    ],
    [
      h.button(
        [
          h.Type("button"),
          h.AriaLabel("Add to chat"),
          h.Attribute("title", "Add to chat"),
          h.Class(
            buttonClass({
              variant: "ghost",
              size: "icon",
              className: "rounded-full max-md:size-11",
            }),
          ),
          h.OnClick(toParent(Message.AddClicked())),
        ],
        [messageSquareQuoteIcon(h)],
      ),
    ],
  );
}

function sourceLine(selection: ChatSelection, time: string | undefined) {
  if (selection.source === "message") {
    return `${selection.messageAuthor} message${time ? ` · ${clock(time)}` : ""}`;
  }
  const part = selection.part ? ` · ${selection.part}` : "";
  return selection.source === "task"
    ? `${selection.reference}${part}`
    : `Brief of ${selection.reference}${part}`;
}

// time is the time of the quoted message, when the message is in the loaded page. onRemove adds the remove button.
export function renderSelectionCard<Msg>(
  h: HtmlBuilder<Msg>,
  selection: ChatSelection,
  options: { key?: string; time?: string; onRemove?: Msg },
): Html {
  const { key, time, onRemove } = options;
  return h.div(
    [
      ...(key ? [h.Key(key)] : []),
      h.DataAttribute("selection-card", ""),
      h.Class(
        `relative grid min-w-0 max-w-full gap-0.5 rounded-lg border bg-card px-2 py-1.5 text-sm ${onRemove ? "w-64 pr-9" : ""}`,
      ),
    ],
    [
      h.span([h.Class("truncate text-xs text-muted-foreground")], [sourceLine(selection, time)]),
      h.span(
        [h.Class("line-clamp-3 border-l-2 pl-2 text-xs whitespace-pre-line wrap-anywhere")],
        [selection.text],
      ),
      onRemove
        ? h.button(
            [
              h.Type("button"),
              h.AriaLabel("Remove the selection"),
              h.Attribute("title", "Remove the selection"),
              h.DataAttribute("keep-focus", ""),
              h.Class(
                buttonClass({
                  variant: "secondary",
                  size: "icon-sm",
                  className: "absolute top-0.5 right-0.5 rounded-full max-md:size-8",
                }),
              ),
              h.OnClick(onRemove),
            ],
            [xIcon(h)],
          )
        : h.empty,
    ],
  );
}
