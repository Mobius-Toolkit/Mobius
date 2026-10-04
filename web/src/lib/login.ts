import { createContext } from 'react'

// LoginContext gives the function that shows the login page.
export const LoginContext = createContext<() => void>(() => {})

// The API answers 401 to each request with no device login, but the spec has
// no 401 response, so the generated status types do not include it.
export const unauthorized = (res: { status: number }) => res.status === 401
