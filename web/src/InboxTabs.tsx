import { Link, Outlet, useLocation } from "@tanstack/react-router";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

export function InboxTabs({ count }: { count: number }) {
  const { pathname } = useLocation();
  return (
    <>
      <h2 className="leading-none font-semibold">Inbox</h2>
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
