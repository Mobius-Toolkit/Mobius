import * as React from "react";
import { type VariantProps } from "class-variance-authority";
import { cn } from "cn";
import { Slot } from "radix-ui";
import { buttonVariants } from "@/components/ui/button-variants";
import { Spinner } from "@/components/ui/spinner";

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  pending = false,
  disabled,
  children,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
    pending?: boolean;
  }) {
  const Comp = asChild ? Slot.Root : "button";

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      disabled={disabled || pending}
      {...props}
    >
      {pending ? (
        <>
          <Spinner aria-hidden />
          {children}
        </>
      ) : (
        children
      )}
    </Comp>
  );
}

export { Button };
