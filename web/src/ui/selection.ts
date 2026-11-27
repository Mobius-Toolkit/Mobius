import { Effect, Queue, Schema, Stream } from "effect";
import { Mount } from "foldkit";
import * as Command from "foldkit/command";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineMessageUnion } from "foldkit/message";
import type * as Update from "foldkit/update";
import type { ChatSelection as ChatSelectionType } from "@/api/api.gen";
import { ChatSelection } from "@/chat/schemas";
import { addDraftSelection } from "@/lib/draft";
import { store } from "@/lib/effects";
import { newMessageId } from "@/lib/outbox";
import { clock } from "@/lib/time";
import { buttonClass } from "./button";
import { messageSquareQuoteIcon } from "./icons";

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
  const inLast = last.ownerDocument.createRange();
  inLast.selectNodeContents(last);
  inLast.setEnd(range.endContainer, range.endOffset);
  // A selection that ends before the first text of a block does not contain that block.
  const endsBeforeLast = inLast.toString().trim() === "";
  const to = Math.max(from, blocks.indexOf(last) - (endsBeforeLast ? 1 : 0));
  return blocks
    .slice(from, to + 1)
    .map(blockText)
    .join("\n\n");
}

// The text of a section heading: a heading element, or a paragraph that starts with a line of only bold text.
function headingText(block: Element) {
  if (/^H[1-6]$/.test(block.tagName)) {
    return blockText(block);
  }
  const [bold, next] = block.childNodes;
  const endsLine = !next || next.nodeName === "BR" || (next.textContent ?? "").startsWith("\n");
  return block.tagName === "P" && bold instanceof Element && bold.tagName === "STRONG" && endsLine
    ? blockText(bold)
    : null;
}

// The text of the nearest section heading above the start of the selection, or an empty text above the first heading.
export function selectionPart(root: Element, range: Range) {
  let part = "";
  for (const block of root.querySelectorAll("h1, h2, h3, h4, h5, h6, p")) {
    const heading = headingText(block);
    if (heading === null) {
      continue;
    }
    const above =
      block === range.startContainer ||
      block.compareDocumentPosition(range.startContainer) & Node.DOCUMENT_POSITION_FOLLOWING;
    if (!above) {
      break;
    }
    part = heading;
  }
  return part;
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
    part: Schema.String,
    rect: Rect,
  },
  Cleared: { owner: Schema.String },
  AddClicked: {},
  CompletedClearSelection: {},
  CompletedAddSelection: {},
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  AddRequested: {
    owner: Schema.String,
    text: Schema.String,
    context: Schema.String,
    part: Schema.String,
  },
});

export type OutMessage = typeof OutMessage.Type;

export const Model = Schema.NullOr(
  Schema.Struct({
    owner: Schema.String,
    text: Schema.String,
    context: Schema.String,
    part: Schema.String,
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
  return {
    text,
    context: selectionContext(element, range),
    part: selectionPart(element, range),
    rect: { left, right, top, bottom },
  };
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

export const AddSelection = Command.define("AddSelection", {
  args: { key: Schema.String, selection: ChatSelection },
  messages: [Message.CompletedAddSelection],
  execute: ({ key, selection }) =>
    // A draft that the store refused leaves the draft as it was.
    store(() => addDraftSelection(key, { ...selection, id: newMessageId() })).pipe(
      Effect.ignore,
      Effect.as(Message.CompletedAddSelection()),
    ),
});

type UpdateReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    Selected: ({ owner, text, context, part, rect }) => ({
      model: { owner, text, context, part, rect },
    }),
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
              part: model.part,
            }),
          }
        : { model },
    CompletedClearSelection: () => ({ model }),
    CompletedAddSelection: () => ({ model }),
  });
}

const popoverGap = 8;

// The popover is below the last line of the selection, because iOS and Android show their menu above it. The CSS
// functions keep it inside the viewport. It is above a dialog, which is z-50. label tells the field that gets the quote.
export function renderSelectionPopover<Msg>(
  h: HtmlBuilder<Msg>,
  model: Model,
  toParent: (message: Message) => Msg,
  label: string,
): Html {
  if (!model) {
    return h.empty;
  }
  const { rect } = model;
  return h.div(
    [
      h.Key("selection-popover"),
      h.DataAttribute("selection-popover", ""),
      h.Class("fixed z-60 rounded-full border bg-popover p-0.5 shadow-md"),
      h.Style({
        top: `clamp(0.5rem, ${rect.bottom + popoverGap}px, calc(100dvh - 3.25rem))`,
        left: `clamp(0.5rem, calc(${(rect.left + rect.right) / 2}px - 1.25rem), calc(100vw - 3.25rem))`,
      }),
    ],
    [
      h.button(
        [
          h.Type("button"),
          h.AriaLabel(label),
          h.Attribute("title", label),
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

export function sourceLine(selection: ChatSelectionType, time: string | undefined) {
  if (selection.source === "message") {
    return `${selection.messageAuthor} message${time ? ` · ${clock(time)}` : ""}`;
  }
  const part = selection.part ? ` · ${selection.part}` : "";
  if (selection.source === "inbox") {
    return `Inbox item${selection.reference ? ` ${selection.reference}` : ""}${part}`;
  }
  return selection.source === "task"
    ? `${selection.reference}${part}`
    : `Brief of ${selection.reference}${part}`;
}

// time is the time of the quoted message, when the message is in the loaded page.
export function selectionCardFields<Msg>(
  h: HtmlBuilder<Msg>,
  selection: ChatSelectionType,
  time: string | undefined,
) {
  return {
    icon: messageSquareQuoteIcon(h),
    header: sourceLine(selection, time),
    body: h.span(
      [h.Class("line-clamp-3 border-l-2 pl-2 text-xs whitespace-pre-line wrap-anywhere")],
      [selection.text],
    ),
    removeLabel: "Remove the selection",
  };
}
