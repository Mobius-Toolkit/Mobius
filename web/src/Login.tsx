import { useState, type FormEvent } from 'react'
import { login } from '@/api/api.gen'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'

export function Login({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    login({ password })
      .then((res) => {
        if (res.status === 204) {
          onLogin()
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setBusy(false))
  }

  return (
    <main className="flex min-h-svh items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Mobius</CardTitle>
          <CardDescription>Log in with the access password.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit}>
            <FieldGroup>
              <Field data-invalid={error !== undefined}>
                <FieldLabel htmlFor="password">Access password</FieldLabel>
                <Input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  autoFocus
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  aria-invalid={error !== undefined}
                />
                <FieldError>{error}</FieldError>
              </Field>
              <Button type="submit" disabled={busy}>
                Log in
              </Button>
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
