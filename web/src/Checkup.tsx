import { use, useCallback, useEffect, useState } from 'react'
import {
  fixLabels,
  getCheckup,
  type Checkup as CheckupView,
  type LabelCheck,
  type PermissionCheck,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { LoginContext } from '@/lib/login'
import { BackButton } from './BackButton'

const fixButton = { create: 'Create labels', fix: 'Fix labels' }

function LabelStatus({ label }: { label: LabelCheck }) {
  switch (label.status) {
    case 'present':
      return <Badge variant="secondary">present</Badge>
    case 'wrong-color':
      return <Badge variant="destructive">wrong color: #{label.found}</Badge>
    case 'wrong-case':
      return <Badge variant="destructive">wrong case: {label.found}</Badge>
    case 'missing':
      return <Badge variant="destructive">missing</Badge>
  }
}

function PermissionStatus({ permission }: { permission: PermissionCheck }) {
  switch (permission.status) {
    case 'present':
      return <Badge variant="secondary">present</Badge>
    case 'not-accepted':
      return (
        <Badge variant="destructive" asChild>
          <a href={permission.url} target="_blank" rel="noreferrer">
            not accepted: accept on GitHub
          </a>
        </Badge>
      )
    case 'missing':
      return (
        <Badge variant="destructive" asChild>
          <a href={permission.url} target="_blank" rel="noreferrer">
            missing: add on GitHub
          </a>
        </Badge>
      )
  }
}

export function Checkup({ organization }: { organization: string }) {
  const showLogin = use(LoginContext)
  const [checkup, setCheckup] = useState<CheckupView>()
  const [error, setError] = useState<string>()
  const [fixing, setFixing] = useState(false)

  const load = useCallback(() => {
    if (!organization) {
      return
    }
    getCheckup({ organization })
      .then((res) => {
        if (res.status === 401) {
          showLogin()
        } else if (res.status === 200) {
          setCheckup(res.data.data)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [organization, showLogin])

  useEffect(load, [load])

  const fix = () => {
    setFixing(true)
    fixLabels({ organization })
      .then((res) => {
        if (res.status === 401) {
          showLogin()
        } else if (res.status === 204) {
          setError(undefined)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
      // The fix can change labels before it fails, so the status loads again.
      .finally(() => {
        load()
        setFixing(false)
      })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <BackButton parent="/settings" />
          Checkup
        </CardTitle>
        {checkup && checkup.labelFix !== 'none' && (
          <CardAction>
            <Button disabled={fixing} onClick={fix}>
              {fixButton[checkup.labelFix]}
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="grid gap-6">
        {error && <Badge variant="destructive">{error}</Badge>}
        {(!organization || checkup?.repositories.length === 0) && (
          <p className="text-muted-foreground">
            The Mobius App has no repository in this organization.
          </p>
        )}
        {checkup?.permissionsError && (
          <section className="grid gap-2">
            <h3 className="font-medium">App permissions</h3>
            <Badge variant="destructive">{checkup.permissionsError}</Badge>
          </section>
        )}
        {checkup && checkup.permissions.length > 0 && (
          <section className="grid gap-2">
            <h3 className="font-medium">App permissions</h3>
            <ul className="divide-y">
              {checkup.permissions.map((permission) => (
                <li
                  key={permission.name}
                  className="flex items-center justify-between gap-4 py-2"
                >
                  <span>
                    {permission.name}: {permission.level}
                  </span>
                  <PermissionStatus permission={permission} />
                </li>
              ))}
            </ul>
          </section>
        )}
        {checkup?.repositories.map((repository) => (
          <section key={repository.repository} className="grid gap-2">
            <h3 className="font-medium">{repository.repository}</h3>
            <ul className="divide-y">
              {repository.labels.map((label) => (
                <li
                  key={label.name}
                  className="flex items-center justify-between gap-4 py-2"
                >
                  <span className="flex items-center gap-2">
                    <span
                      className="size-3 rounded-full"
                      style={{ background: `#${label.color}` }}
                    />
                    {label.name}
                  </span>
                  <LabelStatus label={label} />
                </li>
              ))}
            </ul>
          </section>
        ))}
      </CardContent>
    </Card>
  )
}
