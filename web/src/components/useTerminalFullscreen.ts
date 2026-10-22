import { useEffect, useLayoutEffect, useRef } from 'react'

const FULLSCREEN_CLASS = 'ks-terminal-fullscreen'
let layers = 0
let previousOverflow = ''

export function useTerminalFullscreen(active: boolean, onExit: () => void) {
  const exit = useRef(onExit)
  useEffect(() => {
    exit.current = onExit
  }, [onExit])

  useLayoutEffect(() => {
    if (!active) return
    layers += 1
    if (layers === 1) {
      previousOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      document.body.classList.add(FULLSCREEN_CLASS)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      exit.current()
    }
    window.addEventListener('keydown', onKeyDown, true)
    return () => {
      window.removeEventListener('keydown', onKeyDown, true)
      layers -= 1
      if (layers === 0) {
        document.body.style.overflow = previousOverflow
        document.body.classList.remove(FULLSCREEN_CLASS)
      }
    }
  }, [active])
}
