import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { Alert, Button } from '@patternfly/react-core'
import '@xterm/xterm/css/xterm.css'
import { parseKubectlCommand } from './kubectlCommand'

export default function KubectlTTY({clusterId,clusterName,fullscreen=false,onToggleFullscreen}:{clusterId:string;clusterName:string;fullscreen?:boolean;onToggleFullscreen?:()=>void}) {
 const host=useRef<HTMLDivElement>(null)
 const term=useRef<Terminal|null>(null)
 const socket=useRef<WebSocket|null>(null)
 const [attempt,setAttempt]=useState(0)
 const [state,setState]=useState('连接中')
 const [error,setError]=useState('')
 useEffect(()=>{
  const terminal=new Terminal({cursorBlink:true,convertEol:true,scrollback:5000,fontSize:14,fontFamily:'Menlo,monospace',theme:{background:'#17222d',foreground:'#dce5ee'}})
  const fit=new FitAddon();terminal.loadAddon(fit);terminal.open(host.current!);term.current=terminal
  const observer=new ResizeObserver(()=>{if(host.current?.clientWidth&&host.current?.clientHeight)fit.fit()});observer.observe(host.current!)
  const protocol=location.protocol==='https:'?'wss:':'ws:'
  const ws=new WebSocket(`${protocol}//${location.host}/api/v1/clusters/${encodeURIComponent(clusterId)}/kubectl/tty`);ws.binaryType='arraybuffer';socket.current=ws
  let line='',running=false,ready=false,historyIndex=-1;const history:string[]=[]
  const send=(message:object)=>{if(ws.readyState===WebSocket.OPEN)ws.send(JSON.stringify(message))}
  const prompt=()=>terminal.write(`\r\n\x1b[36m${clusterName} $ \x1b[0m`)
  const redraw=()=>terminal.write(`\r\x1b[2K\x1b[36m${clusterName} $ \x1b[0m${line}`)
  const input=terminal.onData(data=>{
   if(!ready)return
   if(running){send({type:'input',data});return}
   if(data==='\r'){
    terminal.write('\r\n');const text=line.trim();line='';historyIndex=-1
    if(!text){prompt();return}
    if(text==='clear'){terminal.clear();prompt();return}
    if(text==='exit'){ws.close();return}
    try{const args=parseKubectlCommand(text);history.unshift(text);history.splice(50);running=true;send({type:'run',args});send({type:'resize',cols:terminal.cols,rows:terminal.rows})}catch(cause){terminal.writeln(String((cause as Error).message));prompt()}
   }else if(data==='\x03'){line='';terminal.write('^C');prompt()}
   else if(data==='\x0c'){terminal.clear();redraw()}
   else if(data==='\x7f'){line=line.slice(0,-1);redraw()}
   else if(data==='\x1b[A'||data==='\x1b[B'){historyIndex=data==='\x1b[A'?Math.min(history.length-1,historyIndex+1):Math.max(-1,historyIndex-1);line=history[historyIndex]||'';redraw()}
   else if(!data.includes('\x1b')&&!/[\r\n]/.test(data)){line+=Array.from(data).filter(char=>char.charCodeAt(0)>=32).join('');redraw()}
  })
  const resize=terminal.onResize(size=>send({type:'resize',cols:size.cols,rows:size.rows}))
  ws.onopen=()=>{setState('已连接');setError('');fit.fit();terminal.focus()}
  ws.onmessage=event=>{
   if(event.data instanceof ArrayBuffer){terminal.write(new Uint8Array(event.data));return}
   const message=JSON.parse(String(event.data))
   if(message.type==='ready'){ready=true;running=false;prompt()}
   if(message.type==='error')terminal.writeln(`\r\n\x1b[31m${message.message}\x1b[0m`)
  }
  ws.onerror=()=>setError('终端连接失败，请检查登录状态、服务端 kubectl 和集群连接。')
  ws.onclose=()=>{ready=false;setState('已断开');terminal.writeln('\r\n连接已关闭。重新连接将创建新会话。')}
  return()=>{ws.onclose=null;ws.onerror=null;ws.onmessage=null;ws.close();observer.disconnect();input.dispose();resize.dispose();terminal.dispose();term.current=null}
 },[clusterId,clusterName,attempt])
 return <><div className="ks-kubectl-toolbar">
  <div className="ks-kubectl-identity"><span className="ks-kubectl-mark" aria-hidden="true">›_</span><h2>kubectl 终端</h2><span className="ks-kubectl-cluster" title={clusterName}>{clusterName}</span><span className={`ks-kubectl-status ${state==='已连接'?'is-connected':state==='连接中'?'is-connecting':''}`} role="status"><i aria-hidden="true"/>{state}</span></div>
  <div className="ks-kubectl-controls"><Button size="sm" variant="plain" onClick={()=>term.current?.clear()}>清屏</Button><Button size="sm" variant="plain" isDisabled={state==='连接中'} onClick={()=>{setState('连接中');setAttempt(value=>value+1)}}>重新连接</Button><Button size="sm" variant="plain" className="ks-kubectl-disconnect" isDisabled={state==='已断开'} onClick={()=>socket.current?.close()}>断开连接</Button>{onToggleFullscreen&&<Button size="sm" variant="secondary" onClick={onToggleFullscreen}>{fullscreen?'退出全屏':'全屏'}</Button>}</div>
 </div>{error&&<div className="ks-kubectl-error"><Alert isInline variant="danger" title={error}/></div>}<div ref={host} className="ks-kubectl-tty" aria-label="kubectl TTY 交互式终端"/><div className="ks-kubectl-footer"><span>输入 kubectl 命令，按 Enter 执行</span><span>Ctrl+C 中断<span aria-hidden="true"> · </span>↑↓ 历史命令</span></div></>
}
