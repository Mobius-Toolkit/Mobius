import { Effect, Queue, Schema, Stream } from "effect";
import { Mount } from "foldkit";

// The Mount of a link to a page of the host router. navigateTo is the Message that the Mount gives. A plain click goes
// to the host router. A click with a modifier key keeps the default of the browser.
export function defineRouterLink<
  NavigateTo extends Schema.Top & ((args: { path: string }) => Schema.Schema.Type<NavigateTo>),
>(navigateTo: NavigateTo) {
  return Mount.defineStream("RouterLink", {
    args: { path: Schema.String },
    messages: [navigateTo],
    execute: ({ element, path }) =>
      Stream.callback<Schema.Schema.Type<NavigateTo>>((queue) => {
        const click = (mouse: Event) => {
          if (
            !(mouse instanceof MouseEvent) ||
            mouse.button !== 0 ||
            mouse.metaKey ||
            mouse.ctrlKey ||
            mouse.shiftKey ||
            mouse.altKey
          ) {
            return;
          }
          mouse.preventDefault();
          Queue.offerUnsafe(queue, navigateTo({ path }));
        };
        return Effect.gen(function* () {
          element.addEventListener("click", click);
          yield* Effect.addFinalizer(() =>
            Effect.sync(() => element.removeEventListener("click", click)),
          );
        });
      }),
  });
}
