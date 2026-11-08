import { cn } from "cn";
import { Effect, type Schema } from "effect";
import { Mount } from "foldkit";
import type { Html, HtmlBuilder } from "foldkit/html";
import { trackKeyboardHeight } from "@/lib/keyboard";
import { buttonClass } from "./button";
import { xIcon } from "./icons";

const focusable =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])';

// The Mount of a dialog. done is the Message that the Mount gives when the dialog has opened or has no content. The
// dialog lives outside the section of the screen, so a focused field in the dialog does not hide the other parts of
// the screen. The element moves to the body before the focus goes into the dialog, because a move takes the focus
// away. Tab stays in the dialog, the page does not scroll, the keyboard of a phone moves the dialog up, and the focus
// goes back to the opener when the dialog closes.
export function defineDialogMount<Done extends Schema.Top & (() => Schema.Schema.Type<Done>)>(
  done: Done,
) {
  return Mount.define("DialogOpened", {
    messages: [done],
    execute: ({ element }) =>
      Effect.gen(function* () {
        const dialog = element.querySelector<HTMLElement>('[role="dialog"]');
        if (dialog === null) {
          return done();
        }
        yield* Effect.acquireRelease(
          Effect.sync(() => {
            const opener = document.activeElement;
            document.body.appendChild(element);
            const overflow = document.body.style.overflow;
            const trap = (event: KeyboardEvent) => {
              if (event.key !== "Tab") {
                return;
              }
              const items = [...dialog.querySelectorAll<HTMLElement>(focusable)];
              const first = items[0];
              const last = items[items.length - 1];
              if (!first || !dialog.contains(document.activeElement)) {
                event.preventDefault();
                (first ?? dialog).focus();
              } else if (event.shiftKey && document.activeElement === first) {
                event.preventDefault();
                last.focus();
              } else if (!event.shiftKey && document.activeElement === last) {
                event.preventDefault();
                first.focus();
              }
            };
            dialog.focus();
            document.body.style.overflow = "hidden";
            document.addEventListener("keydown", trap);
            return { opener, overflow, trap, stopKeyboard: trackKeyboardHeight() };
          }),
          ({ opener, overflow, trap, stopKeyboard }) =>
            Effect.sync(() => {
              element.remove();
              document.removeEventListener("keydown", trap);
              document.body.style.overflow = overflow;
              stopKeyboard();
              if (opener instanceof HTMLElement) {
                opener.focus();
              }
            }),
        );
        return done();
      }),
  });
}

// A dialog that slides up from the bottom on a phone. mount is the OnMount attribute of the Mount of
// defineDialogMount, and close is the Message of the overlay and of the close button.
export function renderDialog<M>(
  h: HtmlBuilder<M>,
  options: {
    mount: ReturnType<HtmlBuilder<M>["OnMount"]>;
    titleId: string;
    title: string;
    description: string;
    close: M;
    body: Html;
    footer: Html;
  },
): Html {
  return h.div(
    [h.DataAttribute("slot", "dialog-portal"), options.mount],
    [
      h.div([
        h.DataAttribute("slot", "dialog-overlay"),
        h.DataAttribute("state", "open"),
        h.Class(
          "fixed inset-0 isolate z-50 bg-black/10 duration-100 supports-backdrop-filter:backdrop-blur-xs",
        ),
        h.OnClick(options.close),
      ]),
      h.div(
        [
          h.Role("dialog"),
          h.Attribute("aria-modal", "true"),
          h.Tabindex(-1),
          h.AriaLabelledBy(options.titleId),
          h.DataAttribute("slot", "dialog-content"),
          h.DataAttribute("state", "open"),
          h.Class(
            cn(
              "fixed top-1/2 left-1/2 z-50 grid w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 rounded-xl bg-popover p-4 text-sm text-popover-foreground ring-1 ring-foreground/10 duration-100 outline-none sm:max-w-sm",
              "grid max-h-[calc(100dvh-var(--keyboard,0px)-2rem)] grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 p-0 max-sm:top-auto max-sm:bottom-(--keyboard,0px) max-sm:left-0 max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-b-none sm:max-h-[85dvh] sm:max-w-lg",
            ),
          ),
        ],
        [
          h.div(
            [h.DataAttribute("slot", "dialog-header"), h.Class("flex flex-col gap-1 p-4 pr-10")],
            [
              h.h2(
                [
                  h.Id(options.titleId),
                  h.DataAttribute("slot", "dialog-title"),
                  h.Class("font-heading text-base leading-none font-medium wrap-anywhere"),
                ],
                [options.title],
              ),
              options.description
                ? h.p([h.Class("text-sm text-muted-foreground")], [options.description])
                : h.empty,
            ],
          ),
          options.body,
          options.footer,
          h.button(
            [
              h.Type("button"),
              h.DataAttribute("slot", "dialog-close"),
              h.Class(
                buttonClass({
                  variant: "ghost",
                  size: "icon-sm",
                  className: "absolute top-2 right-2",
                }),
              ),
              h.OnClick(options.close),
            ],
            [xIcon(h), h.span([h.Class("sr-only")], ["Close"])],
          ),
        ],
      ),
    ],
  );
}
