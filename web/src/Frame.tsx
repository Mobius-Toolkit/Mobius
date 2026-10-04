import { SettingsIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { settingsPages } from '@/lib/settings'
import { cn } from '@/lib/utils'
import { OrganizationSwitch } from './OrganizationSwitch'

export function Frame({
  path,
  organizations,
  organization,
  onSelect,
  children,
}: {
  path: string
  organizations: string[]
  organization: string
  onSelect: (organization: string) => void
  children: ReactNode
}) {
  const organizationSwitch = organizations.length > 1 && (
    <OrganizationSwitch
      organizations={organizations}
      organization={organization}
      onSelect={onSelect}
    />
  )
  return (
    <div className="flex min-h-svh">
      <nav className="sticky top-0 hidden h-svh w-52 shrink-0 flex-col gap-1 border-r bg-sidebar p-2 text-sidebar-foreground md:flex">
        <div className="px-2 py-1">
          {organizationSwitch || <span className="font-semibold">Mobius</span>}
        </div>
        <div className="grow" />
        {settingsPages.map((page) => (
          <Button
            key={page.path}
            asChild
            variant={page.path === path ? 'secondary' : 'ghost'}
            className="justify-start"
          >
            <a
              href={page.path}
              aria-current={page.path === path ? 'page' : undefined}
            >
              {page.title}
            </a>
          </Button>
        ))}
      </nav>
      <div className="flex min-w-0 grow flex-col pb-20 md:pb-0">
        {organizationSwitch && (
          <header className="px-4 pt-4 md:hidden">{organizationSwitch}</header>
        )}
        <main className="mx-auto grid w-full max-w-3xl content-start gap-6 p-4 md:p-6">
          {children}
        </main>
      </div>
      <nav className="fixed inset-x-0 bottom-0 flex border-t bg-sidebar pb-[env(safe-area-inset-bottom)] md:hidden">
        <a
          href="/settings"
          aria-current={path === '/settings' ? 'page' : undefined}
          className={cn(
            'flex flex-1 flex-col items-center gap-1 py-2 text-xs',
            path === '/settings'
              ? 'font-medium text-foreground'
              : 'text-muted-foreground',
          )}
        >
          <SettingsIcon className="size-5" />
          Settings
        </a>
      </nav>
    </div>
  )
}
