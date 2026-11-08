import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef, useState } from "react";
import { LoginContext } from "@/lib/login";
import type { AgentsPorts } from "./agents/ports";
import { createAgentsProgram } from "./agents/program";
import { TopBar } from "./TopBar";

export function Agents() {
  const showLogin = use(LoginContext);
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<AgentsPorts>>(null);
  const [title, setTitle] = useState("");

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = {};
    const handle = Runtime.embed(createAgentsProgram(container, flags), {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubTitle = handle.ports.titleChanged.subscribe(setTitle);

    return () => {
      unsubLogin();
      unsubTitle();
      handle.dispose();
      handleRef.current = null;
    };
  }, [showLogin]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      <TopBar
        title={title || "Agents"}
        back="/settings"
        onBack={title ? () => handleRef.current?.ports.backRequested.send(undefined) : undefined}
      />
      <div id="agents-app-wrapper">
        <div id="agents-app" ref={containerRef} />
      </div>
    </>
  );
}
