import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import { buttonVariants } from "@/components/ui/button-variants";
import { spinnerIcon } from "./icons";

export function buttonClass(options: NonNullable<Parameters<typeof buttonVariants>[0]>) {
  return cn(buttonVariants(options));
}

// autofocus marks the button for the focus of a dialog (defineDialogMount).
export function renderButton<M>(
  h: HtmlBuilder<M>,
  options: NonNullable<Parameters<typeof buttonVariants>[0]> & {
    label: string;
    pending?: boolean;
    disabled?: boolean;
    autofocus?: boolean;
    onClick: M;
  },
): Html {
  const {
    label,
    pending = false,
    disabled = false,
    autofocus = false,
    onClick,
    ...style
  } = options;
  return h.button(
    [
      h.Type("button"),
      h.DataAttribute("slot", "button"),
      h.Disabled(disabled || pending),
      h.Class(buttonClass(style)),
      h.OnClick(onClick),
      ...(autofocus ? [h.DataAttribute("autofocus", "")] : []),
    ],
    [pending ? spinnerIcon(h) : h.empty, label],
  );
}
