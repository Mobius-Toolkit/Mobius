import * as React from "react";
import { cn } from "cn";
import { CheckIcon } from "lucide-react";

function Notice({ className, children, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      role="status"
      data-slot="notice"
      className={cn(
        "flex max-w-md items-start gap-2 rounded-xl bg-card px-3 py-2 text-sm text-card-foreground shadow-md ring-1 ring-foreground/10",
        className,
      )}
      {...props}
    >
      <CheckIcon
        aria-hidden
        className="mt-0.5 size-4 shrink-0 text-green-600 dark:text-green-400"
      />
      <span className="line-clamp-2 min-w-0 wrap-anywhere">{children}</span>
    </div>
  );
}

export { Notice };
