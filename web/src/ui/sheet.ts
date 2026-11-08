import { cn } from "cn";
import { Effect, type Schema } from "effect";
import { Mount } from "foldkit";
import type { Html, HtmlBuilder } from "foldkit/html";
import { buttonClass } from "./button";
import { xIcon } from "./icons";

const focusable =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])';

// The Mount of a sheet or a dialog. done is the Message that the Mount gives when the layer has opened or has no
// content. The layer stays in its place in the DOM, because the panel can sit inside a dialog of the host, and that
// dialog blocks the pointer and the focus outside its content. Tab stays in the layer, and the focus goes back to the
// opener when the layer closes.
export function defineInPlaceMount<Done extends Schema.Top & (() => Schema.Schema.Type<Done>)>(
  done: Done,
) {
  return Mount.define("InPlaceOpened", {
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
            document.addEventListener("keydown", trap);
            return { opener, trap };
          }),
          ({ opener, trap }) =>
            Effect.sync(() => {
              document.removeEventListener("keydown", trap);
              if (opener instanceof HTMLElement) {
                opener.focus();
              }
            }),
        );
        return done();
      }),
  });
}

// A sheet that slides up from the bottom. mount is the OnMount attribute of the Mount of defineDialogMount, and close is
// the Message of the overlay and of the close button.
export function renderSheet<M>(
  h: HtmlBuilder<M>,
  options: {
    mount: ReturnType<HtmlBuilder<M>["OnMount"]>;
    titleId: string;
    descriptionId: string;
    title: string;
    description: string;
    close: M;
    className: string;
    children: ReadonlyArray<Html>;
  },
): Html {
  return h.div(
    [h.DataAttribute("slot", "sheet-portal"), h.Class("contents"), options.mount],
    [
      h.div([
        h.DataAttribute("slot", "sheet-overlay"),
        h.Class(
          "fixed inset-0 z-50 bg-black/10 duration-100 supports-backdrop-filter:backdrop-blur-xs",
        ),
        h.OnClick(options.close),
      ]),
      h.div(
        [
          h.Role("dialog"),
          h.Tabindex(-1),
          h.AriaLabelledBy(options.titleId),
          h.AriaDescribedBy(options.descriptionId),
          h.DataAttribute("slot", "sheet-content"),
          h.DataAttribute("side", "bottom"),
          h.Class(
            cn(
              "fixed z-50 flex flex-col gap-4 bg-popover bg-clip-padding text-sm text-popover-foreground shadow-lg transition duration-200 ease-in-out outline-none data-[side=bottom]:inset-x-0 data-[side=bottom]:bottom-0 data-[side=bottom]:h-auto data-[side=bottom]:border-t",
              options.className,
            ),
          ),
        ],
        [
          h.h2(
            [
              h.Id(options.titleId),
              h.DataAttribute("slot", "sheet-title"),
              h.Class("font-heading text-base font-medium text-foreground"),
            ],
            [options.title],
          ),
          h.p(
            [
              h.Id(options.descriptionId),
              h.DataAttribute("slot", "sheet-description"),
              h.Class("text-sm text-muted-foreground"),
            ],
            [options.description],
          ),
          ...options.children,
          h.button(
            [
              h.Type("button"),
              h.DataAttribute("slot", "sheet-close"),
              h.Class(
                buttonClass({
                  variant: "ghost",
                  size: "icon-sm",
                  className: "absolute top-3 right-3",
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
