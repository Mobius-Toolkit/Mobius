import { useNavigate, type LinkProps } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useEffectEvent, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { useGoBack } from "@/lib/goBack";
import { LoginContext } from "@/lib/login";
import { useShell } from "@/lib/shell";
import type { Page } from "./checkup/flags";
import type { CheckupPorts } from "./checkup/ports";
import { createCheckupProgram } from "./checkup/program";
import type { FixButton } from "./checkup/schemas";
import { TopBar } from "./TopBar";

// organization is empty on the pages that have no organization. back is the parent page of the top bar.
export function CheckupHost({
  page,
  organization = "",
  title,
  back,
}: {
  page: Page;
  organization?: string;
  title: string;
  back: LinkProps["to"];
}) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const { organizations, pauses, selectOrganization } = useShell();
  const goBack = useGoBack(back);
  const select = useEffectEvent(selectOrganization);
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<CheckupPorts>>(null);
  const [fix, setFix] = useState<FixButton | null>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { page, organization };
    const handle = Runtime.embed(createCheckupProgram(container, flags), {
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
    const unsubChat = handle.ports.chatRequested.subscribe((chatOrganization) => {
      select(chatOrganization);
      void navigate({ to: "/chat" });
    });
    const unsubFix = handle.ports.fixButtonChanged.subscribe(setFix);

    return () => {
      unsubLogin();
      unsubNav();
      unsubBack();
      unsubChat();
      unsubFix();
      handle.dispose();
      handleRef.current = null;
    };
  }, [page, organization, showLogin, navigate, goBack]);

  useEffect(() => {
    handleRef.current?.ports.organizations.send(organizations);
  }, [organizations]);

  useEffect(() => {
    handleRef.current?.ports.pauses.send(pauses);
  }, [pauses]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <>
      <TopBar title={title} back={back}>
        {fix && (
          <div className="mr-2 ml-auto">
            <Button
              pending={fix.pending}
              onClick={() => handleRef.current?.ports.fixRequested.send(undefined)}
            >
              {fix.text}
            </Button>
          </div>
        )}
      </TopBar>
      <div id="checkup-app-wrapper">
        <div id="checkup-app" ref={containerRef} />
      </div>
    </>
  );
}
