import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef } from "react";
import { LoginContext } from "@/lib/login";
import type { DevicesPorts } from "./settings/devices/ports";
import { createDevicesProgram } from "./settings/devices/program";
import { TopBar } from "./TopBar";

export function Devices() {
  const showLogin = use(LoginContext);
  const containerRef = useRef<HTMLDivElement>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = {};
    const handle: Runtime.EmbedHandle<DevicesPorts> = Runtime.embed(
      createDevicesProgram(container, flags),
      { flags: Effect.succeed(flags) },
    );

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });

    return () => {
      unsubLogin();
      handle.dispose();
    };
  }, [showLogin]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      <TopBar title="Devices" back="/settings" />
      <div id="devices-app-wrapper">
        <div id="devices-app" ref={containerRef} />
      </div>
    </>
  );
}
