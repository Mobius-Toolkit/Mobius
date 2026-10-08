import { Link, Outlet, useLocation } from "@tanstack/react-router";
import { inset, PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { TopBar } from "./TopBar";

export function InboxTabs({ count }: { count: number }) {
  const { pathname } = useLocation();
  return (
    <>
      <TopBar title="Inbox" />
      <PageHeader title="Inbox" />
      <Tabs value={pathname === "/inbox/activity" ? "activity" : "todo"} className={inset}>
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
