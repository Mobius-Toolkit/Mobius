import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef } from "react";
import { useGoBack } from "@/lib/goBack";
import { LoginContext } from "@/lib/login";
import type { RolesPorts } from "./settings/roles/ports";
import { createRolesProgram } from "./settings/roles/program";
import { TopBar } from "./TopBar";

export function Roles() {
  const showLogin = use(LoginContext);
  const goBack = useGoBack("/settings");
  const containerRef = useRef<HTMLDivElement>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = {};
    const handle: Runtime.EmbedHandle<RolesPorts> = Runtime.embed(
      createRolesProgram(container, flags),
      { flags: Effect.succeed(flags) },
    );

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubBack = handle.ports.backRequested.subscribe(goBack);

    return () => {
      unsubLogin();
      unsubBack();
      handle.dispose();
    };
  }, [showLogin, goBack]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      <TopBar title="Roles" back="/settings" />
      <div id="roles-app-wrapper">
        <div id="roles-app" ref={containerRef} />
      </div>
    </>
  );
}
