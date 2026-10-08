import type { LinkProps } from "@tanstack/react-router";
import { use, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { TopBarContext } from "@/lib/topbar";
import { BackButton } from "./BackButton";

// onBack replaces the history step of the back button.
// children are the controls of the page, after the title.
export function TopBar({
  title,
  back,
  onBack,
  children,
}: {
  title: string;
  back?: LinkProps["to"];
  onBack?: () => void;
  children?: ReactNode;
}) {
  const topBar = use(TopBarContext);
  if (!topBar) {
    return null;
  }
  return createPortal(
    <>
      {back && <BackButton parent={back} onBack={onBack} />}
      <h1 className="min-w-0 truncate font-semibold">{title}</h1>
      {children}
    </>,
    topBar,
  );
}
