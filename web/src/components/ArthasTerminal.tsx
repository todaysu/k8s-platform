import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react'

export type ArthasTerminalHandle = { send: (command: string) => boolean; interrupt: () => void; clear: () => void; stop: () => void }
type Props = {
  active: boolean; clusterId: string; container: string; namespace: string; podName: string
  onError: (message: string) => void
  onState: (state: 'idle' | 'connecting' | 'connected') => void
  onAttached: () => void
}

const ArthasTerminal = forwardRef<ArthasTerminalHandle, Props>(function ArthasTerminal({ active, clusterId, container, namespace, podName, onError, onState, onAttached }, ref) {
  const host = useRef<HTMLDivElement | null>(null)
  const socket = useRef<WebSocket | null>(null)
  const terminal = useRef<Terminal | null>(null)
  const attached = useRef(false)
  const callbacks = useRef({ onError, onState, onAttached })
  const [state, setState] = useState<'idle' | 'connecting' | 'connected'>('idle')
  useEffect(() => { callbacks.current = { onError, onState, onAttached } }, [onError, onState, onAttached])

  useImperativeHandle(ref, () => ({
    send(command) {
      const current = socket.current
      if (current?.readyState !== WebSocket.OPEN || !command.trim()) return false
      current.send(JSON.stringify({ type: 'input', data: `${command}\r` }))
      terminal.current?.focus()
      return true
    },
    clear() { terminal.current?.clear() },
    interrupt() {
      const current = socket.current
      if (current?.readyState === WebSocket.OPEN) current.send(JSON.stringify({ type: 'input', data: '\u0003' }))
    },
    stop() {
      // Capture this connection: a delayed stop must never close a later session.
      const current = socket.current
      if (!current) return
      if (current.readyState === WebSocket.OPEN) {
        current.send(JSON.stringify({ type: 'input', data: '\u0003' }))
        if (attached.current) window.setTimeout(() => {
          if (current.readyState === WebSocket.OPEN) current.send(JSON.stringify({ type: 'input', data: 'stop\r' }))
        }, 100)
      }
      window.setTimeout(() => current.close(), 800)
    },
  }), [])

  useEffect(() => {
    const element = host.current
    if (!element || !active || !container) return
    let disposed = false
    let current: WebSocket | null = null
    let frame = 0
    let promptTail = ''
    const decoder = new TextDecoder()
    attached.current = false
    const term = new Terminal({ cursorBlink: true, convertEol: true, fontFamily: 'Red Hat Mono, Menlo, monospace', fontSize: 13, rows: 26, scrollback: 5000,
      theme: { background: '#151f2b', foreground: '#e4edf5', cursor: '#73bcf7', selectionBackground: '#2b9af355' } })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(element)
    terminal.current = term
    const changeState = (next: 'idle' | 'connecting' | 'connected') => {
      if (disposed) return
      setState(next)
      callbacks.current.onState(next)
    }
    changeState('connecting')
    term.writeln('\x1b[36m正在准备 Arthas 会话…\x1b[0m')
    term.writeln('\x1b[90m将通过 Kubernetes Exec 传入内置离线包，无需容器联网。\x1b[0m')
    const fitToHost = () => {
      window.cancelAnimationFrame(frame)
      frame = window.requestAnimationFrame(() => {
        if (!disposed && element.clientWidth > 0 && element.clientHeight > 0) fit.fit()
      })
    }
    const observer = new ResizeObserver(fitToHost)
    observer.observe(element)
    fitToHost()
    const input = term.onData(data => { if (current?.readyState === WebSocket.OPEN) current.send(JSON.stringify({ type: 'input', data })) })
    const resize = term.onResize(size => { if (current?.readyState === WebSocket.OPEN) current.send(JSON.stringify({ type: 'resize', cols: size.cols, rows: size.rows })) })
    // Cancel the StrictMode preflight before it can start a remote JVM process.
    const connectTimer = window.setTimeout(() => {
      if (disposed) return
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const params = new URLSearchParams({ container, mode: 'arthas' })
      const next = new WebSocket(`${protocol}//${window.location.host}/api/v1/clusters/${encodeURIComponent(clusterId)}/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/exec?${params}`)
      current = next
      socket.current = next
      next.binaryType = 'arraybuffer'
      next.onopen = () => {
        if (disposed) { next.close(); return }
        changeState('connected')
        fitToHost()
        next.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
        term.focus()
      }
      next.onmessage = event => {
        if (disposed) return
        if (event.data instanceof ArrayBuffer) {
          const bytes = new Uint8Array(event.data)
          promptTail = (promptTail + decoder.decode(bytes, { stream: true })).slice(-2048)
          if (!attached.current && /\[arthas@\d+\]\$/.test(promptTail.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, ''))) {
            attached.current = true
            callbacks.current.onAttached()
          }
          term.write(bytes)
          return
        }
        try {
          const message = JSON.parse(String(event.data)) as { type: string; message?: string }
          if (message.type === 'error') {
            const value = message.message ?? 'Arthas 会话执行失败。'
            callbacks.current.onError(value)
            term.writeln(`\r\n\x1b[31m${value}\x1b[0m`)
          }
          if (message.type === 'closed') next.close()
        } catch { term.write(String(event.data)) }
      }
      next.onerror = () => { if (!disposed) callbacks.current.onError('无法建立 Arthas 终端连接。请检查平台容器终端权限及 WebSocket 代理配置。') }
      next.onclose = () => {
        if (disposed || socket.current !== next) return
        socket.current = null
        attached.current = false
        changeState('idle')
        term.writeln('\r\n\x1b[33m会话已关闭，可查看以上输出或重新连接。\x1b[0m')
      }
    }, 0)
    return () => {
      disposed = true
      window.clearTimeout(connectTimer)
      window.cancelAnimationFrame(frame)
      observer.disconnect()
      input.dispose()
      resize.dispose()
      if (socket.current === current) socket.current = null
      current?.close()
      if (terminal.current === term) terminal.current = null
      term.dispose()
    }
  }, [active, clusterId, container, namespace, podName])

  return <div className="ks-arthas-terminal-wrap" data-state={state}>
    <div aria-label="Arthas 交互式终端" className="ks-arthas-terminal"><div className="ks-arthas-terminal-view" ref={host} /></div>
    {state === 'connecting' && <div className="ks-arthas-terminal-status" role="status">正在建立连接…</div>}
  </div>
})
export default ArthasTerminal
