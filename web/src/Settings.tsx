import { Link } from "@tanstack/react-router";
import { ChevronRightIcon } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Item, ItemActions, ItemContent, ItemGroup, ItemTitle } from "@/components/ui/item";
import { settingsPages } from "@/lib/settings";
import { TopBar } from "./TopBar";

export function Settings() {
  return (
    <>
      <TopBar title="Settings" />
      <Card>
        <CardHeader className="max-md:hidden">
          <CardTitle>Settings</CardTitle>
        </CardHeader>
        <CardContent>
          <ItemGroup className="gap-1">
            {settingsPages.map((page) => (
              <Item key={page.path} asChild>
                <Link to={page.path}>
                  <ItemContent>
                    <ItemTitle>{page.title}</ItemTitle>
                  </ItemContent>
                  <ItemActions>
                    <ChevronRightIcon className="size-4" />
                  </ItemActions>
                </Link>
              </Item>
            ))}
          </ItemGroup>
        </CardContent>
      </Card>
    </>
  );
}
