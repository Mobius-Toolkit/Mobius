import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef, type ComponentProps, type ReactNode } from "react";
import type { InboxItem, Workstream } from "@/api/api.gen";
import { LoginContext } from "@/lib/login";
import type { ChatPorts } from "./chat/ports";
import { createChatProgram } from "./chat/program";

function ChatProgram({
  organization,
  repository,
  workstream,
  agent,
  head,
  brief,
  note,
  trayItems = [],
}: {
  organization: string;
  repository: string;
  workstream: number;
  agent: string;
  head: ReactNode;
  brief?: Workstream;
  note?: ReactNode;
  trayItems?: InboxItem[];
}) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<ChatPorts>>(null);

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { organization, repository, workstream, agent };
    const program = createChatProgram(container, flags);
    const handle = Runtime.embed(program, {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubNav = handle.ports.navigationRequested.subscribe((path) => {
      void navigate({ to: path });
    });

    return () => {
      unsubLogin();
      unsubNav();
      handle.dispose();
      handleRef.current = null;
    };
  }, [organization, repository, workstream, agent, showLogin, navigate]);

  useEffect(() => {
    handleRef.current?.ports.brief.send(brief ?? null);
  }, [brief]);

  useEffect(() => {
    handleRef.current?.ports.trayItems.send(trayItems);
  }, [trayItems]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <section className="group/chat flex min-h-0 min-w-0 grow flex-col">
      <header className="hidden min-h-14 items-center gap-2 border-b px-4 py-2 md:flex">
        {head}
      </header>
      {note}
      <div id="chat-app-wrapper" className="flex min-h-0 min-w-0 grow flex-col">
        <div id="chat-app" ref={containerRef} className="flex min-h-0 min-w-0 grow flex-col" />
      </div>
    </section>
  );
}

// A new chat gets a new program, so its inbound Ports start with the data of that chat.
export function Conversation(props: ComponentProps<typeof ChatProgram>) {
  return (
    <ChatProgram
      key={`${props.organization}/${props.repository}/${props.workstream}/${props.agent}`}
      {...props}
    />
  );
}
