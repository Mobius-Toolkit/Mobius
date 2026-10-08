import { Link, type LinkProps } from "@tanstack/react-router";
import { ChevronRightIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

// The lists go from edge to edge on the phone, so all other content of a page needs this inset.
export const inset = "px-4 md:px-0";

export const rowClass = "flex min-h-11 items-center gap-4 px-4 py-2 md:px-2";

// The phone shows the title and the controls of a page in the top bar.
export function PageHeader({ title, children }: { title: ReactNode; children?: ReactNode }) {
  return (
    <div className="hidden items-center justify-between gap-4 md:flex">
      <h2 className="min-w-0 truncate font-semibold">{title}</h2>
      {children}
    </div>
  );
}

export function Section({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <section className="grid gap-1">
      <h3
        className={cn("text-xs font-semibold tracking-wide text-muted-foreground uppercase", inset)}
      >
        {title}
      </h3>
      {children}
    </section>
  );
}

// The negative margin on desktop puts the text of a row at the edge of the page.
export function List({ children }: { children: ReactNode }) {
  return <ul className="divide-y md:-mx-2">{children}</ul>;
}

export function Row({ className, children }: { className?: string; children: ReactNode }) {
  return <li className={cn(rowClass, className)}>{children}</li>;
}

export function LinkRow({
  link,
  title,
  children,
}: {
  link: LinkProps;
  title: ReactNode;
  children?: ReactNode;
}) {
  return (
    <li>
      <Link {...link} className={cn(rowClass, "hover:bg-muted")}>
        <span className="min-w-0 grow break-words">{title}</span>
        {children}
        <ChevronRightIcon className="size-4 shrink-0" />
      </Link>
    </li>
  );
}

export function ErrorBadge({ children }: { children: ReactNode }) {
  return (
    <div className={inset}>
      <Badge variant="destructive" className="h-auto whitespace-normal">
        {children}
      </Badge>
    </div>
  );
}
