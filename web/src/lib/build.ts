import { useEffect, useState } from 'react'

declare const UI_BUILD: string

async function newBuild() {
  // The Vite dev server has no ui-version.
  if (import.meta.env.DEV) {
    return false
  }
  try {
    const response = await fetch('/ui-version', { cache: 'no-store' })
    return response.ok && (await response.text()) !== UI_BUILD
  } catch {
    return false
  }
}

export function useNewBuild() {
  const [found, setFound] = useState(false)
  useEffect(() => {
    const check = () => {
      void newBuild().then((differs) => setFound((shown) => shown || differs))
    }
    const shown = () => {
      if (!document.hidden) {
        check()
      }
    }
    check()
    window.addEventListener('focus', check)
    document.addEventListener('visibilitychange', shown)
    const timer = setInterval(check, 5 * 60 * 1000)
    return () => {
      window.removeEventListener('focus', check)
      document.removeEventListener('visibilitychange', shown)
      clearInterval(timer)
    }
  }, [])
  return found
}

// The old server answers until it restarts, so only a new build ends the wait.
export async function reloadOnNewBuild() {
  while (!(await newBuild())) {
    await new Promise((resolve) => setTimeout(resolve, 1000))
  }
  window.location.reload()
}
