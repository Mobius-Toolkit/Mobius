import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef } from "react";
import { LoginContext } from "@/lib/login";
import { useShell } from "@/lib/shell";
import type { OverviewPorts } from "./settings/overview/ports";
import { createOverviewProgram } from "./settings/overview/program";
import { TopBar } from "./TopBar";

export function Settings() {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const { switches, switchErrors } = useShell();
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<OverviewPorts>>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = {};
    const handle = Runtime.embed(createOverviewProgram(container, flags), {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

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
      handleRef.current = null;
    };
  }, [showLogin, navigate]);

  useEffect(() => {
    handleRef.current?.ports.switches.send(
      switches ? { start: switches.start, merge: switches.merge, repairs: switches.repairs } : null,
    );
  }, [switches]);

  const { start = "", merge = "", repairs = "" } = switchErrors;
  useEffect(() => {
    handleRef.current?.ports.errors.send({ start, merge, repairs });
  }, [start, merge, repairs]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      <TopBar title="Settings" />
      <div id="settings-app-wrapper">
        <div id="settings-app" ref={containerRef} />
      </div>
    </>
  );
}
