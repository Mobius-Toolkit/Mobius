import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef } from "react";
import type { GitHubApp } from "@/api/api.gen";
import { LoginContext } from "@/lib/login";
import type { GitHubPorts } from "./settings/github/ports";
import { createGitHubProgram } from "./settings/github/program";
import { TopBar } from "./TopBar";

// With no back, the component is the first screen of a server that has no App.
export function GitHub({ apps, back }: { apps: GitHubApp[]; back?: boolean }) {
  const showLogin = use(LoginContext);
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<GitHubPorts>>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { setup: !back };
    const handle = Runtime.embed(createGitHubProgram(container, flags), {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });

    return () => {
      unsubLogin();
      handle.dispose();
      handleRef.current = null;
    };
  }, [back, showLogin]);

  useEffect(() => {
    handleRef.current?.ports.apps.send(apps);
  }, [apps]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      {back && <TopBar title="Connect GitHub" back="/settings" />}
      <div id="github-app-wrapper" className={back ? undefined : "w-full max-w-lg"}>
        <div id="github-app" ref={containerRef} />
      </div>
    </>
  );
}
