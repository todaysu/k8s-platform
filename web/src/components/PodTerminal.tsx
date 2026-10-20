import { Alert, Button, Card, CardBody, FormSelect, FormSelectOption, Label, Title } from '@patternfly/react-core'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useEffect, useRef, useState } from 'react'

import { fetchPlatformCapabilities } from '../api/clusters'
import './PodTerminal.css'

type Props = { clusterId: string; containers: string[]; namespace: string; podName: string; running: boolean }

export default function PodTerminal({ clusterId, containers, namespace, podName, running }: Props) {
  const host = useRef<HTMLDivElement | null>(null); const socket = useRef<WebSocket | null>(null); const terminal = useRef<Terminal | null>(null); const fit = useRef<FitAddon | null>(null)
  const [container, setContainer] = useState(containers[0] ?? ''); const [shell, setShell] = useState('sh'); const [state, setState] = useState<'idle'|'connecting'|'connected'>('idle'); const [error, setError] = useState('')
  const [execAvailable, setExecAvailable] = useState<boolean | null>(null)
  const [capabilityError, setCapabilityError] = useState('')
  const selectedContainer = containers.includes(container) ? container : containers[0] ?? ''

  useEffect(() => {
    let active = true
    void fetchPlatformCapabilities().then((capabilities) => {
      if (active) setExecAvailable(capabilities.pod_exec_enabled)
    }).catch((requestError) => {
      if (!active) return
      setExecAvailable(false)
      setCapabilityError(requestError instanceof Error ? requestError.message : '无法确认 Pod Exec 状态。')
    })
    return () => { active = false }
  }, [])

  useEffect(() => {
    const terminalHost = host.current
    if (!terminalHost) return
    const nextTerminal = new Terminal({ cursorBlink: true, convertEol: true, fontFamily: 'Red Hat Mono, Menlo, monospace', fontSize: 14, rows: 24, scrollback: 3000, theme: { background: '#151515', foreground: '#f0f0f0', cursor: '#73bcf7', selectionBackground: '#2b9af355' } })
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
  const disconnect = () => { if(socket.current?.readyState===WebSocket.OPEN) socket.current.send(JSON.stringify({type:'close'})); socket.current?.close();socket.current=null;setState('idle') }
  const connect = () => {
    if(!running||!selectedContainer||state!=='idle'||execAvailable!==true)return;setError('');setState('connecting');terminal.current?.clear();terminal.current?.writeln('\x1b[36m正在连接容器终端…\x1b[0m')
    const protocol=window.location.protocol==='https:'?'wss:':'ws:';const params=new URLSearchParams({container:selectedContainer,shell});const next=new WebSocket(`${protocol}//${window.location.host}/api/v1/clusters/${encodeURIComponent(clusterId)}/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/exec?${params}`);next.binaryType='arraybuffer';socket.current=next
    const input=terminal.current?.onData(data=>{if(next.readyState===WebSocket.OPEN)next.send(JSON.stringify({type:'input',data}))});const resize=terminal.current?.onResize(size=>{if(next.readyState===WebSocket.OPEN)next.send(JSON.stringify({type:'resize',cols:size.cols,rows:size.rows}))})
    next.onopen=()=>{setState('connected');terminal.current?.clear();fit.current?.fit();next.send(JSON.stringify({type:'resize',cols:terminal.current?.cols??80,rows:terminal.current?.rows??24}));terminal.current?.focus()}
    next.onmessage=event=>{if(event.data instanceof ArrayBuffer){terminal.current?.write(new Uint8Array(event.data));return}try{const message=JSON.parse(String(event.data)) as {type:string;message?:string};if(message.type==='error'){setError(message.message??'终端执行失败。');terminal.current?.writeln(`\r\n\x1b[31m${message.message??'终端执行失败。'}\x1b[0m`)}if(message.type==='closed')next.close()}catch{terminal.current?.write(String(event.data))}}
    next.onerror=()=>setError('终端连接失败。请确认平台已开启 Pod Exec，且纳管凭据具有 pods/exec 权限。')
    next.onclose=()=>{input?.dispose();resize?.dispose();socket.current=null;setState('idle');terminal.current?.writeln('\r\n\x1b[33m终端连接已关闭。\x1b[0m')}
  }

  return <Card className="ks-pod-terminal-card"><div className="ks-pod-terminal-header"><div><Title headingLevel="h2" size="lg">容器终端</Title></div><Label color={state==='connected'?'green':state==='connecting'?'orange':'grey'}>{state==='connected'?'已连接':state==='connecting'?'连接中':execAvailable===false?'未启用':'未连接'}</Label></div><CardBody>
    <Alert isInline title="高权限操作" variant="warning">终端会直接在容器内执行命令。请先确认集群、命名空间、Pod 和容器均正确，不要粘贴未审查的命令。</Alert>
    {execAvailable===false&&<Alert isInline title="平台未开启 Pod Exec" variant="danger">{capabilityError||'本地开发请重新执行 make dev-backend；Kubernetes 部署请设置 ENABLE_POD_EXEC=true 后重启平台。'}</Alert>}
    {!running&&<Alert isInline title="Pod 不是 Running 状态" variant="danger">只有正在运行的 Pod 才能打开终端。</Alert>}{error&&<Alert isInline title="终端连接失败" variant="danger">{error}</Alert>}
    <div className="ks-pod-terminal-toolbar">
      <div className="ks-pod-terminal-field"><FormSelect aria-label="选择终端容器" isDisabled={state!=='idle'||execAvailable!==true} onChange={(_,value)=>setContainer(value)} value={selectedContainer}>{containers.map(value=><FormSelectOption key={value} label={value} value={value}/>)}</FormSelect></div>
      <div className="ks-pod-terminal-field"><FormSelect aria-label="选择 Shell" isDisabled={state!=='idle'||execAvailable!==true} onChange={(_,value)=>setShell(value)} value={shell}><FormSelectOption label="/bin/sh（推荐）" value="sh"/><FormSelectOption label="/bin/bash" value="bash"/></FormSelect></div>
      {state==='idle'?<Button className="ks-pod-terminal-action" isDisabled={!running||!selectedContainer||execAvailable!==true} isLoading={execAvailable===null} onClick={connect}>连接终端</Button>:<Button className="ks-pod-terminal-action" onClick={disconnect} variant="danger">断开连接</Button>}
      <span className="ks-pod-terminal-target" title={`${namespace} / ${podName}`}>{namespace} / {podName}</span>
    </div>
    <div aria-label="Pod 交互式终端" className="ks-pod-terminal" ref={host}/>
  </CardBody></Card>
}
