import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useEffectEvent, useMemo, useRef } from "react";
import type { InboxItem } from "@/api/api.gen";
import { LoginContext } from "@/lib/login";
import type { InboxPorts } from "./inbox/ports";
import { createInboxProgram } from "./inbox/program";

function InboxProgram({
  organization,
  items,
  selectOrganization,
}: {
  organization: string;
  items: InboxItem[];
  selectOrganization: (organization: string) => void;
}) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<InboxPorts>>(null);
  const select = useEffectEvent(selectOrganization);
  const shown = useMemo(
    () => items.filter((item) => item.organization === organization),
    [items, organization],
  );

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { organization };
    const handle = Runtime.embed(createInboxProgram(container, flags), {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubNav = handle.ports.navigationRequested.subscribe((path) => {
      void navigate({ to: path });
    });
    const unsubOrganization = handle.ports.organizationRequested.subscribe(select);

    return () => {
      unsubLogin();
      unsubNav();
      unsubOrganization();
      handle.dispose();
      handleRef.current = null;
    };
  }, [organization, showLogin, navigate]);

  useEffect(() => {
    handleRef.current?.ports.trayItems.send(shown);
  }, [shown]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <div id="inbox-app-wrapper">
      <div id="inbox-app" ref={containerRef} />
    </div>
  );
}

// An organization change gets a new program, so its inbound Port starts with the items of that organization.
export function Inbox(props: Parameters<typeof InboxProgram>[0]) {
  return <InboxProgram key={props.organization} {...props} />;
}
