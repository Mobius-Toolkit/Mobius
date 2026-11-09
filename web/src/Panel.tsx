import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { use, useEffect, useEffectEvent, useRef } from "react";
import { LoginContext } from "@/lib/login";
import type { PanelPorts } from "./panel/ports";
import { createPanelProgram } from "./panel/program";
import type { Tab } from "./panel/schemas";

// The panel of a Workstream: the tabs Tasks, Agents, Details, Brief and Recurring. The open tab stays in the page, because the
// Brief button of the chat selects the Brief tab. instance names the place of the panel, because the page can hold
// two panels at the same time.
export function Panel({
  owner,
  name,
  number,
  instance,
  tab,
  onTabChange,
  onTaskAttached,
}: {
  owner: string;
  name: string;
  number: number;
  instance: string;
  tab: Tab;
  onTabChange: (tab: Tab) => void;
  onTaskAttached?: () => void;
}) {
  const showLogin = use(LoginContext);
  const containerRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<Runtime.EmbedHandle<PanelPorts>>(null);
  const currentTab = useEffectEvent(() => tab);
  const changeTab = useEffectEvent(onTabChange);
  const attachedTask = useEffectEvent(() => onTaskAttached?.());

  // A change of a Flag disposes the program and embeds it again.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const flags = { owner, name, number, instance, tab: currentTab() };
    const handle = Runtime.embed(createPanelProgram(container, flags), {
      flags: Effect.succeed(flags),
    });
    handleRef.current = handle;

    const unsubLogin = handle.ports.loginRequested.subscribe(() => {
      showLogin();
    });
    const unsubTab = handle.ports.tabChanged.subscribe(changeTab);
    const unsubAttached = handle.ports.taskAttached.subscribe(attachedTask);

    return () => {
      unsubLogin();
      unsubTab();
      unsubAttached();
      handle.dispose();
      handleRef.current = null;
    };
  }, [owner, name, number, instance, showLogin]);

  useEffect(() => {
    handleRef.current?.ports.tab.send(tab);
  }, [tab]);

  // Foldkit replaces the container element with its own root. Thus the wrapper keeps the container as its only child,
  // and React never moves the container.
  return (
    <div id={`panel-${instance}-wrapper`} className="flex min-h-0 grow flex-col">
      <div id={`panel-${instance}`} ref={containerRef} className="flex min-h-0 grow flex-col" />
    </div>
  );
}
