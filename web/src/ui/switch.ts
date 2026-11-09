import type { Html, HtmlBuilder } from "foldkit/html";

const switchClass =
  "peer group/switch relative inline-flex shrink-0 items-center rounded-full border border-transparent transition-all outline-none group-has-[:focus-visible]/field-label:border-transparent group-has-[:focus-visible]/field-label:ring-0 after:absolute after:-inset-x-3 after:-inset-y-2 focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 data-[size=default]:h-[18.4px] data-[size=default]:w-[32px] data-[size=sm]:h-[14px] data-[size=sm]:w-[24px] dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40 data-checked:bg-primary data-unchecked:bg-input dark:data-unchecked:bg-input/80 data-disabled:cursor-not-allowed data-disabled:opacity-50";

const thumbClass =
  "pointer-events-none block rounded-full bg-background ring-0 transition-transform group-data-[size=default]/switch:size-4 group-data-[size=sm]/switch:size-3 group-data-[size=default]/switch:data-checked:translate-x-[calc(100%-2px)] group-data-[size=sm]/switch:data-checked:translate-x-[calc(100%-2px)] dark:data-checked:bg-primary-foreground group-data-[size=default]/switch:data-unchecked:translate-x-0 group-data-[size=sm]/switch:data-unchecked:translate-x-0 dark:data-unchecked:bg-foreground";

export function renderSwitch<M>(
  h: HtmlBuilder<M>,
  options: { id: string; checked: boolean; disabled: boolean; toggle: M },
): Html {
  const state = options.checked ? "checked" : "unchecked";
  return h.button(
    [
      h.Type("button"),
      h.Role("switch"),
      h.Id(options.id),
      h.AriaChecked(options.checked),
      h.Disabled(options.disabled),
      h.DataAttribute("slot", "switch"),
      h.DataAttribute("size", "default"),
      h.DataAttribute("state", state),
      h.Class(switchClass),
      h.OnClick(options.toggle),
      ...(options.disabled ? [h.DataAttribute("disabled", "")] : []),
    ],
    [
      h.span([
        h.DataAttribute("slot", "switch-thumb"),
        h.DataAttribute("state", state),
        h.Class(thumbClass),
      ]),
    ],
  );
}
