import { use, useCallback, useEffect, useState } from 'react'
import {
  listDevices,
  logout,
  type Devices as DeviceList,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { LoginContext, unauthorized } from '@/lib/login'

export function Devices() {
  const showLogin = use(LoginContext)
  const [devices, setDevices] = useState<DeviceList>()
  const [error, setError] = useState<string>()

  const load = useCallback(() => {
    listDevices()
      .then((res) => {
        if (unauthorized(res)) {
          showLogin()
        } else if (res.status === 200) {
          setDevices(res.data.data)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [showLogin])

  useEffect(load, [load])

  const logOut = (id: number) => {
    logout(id)
      .then((res) => {
        if (unauthorized(res)) {
          showLogin()
        } else if (res.status === 204) {
          load()
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Devices</CardTitle>
      </CardHeader>
      <CardContent>
        {error && <Badge variant="destructive">{error}</Badge>}
        <ul className="divide-y">
          {devices?.logins.map((login) => (
            <li
              key={login.id}
              className="flex items-center justify-between gap-4 py-2"
            >
              <span className="grid gap-1">
                <span>{login.userAgent}</span>
                <span className="text-muted-foreground">
                  Logged in {new Date(login.createdAt).toLocaleString()}
                </span>
              </span>
              <span className="flex items-center gap-2">
                {login.id === devices.thisDevice && (
                  <Badge variant="secondary">This device</Badge>
                )}
                <Button variant="outline" onClick={() => logOut(login.id)}>
                  Log out
                </Button>
              </span>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
