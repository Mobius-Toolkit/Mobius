import { Link, Outlet, useLocation } from "@tanstack/react-router";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { TopBar } from "./TopBar";

export function InboxTabs({ count }: { count: number }) {
  const { pathname } = useLocation();
  return (
    <>
      <TopBar title="Inbox" />
      <h2 className="text-base leading-none font-semibold max-md:hidden">Inbox</h2>
      <Tabs value={pathname === "/inbox/activity" ? "activity" : "todo"}>
        <TabsList className="w-full">
          <TabsTrigger value="todo" asChild>
            <Link to="/inbox" activeOptions={{ exact: true }}>
              To do
              {count > 0 && <Badge>{count}</Badge>}
            </Link>
          </TabsTrigger>
          <TabsTrigger value="activity" asChild>
            <Link to="/inbox/activity">Activity</Link>
          </TabsTrigger>
        </TabsList>
      </Tabs>
      <Outlet />
    </>
  );
}
