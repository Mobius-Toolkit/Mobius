import { ChevronRightIcon } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemGroup,
  ItemTitle,
} from '@/components/ui/item'
import { settingsPages } from '@/lib/settings'

export function Settings() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Settings</CardTitle>
      </CardHeader>
      <CardContent>
        <ItemGroup className="gap-1">
          {settingsPages.map((page) => (
            <Item key={page.path} asChild>
              <a href={page.path}>
                <ItemContent>
                  <ItemTitle>{page.title}</ItemTitle>
                </ItemContent>
                <ItemActions>
                  <ChevronRightIcon className="size-4" />
                </ItemActions>
              </a>
            </Item>
          ))}
        </ItemGroup>
      </CardContent>
    </Card>
  )
}
