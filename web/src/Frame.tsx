import { LayersIcon, SettingsIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { useNewBuild } from '@/lib/build'
import { settingsPages } from '@/lib/settings'
import { useUpgrade } from '@/lib/upgrade'
import { cn } from '@/lib/utils'
import { OrganizationSwitch } from './OrganizationSwitch'
import { UpgradeControls, UpgradeDialog } from './Upgrade'

const tabs = [
  { path: '/workstreams', title: 'Workstreams', Icon: LayersIcon },
  { path: '/settings', title: 'Settings', Icon: SettingsIcon },
]

function SideLink({
  path,
  title,
  current,
}: {
  path: string
  title: string
  current: string
}) {
  return (
    <Button
      asChild
      variant={path === current ? 'secondary' : 'ghost'}
      className="justify-start"
    >
      <a href={path} aria-current={path === current ? 'page' : undefined}>
        {title}
      </a>
    </Button>
  )
}

export function Frame({
  path,
  organizations,
  organization,
  onSelect,
  source,
  children,
}: {
  path: string
  organizations: string[]
  organization: string
  onSelect: (organization: string) => void
  source?: EventSource
  children: ReactNode
}) {
  const upgrade = useUpgrade(source)
  const newBuild = useNewBuild()
  const organizationSwitch = organizations.length > 1 && (
    <OrganizationSwitch
      organizations={organizations}
      organization={organization}
      onSelect={onSelect}
    />
  )
  const upgradeControls = (
    <UpgradeControls upgrade={upgrade} newBuild={newBuild} />
  )
  return (
    <div className="flex min-h-svh">
      <nav className="sticky top-0 hidden h-svh w-52 shrink-0 flex-col gap-1 border-r bg-sidebar p-2 text-sidebar-foreground md:flex">
        <div className="px-2 py-1">
          {organizationSwitch || <span className="font-semibold">Mobius</span>}
        </div>
        <SideLink path="/workstreams" title="Workstreams" current={path} />
        <div className="grow" />
        {upgradeControls}
        {settingsPages.map((page) => (
          <SideLink
            key={page.path}
            path={page.path}
            title={page.title}
            current={path}
          />
        ))}
      </nav>
      <div className="flex min-w-0 grow flex-col pb-20 md:pb-0">
        <header className="grid gap-2 px-4 pt-4 empty:hidden md:hidden">
          {organizationSwitch}
          {upgradeControls}
        </header>
        <main className="mx-auto grid w-full max-w-3xl content-start gap-6 p-4 md:p-6">
          {children}
        </main>
      </div>
      <nav className="fixed inset-x-0 bottom-0 flex border-t bg-sidebar pb-[env(safe-area-inset-bottom)] md:hidden">
        {tabs.map(({ path: tabPath, title, Icon }) => (
          <a
            key={tabPath}
            href={tabPath}
            aria-current={path === tabPath ? 'page' : undefined}
            className={cn(
              'flex flex-1 flex-col items-center gap-1 py-2 text-xs',
              path === tabPath
                ? 'font-medium text-foreground'
                : 'text-muted-foreground',
            )}
          >
            <Icon className="size-5" />
            {title}
          </a>
        ))}
      </nav>
      <UpgradeDialog upgrade={upgrade} />
    </div>
  )
}
