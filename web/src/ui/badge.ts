import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import { badgeVariants } from "@/components/ui/badge-variants";

type Variant = NonNullable<Parameters<typeof badgeVariants>[0]>["variant"];

export function badgeClass(variant: Variant, className?: string) {
  return cn(badgeVariants({ variant }), className);
}

export function renderBadge<M>(
  h: HtmlBuilder<M>,
  variant: NonNullable<Variant>,
  text: string,
  className?: string,
): Html {
  return h.span(
    [
      h.DataAttribute("slot", "badge"),
      h.DataAttribute("variant", variant),
      h.Class(badgeClass(variant, className)),
    ],
    [text],
  );
}
