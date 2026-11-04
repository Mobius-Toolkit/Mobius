import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef, type ComponentProps, type ReactNode } from "react";
import type { InboxItem, Workstream } from "@/api/api.gen";
import { LoginContext } from "@/lib/login";
import type { ChatPorts } from "./chat/ports";
import { createChatProgram } from "./chat/program";

// The chat screen runs in Foldkit in web/src/chat/ as one program.
// Conversation.tsx is the React host that embeds the Foldkit program.
function ChatProgram({
  organization,
  repository,
  workstream,
  agent,
  unread,
  head,
  brief,
  note,
  trayItems = [],
  workstreams,
}: {
  organization: string;
  repository: string;
  workstream: number;
  agent: string;
  source?: EventSource;
  unread?: number;
  head: ReactNode;
  brief?: Workstream;
  note?: ReactNode;
  trayItems?: InboxItem[];
  workstreams?: Workstream[];
}) {
  const showLogin = use(LoginContext);
  const navigate = useNavigate();
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<ChatPorts>>(null);

  // The Flags are organization, repository, workstream and agent.
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

    // Outbound Ports: login and navigation
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

  // Inbound Ports carry host data that changes
  useEffect(() => {
    handleRef.current?.ports.unread.send(unread ?? null);
  }, [unread]);

  useEffect(() => {
    handleRef.current?.ports.brief.send(brief ?? null);
  }, [brief]);

  useEffect(() => {
    handleRef.current?.ports.trayItems.send(trayItems);
  }, [trayItems]);

  useEffect(() => {
    handleRef.current?.ports.workstreams.send(workstreams ?? null);
  }, [workstreams]);

  return (
    <section className="group/chat flex min-h-0 min-w-0 grow flex-col">
      <header className="hidden min-h-14 items-center gap-2 border-b px-4 py-2 md:flex">
        {head}
      </header>
      {note}
      <div id="chat-app" ref={containerRef} className="flex min-h-0 min-w-0 grow flex-col" />
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
