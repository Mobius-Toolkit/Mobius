import { useNavigate } from "@tanstack/react-router";
import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useRef, type ReactNode } from "react";
import type { InboxItem, Workstream } from "@/api/api.gen";
import { LoginContext } from "@/lib/login";
import type { ChatPorts } from "./chat/ports";
import { createChatProgram } from "./chat/program";

// The chat screen runs in Foldkit in web/src/chat/ as one program.
// Conversation.tsx is the React host that embeds the Foldkit program.
export function Conversation({
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
  const wrapperRef = useRef<HTMLDivElement>(null);
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

  const chatKey = `${organization}/${repository}/${workstream}`;
  const openUnreadRef = useRef<{ key: string; count: number } | null>(null);

  useEffect(() => {
    if (unread !== undefined && openUnreadRef.current?.key !== chatKey) {
      openUnreadRef.current = { key: chatKey, count: unread };
    }
  }, [unread, chatKey]);

  // Chat scroll management: keep pinned at bottom, maintain anchor when older messages prepend,
  // and dispatch scroll when scrolled to top
  useEffect(() => {
    const wrapper = wrapperRef.current;
    if (!wrapper) return;
    wrapper.setAttribute("data-chat-target", chatKey);

    let initialScrolled = false;
    let hookedList: HTMLElement | null = null;
    let anchor: { id: string; offset: number } | null = null;

    const hookScrollTop = (list: HTMLElement) => {
      if (hookedList === list) return;
      hookedList = list;
      const desc =
        Object.getOwnPropertyDescriptor(Element.prototype, "scrollTop") ||
        Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollTop");
      if (desc?.set) {
        Object.defineProperty(list, "scrollTop", {
          get() {
            return desc.get ? desc.get.call(this) : 0;
          },
          set(value: number) {
            desc.set?.call(this, value);
            if (value < 600) {
              list.dispatchEvent(new Event("scroll"));
            }
          },
          configurable: true,
        });
      }
    };

    const checkScroll = () => {
      const list = wrapper.querySelector("[data-message]")?.parentElement;
      if (!list) return;
      hookScrollTop(list);

      const hasBrief = list.querySelector('section[aria-label="Brief"]') !== null;
      if (hasBrief) {
        anchor = null;
      }

      // Check if older messages were prepended
      if (anchor) {
        const anchorElem = list.querySelector(`[data-message="${anchor.id}"]`);
        if (anchorElem) {
          const currentOffset =
            anchorElem.getBoundingClientRect().top - list.getBoundingClientRect().top;
          list.scrollTop += currentOffset - anchor.offset;
        }
        anchor = null;
      }

      const elements = list.querySelectorAll("[data-message]");
      if (elements.length === 0) return;

      const firstElem = elements[0];
      const currentFirstId = firstElem.getAttribute("data-message");

      // When scrollTop < 600 and no Brief yet, save anchor of current first element
      if (!hasBrief && list.scrollTop < 600 && currentFirstId) {
        anchor = {
          id: currentFirstId,
          offset: firstElem.getBoundingClientRect().top - list.getBoundingClientRect().top,
        };
      }

      const openUnread = openUnreadRef.current?.count;
      if (openUnread === undefined) return;

      if (!initialScrolled) {
        if (openUnread > 0) {
          if (elements.length < openUnread) {
            return;
          }
          const target = elements[Math.max(0, elements.length - openUnread)];
          if (target instanceof HTMLElement) {
            target.scrollIntoView();
          } else {
            list.scrollTop = list.scrollHeight;
          }
        } else {
          list.scrollTop = list.scrollHeight;
        }
        initialScrolled = true;
        return;
      }
    };

    checkScroll();

    const observer = new MutationObserver(() => {
      checkScroll();
    });

    observer.observe(wrapper, { childList: true, subtree: true });

    return () => {
      observer.disconnect();
    };
  }, [chatKey]);

  return (
    <section className="group/chat flex min-h-0 min-w-0 grow flex-col">
      <header className="hidden min-h-14 items-center gap-2 border-b px-4 py-2 md:flex">
        {head}
      </header>
      {note}
      <div id="chat-app-wrapper" ref={wrapperRef} className="flex min-h-0 min-w-0 grow flex-col">
        <div id="chat-app" ref={containerRef} className="flex min-h-0 min-w-0 grow flex-col" />
      </div>
    </section>
  );
}
