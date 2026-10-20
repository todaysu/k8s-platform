import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react'

export type ArthasTerminalHandle = {
  send: (command: string) => boolean
  interrupt: () => void
  stop: () => void
}

type Props = {
  active: boolean
  clusterId: string
  container: string
  namespace: string
  podName: string
  onError: (message: string) => void
  onState: (state: 'idle' | 'connecting' | 'connected') => void
  onAttached: () => void
}

const ArthasTerminal = forwardRef<ArthasTerminalHandle, Props>(function ArthasTerminal({ active, clusterId, container, namespace, podName, onError, onState, onAttached }, ref) {
  const host = useRef<HTMLDivElement | null>(null)
  const socket = useRef<WebSocket | null>(null)
  const terminal = useRef<Terminal | null>(null)
  const fit = useRef<FitAddon | null>(null)
  const [state, setState] = useState<'idle' | 'connecting' | 'connected'>('idle')
  const attachedSent = useRef(false)

  const changeState = (next: 'idle' | 'connecting' | 'connected') => {
    setState(next)
    onState(next)
  }

  useImperativeHandle(ref, () => ({
    send(command: string) {
      if (socket.current?.readyState !== WebSocket.OPEN) return false
      socket.current.send(JSON.stringify({ type: 'input', data: `${command}\r` }))
      terminal.current?.focus()
      return true
    },
    interrupt() {
      if (socket.current?.readyState === WebSocket.OPEN) socket.current.send(JSON.stringify({ type: 'input', data: '\u0003' }))
    },
    stop() {
      if (socket.current?.readyState === WebSocket.OPEN) socket.current.send(JSON.stringify({ type: 'input', data: 'stop\r' }))
      window.setTimeout(() => socket.current?.close(), 800)
    },
  }), [])

  useEffect(() => {
    const terminalHost = host.current
    if (!terminalHost) return
    const nextTerminal = new Terminal({
      cursorBlink: true,
      convertEol: true,
      fontFamily: 'Red Hat Mono, Menlo, monospace',
      fontSize: 13,
      rows: 26,
      scrollback: 5000,
      theme: { background: '#151515', foreground: '#f0f0f0', cursor: '#73bcf7', selectionBackground: '#2b9af355' },
    })
    const nextFit = new FitAddon()
    nextTerminal.loadAddon(nextFit)
    nextTerminal.open(terminalHost)
    terminal.current = nextTerminal
    fit.current = nextFit
    let frame = 0
    let previousWidth = 0
    let previousHeight = 0
    const fitToHost = () => {
      const { height, width } = terminalHost.getBoundingClientRect()
      const nextWidth = Math.round(width)
      const nextHeight = Math.round(height)
      if (nextWidth < 1 || nextHeight < 1 || (nextWidth === previousWidth && nextHeight === previousHeight)) return
      previousWidth = nextWidth
      previousHeight = nextHeight
      window.cancelAnimationFrame(frame)
      frame = window.requestAnimationFrame(() => nextFit.fit())
    }
    const observer = new ResizeObserver(fitToHost)
    observer.observe(terminalHost)
    fitToHost()
    return () => {
      observer.disconnect()
      window.cancelAnimationFrame(frame)
      socket.current?.close()
      nextTerminal.dispose()
      terminal.current = null
      fit.current = null
    }
  }, [])

  useEffect(() => {
    if (!active || !container) {
      socket.current?.close()
      socket.current = null
      changeState('idle')
      return
    }
    const term = terminal.current
    if (!term) return
    attachedSent.current = false
    term.clear()
    changeState('connecting')
    term.writeln('\x1b[36m正在准备 Arthas 会话…\x1b[0m')
    term.writeln('\x1b[90m平台将通过 Kubernetes Exec 传入内置 Arthas 4.3.5 完整包，无需容器联网。\x1b[0m')
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const params = new URLSearchParams({ container, mode: 'arthas' })
    const next = new WebSocket(`${protocol}//${window.location.host}/api/v1/clusters/${encodeURIComponent(clusterId)}/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/exec?${params}`)
    next.binaryType = 'arraybuffer'
    socket.current = next
    const input = term.onData(data => { if (next.readyState === WebSocket.OPEN) next.send(JSON.stringify({ type: 'input', data })) })
    const resize = term.onResize(size => { if (next.readyState === WebSocket.OPEN) next.send(JSON.stringify({ type: 'resize', cols: size.cols, rows: size.rows })) })
    next.onopen = () => {
      changeState('connected')
      term.clear()
      fit.current?.fit()
      next.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
      term.focus()
    }
    next.onmessage = event => {
      if (event.data instanceof ArrayBuffer) {
        const bytes = new Uint8Array(event.data)
        if (!attachedSent.current && /\[arthas@\d+\]/.test(new TextDecoder().decode(bytes))) {
          attachedSent.current = true
          onAttached()
        }
        term.write(bytes)
        return
      }
      try {
        const message = JSON.parse(String(event.data)) as { type: string; message?: string }
        if (message.type === 'error') {
          const value = message.message ?? 'Arthas 会话执行失败。'
          onError(value)
          term.writeln(`\r\n\x1b[31m${value}\x1b[0m`)
        }
        if (message.type === 'closed') next.close()
      } catch { term.write(String(event.data)) }
    }
    next.onerror = () => onError('无法建立 Arthas 终端连接。请检查：平台已开启容器终端、当前账号是管理员，以及开发代理/Ingress 已转发 WebSocket。')
    next.onclose = () => {
      input.dispose(); resize.dispose(); socket.current = null
      changeState('idle')
      term.writeln('\r\n\x1b[33mArthas 会话已关闭。\x1b[0m')
    }
    return () => { input.dispose(); resize.dispose(); next.close(); socket.current = null }
  }, [active, clusterId, container, namespace, podName])

  return <div className="ks-arthas-terminal-wrap">
    <div aria-label="Arthas 交互式终端" className="ks-arthas-terminal" ref={host} />
    {state !== 'connected' && <div className="ks-arthas-terminal-cover">{state === 'connecting' ? '正在连接 Arthas…' : '会话未连接'}</div>}
  </div>
})

export default ArthasTerminal
