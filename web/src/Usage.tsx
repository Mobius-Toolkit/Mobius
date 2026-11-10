import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef, useState } from "react";
import { LoginContext } from "@/lib/login";
import type { UsageSearch } from "@/lib/usage";
import { TopBar } from "./TopBar";
import type { UsagePorts } from "./usage/ports";
import { createUsageProgram } from "./usage/program";

// The program keeps the search of the page after its start, and gives each new search to the address of the page.
export function Usage({ search }: { search: UsageSearch }) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate({ from: "/usage" });
  const containerRef = useRef<HTMLDivElement>(null);
  const [flags] = useState(() => ({
    search,
    now: Date.now(),
    timeZone: new Intl.DateTimeFormat().resolvedOptions().timeZone,
  }));

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const handle: Runtime.EmbedHandle<UsagePorts> = Runtime.embed(
      createUsageProgram(container, flags),
      { flags: Effect.succeed(flags) },
    );

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubSearch = handle.ports.searchChanged.subscribe((next) => {
      void navigate({ search: next, replace: true });
    });

    return () => {
      unsubLogin();
      unsubSearch();
      handle.dispose();
    };
  }, [flags, showLogin, navigate]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      <TopBar title="Usage" back="/settings" />
      <div id="usage-app-wrapper">
        <div id="usage-app" ref={containerRef} />
      </div>
    </>
  );
}
