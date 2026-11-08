import { cn } from "cn";
import { buttonVariants } from "@/components/ui/button-variants";

export function buttonClass(options: NonNullable<Parameters<typeof buttonVariants>[0]>) {
  return cn(buttonVariants(options));
}
