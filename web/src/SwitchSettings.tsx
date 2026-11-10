import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { useEffect, useEffectEvent, useRef } from "react";
import { useGoBack } from "@/lib/goBack";
import { useShell } from "@/lib/shell";
import { createSwitchesProgram } from "./settings/switches/program";
import type { SwitchesPorts } from "./settings/switches/ports";
import { TopBar } from "./TopBar";

export function SwitchSettings() {
  const { switches, switchErrors, changeSwitch } = useShell();
  const goBack = useGoBack("/settings");
  const change = useEffectEvent(changeSwitch);
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<SwitchesPorts>>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = {};
    const handle = Runtime.embed(createSwitchesProgram(container, flags), {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

    const unsubBack = handle.ports.backRequested.subscribe(goBack);
    const unsubSwitch = handle.ports.switchRequested.subscribe(({ kind, on }) => {
      void change(kind, on);
    });

    return () => {
      unsubBack();
      unsubSwitch();
      handle.dispose();
      handleRef.current = null;
    };
  }, [goBack]);

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
      <TopBar title="Switches" back="/settings" />
      <div id="switches-app-wrapper">
        <div id="switches-app" ref={containerRef} />
      </div>
    </>
  );
}
