import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef } from "react";
import { LoginContext } from "@/lib/login";
import type { TasksPorts } from "./tasks/ports";
import { createTasksProgram } from "./tasks/program";

export function Tasks({ organization }: { organization: string }) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const containerRef = useRef<HTMLDivElement>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { organization };
    const handle: Runtime.EmbedHandle<TasksPorts> = Runtime.embed(
      createTasksProgram(container, flags),
      { flags: Effect.succeed(flags) },
    );

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubNav = handle.ports.navigationRequested.subscribe((path) => {
      void navigate({ to: path });
    });

    return () => {
      unsubLogin();
      unsubNav();
      handle.dispose();
    };
  }, [organization, showLogin, navigate]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <div id="tasks-app-wrapper" className="flex min-h-0 min-w-0 grow flex-col">
      <div id="tasks-app" ref={containerRef} className="flex min-h-0 min-w-0 grow flex-col" />
    </div>
  );
}
