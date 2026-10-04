import { use, useEffect, useState } from 'react'
import { listReleaseChanges } from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { LoginContext } from '@/lib/login'
import type { Upgrade } from '@/lib/upgrade'

function DrainText({ waiting }: { waiting: number }) {
  if (waiting === 0) {
    return 'Upgrade is ready to restart'
  }
  return `Upgrade waits for ${waiting} ${waiting === 1 ? 'agent' : 'agents'}`
}

export function UpgradeControls({
  upgrade,
  newBuild,
}: {
  upgrade: Upgrade
  newBuild: boolean
}) {
  return (
    <>
      {upgrade.drain?.on && (
        <p className="flex items-center gap-2 px-2 text-sm text-muted-foreground">
          <span className="size-2 shrink-0 rounded-full bg-amber-500" />
          <DrainText waiting={upgrade.drain.waiting} />
        </p>
      )}
      {upgrade.drain?.on ? (
        <Button variant="secondary" onClick={upgrade.cancel}>
          Cancel upgrade
        </Button>
      ) : (
        upgrade.version && (
          <Button
            variant="secondary"
            disabled={upgrade.upgrading}
            onClick={() => upgrade.setChangesShown(true)}
          >
            Upgrade{' '}
            <span className="text-muted-foreground">{upgrade.version}</span>
          </Button>
        )
      )}
      {upgrade.failure && (
        <Badge
          variant="destructive"
          className="h-auto w-full whitespace-normal"
        >
          {upgrade.failure}
        </Badge>
      )}
      {newBuild && (
        <Button variant="secondary" onClick={() => window.location.reload()}>
          New version
        </Button>
      )}
    </>
  )
}

function ReleaseChanges() {
  const showLogin = use(LoginContext)
  const [changes, setChanges] = useState<string[]>()
  const [error, setError] = useState<string>()

  useEffect(() => {
    listReleaseChanges()
      .then((res) => {
        if (res.status === 401) {
          showLogin()
        } else if (res.status === 200) {
          setChanges(res.data.data)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [showLogin])

  if (error) {
    return <Badge variant="destructive">{error}</Badge>
  }
  if (!changes) {
    return <p className="text-muted-foreground">Mobius reads the changes.</p>
  }
  return (
    <ul className="grid list-disc gap-1 pl-5">
      {changes.map((title, index) => (
        <li key={index}>{title}</li>
      ))}
    </ul>
  )
}

export function UpgradeDialog({ upgrade }: { upgrade: Upgrade }) {
  return (
    <Dialog open={upgrade.changesShown} onOpenChange={upgrade.setChangesShown}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Upgrade to {upgrade.version}</DialogTitle>
          <DialogDescription>
            Mobius waits until no agent runs, and then starts the new release.
          </DialogDescription>
        </DialogHeader>
        <ReleaseChanges />
        <DialogFooter>
          <Button disabled={upgrade.upgrading} onClick={upgrade.start}>
            Upgrade
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
