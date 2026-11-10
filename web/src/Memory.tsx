import { useNavigate, type LinkProps } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef } from "react";
import { useGoBack } from "@/lib/goBack";
import { LoginContext } from "@/lib/login";
import { useShell } from "@/lib/shell";
import type { Flags } from "./settings/memory/model";
import { pageTitle } from "./settings/memory/model";
import type { MemoryPorts } from "./settings/memory/ports";
import { createMemoryProgram } from "./settings/memory/program";
import { TopBar } from "./TopBar";

function MemoryHost({ page, owner, name, file, back }: Flags & { back: LinkProps["to"] }) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const { apps, organizations } = useShell();
  const goBack = useGoBack(back);
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<MemoryPorts>>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { page, owner, name, file };
    const handle = Runtime.embed(createMemoryProgram(container, flags), {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubNav = handle.ports.navigationRequested.subscribe((path) => {
      void navigate({ to: path });
    });
    const unsubBack = handle.ports.backRequested.subscribe(goBack);

    return () => {
      unsubLogin();
      unsubNav();
      unsubBack();
      handle.dispose();
      handleRef.current = null;
    };
  }, [page, owner, name, file, showLogin, navigate, goBack]);

  useEffect(() => {
    handleRef.current?.ports.apps.send(apps);
  }, [apps]);

  useEffect(() => {
    handleRef.current?.ports.organizations.send(organizations);
  }, [organizations]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      <TopBar title={pageTitle({ page, owner, name, file })} back={back} />
      <div id="memory-app-wrapper">
        <div id="memory-app" ref={containerRef} />
      </div>
    </>
  );
}

export function MemoryRepositories() {
  return <MemoryHost page="repositories" owner="" name="" file="" back="/settings" />;
}

export function MemoryFiles({ owner, name }: { owner: string; name: string }) {
  return <MemoryHost page="files" owner={owner} name={name} file="" back="/settings/memory" />;
}

export function Memory({ owner, name, file }: { owner: string; name: string; file: string }) {
  return (
    <MemoryHost
      page="file"
      owner={owner}
      name={name}
      file={file}
      back="/settings/memory/$owner/$name"
    />
  );
}
