import { cn } from "cn";
import { badgeVariants } from "@/components/ui/badge-variants";

export function badgeClass(
  variant: NonNullable<Parameters<typeof badgeVariants>[0]>["variant"],
  className?: string,
) {
  return cn(badgeVariants({ variant }), className);
}
