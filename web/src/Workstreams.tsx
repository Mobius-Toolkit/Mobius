import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { useEffect, useRef } from "react";
import type { NeedsHuman } from "@/api/api.gen";
import { useRestoredScroll } from "@/lib/restoreScroll";
import type { WorkstreamsPorts } from "./workstreams/ports";
import { createWorkstreamsProgram } from "./workstreams/program";

function WorkstreamsProgram({
  organization,
  needsHuman,
}: {
  organization: string;
  needsHuman: NeedsHuman[];
}) {
  const navigate = useNavigate();
  const wrapperRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<WorkstreamsPorts>>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { organization };
    const handle = Runtime.embed(createWorkstreamsProgram(container, flags), {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

    const unsubNav = handle.ports.navigationRequested.subscribe((path) => {
      void navigate({ to: path });
    });

    return () => {
      unsubNav();
      handle.dispose();
      handleRef.current = null;
    };
  }, [organization, navigate]);

  useRestoredScroll(wrapperRef);

  useEffect(() => {
    handleRef.current?.ports.needsHuman.send(needsHuman);
  }, [needsHuman]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <div id="workstreams-app-wrapper" ref={wrapperRef}>
      <div id="workstreams-app" ref={containerRef} />
    </div>
  );
}

// An organization change gets a new program, so its inbound Port starts with the data of that organization.
export function Workstreams(props: Parameters<typeof WorkstreamsProgram>[0]) {
  return <WorkstreamsProgram key={props.organization} {...props} />;
}
