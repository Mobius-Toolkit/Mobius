import { useEffect, useState } from 'react'
import { client } from '@/api/client'
import type { components } from '@/api/schema'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'

type Health = components['schemas']['Health']

function App() {
  const [health, setHealth] = useState<Health>()
  const [error, setError] = useState<string>()

  useEffect(() => {
    client
      .GET('/api/health')
      .then((res) => {
        if (res.error) {
          setError(res.error.error)
        } else {
          setHealth(res.data)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [])

  return (
    <main className="flex min-h-svh items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Mobius</CardTitle>
          <CardDescription>Server health</CardDescription>
        </CardHeader>
        <CardContent>
          {error && <Badge variant="destructive">{error}</Badge>}
          {!error && !health && (
            <p className="text-sm text-muted-foreground">Loading</p>
          )}
          {health && (
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
              <dt className="text-muted-foreground">Status</dt>
              <dd>
                <Badge>{health.status}</Badge>
              </dd>
              <dt className="text-muted-foreground">Time</dt>
              <dd>{new Date(health.time).toLocaleString()}</dd>
            </dl>
          )}
        </CardContent>
      </Card>
    </main>
  )
}

export default App
