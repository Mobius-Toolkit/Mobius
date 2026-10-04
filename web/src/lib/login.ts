import { createContext } from 'react'

// LoginContext gives the function that shows the login page.
export const LoginContext = createContext<() => void>(() => {})
