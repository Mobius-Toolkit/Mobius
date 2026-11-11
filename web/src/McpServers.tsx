import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef } from "react";
import { useGoBack } from "@/lib/goBack";
import { LoginContext } from "@/lib/login";
import type { McpServersPorts } from "./settings/mcp-servers/ports";
import { createMcpServersProgram } from "./settings/mcp-servers/program";
import type { Callback } from "./settings/mcp-servers/schemas";
import { TopBar } from "./TopBar";

export function McpServers({
  organization,
  callback,
}: {
  organization: string;
  callback: Callback;
}) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const goBack = useGoBack("/settings");
  const containerRef = useRef<HTMLDivElement>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { organization, callback };
    const handle: Runtime.EmbedHandle<McpServersPorts> = Runtime.embed(
      createMcpServersProgram(container, flags),
      { flags: Effect.succeed(flags) },
    );

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubNav = handle.ports.navigationRequested.subscribe((path) => {
      void navigate({ to: path, replace: true });
    });
    const unsubBack = handle.ports.backRequested.subscribe(goBack);

    return () => {
      unsubLogin();
      unsubNav();
      unsubBack();
      handle.dispose();
    };
  }, [organization, callback, showLogin, navigate, goBack]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      <TopBar title="MCP servers" back="/settings" />
      <div id="mcp-servers-app-wrapper">
        <div id="mcp-servers-app" ref={containerRef} />
      </div>
    </>
  );
}
