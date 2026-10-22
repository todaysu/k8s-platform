import { StrictMode, createRef } from 'react'
import { act, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ArthasTerminal, { type ArthasTerminalHandle } from './ArthasTerminal'

vi.mock('@xterm/xterm', () => ({Terminal: class {
  cols=100; rows=28
  loadAddon(){} open(){} focus(){} dispose(){} write(){} writeln(){}
  onData(){return {dispose(){}}} onResize(){return {dispose(){}}}
}}))
vi.mock('@xterm/addon-fit', () => ({FitAddon: class {fit(){}}}))
class Socket {
  static OPEN=1
  static instances: Socket[]=[]
  readyState=0
  binaryType=''
  onopen: (()=>void)|null=null
  onclose: (()=>void)|null=null
  onmessage: ((event:{data:unknown})=>void)|null=null
  onerror: (()=>void)|null=null
  send=vi.fn()
  close=vi.fn(()=>{this.readyState=3;this.onclose?.()})
  constructor(){Socket.instances.push(this)}
  open(){this.readyState=1;this.onopen?.()}
  output(text:string){const bytes=new TextEncoder().encode(text);const data=new ArrayBuffer(bytes.length);new Uint8Array(data).set(bytes);this.onmessage?.({data})}
}
beforeEach(()=>{
  vi.useFakeTimers();Socket.instances=[]
  vi.stubGlobal('WebSocket',Socket)
  vi.stubGlobal('ResizeObserver',class {observe(){} disconnect(){}})
})
afterEach(()=>{vi.clearAllTimers();vi.useRealTimers();vi.unstubAllGlobals()})
const defaults={active:true,clusterId:'c1',namespace:'myapp',podName:'java-a',container:'java'}
describe('Arthas terminal lifecycle',()=>{
  it('opens only one remote session in StrictMode and detects a prompt split across frames',()=>{
    const attached=vi.fn(), state=vi.fn(), ref=createRef<ArthasTerminalHandle>()
    render(<StrictMode><ArthasTerminal {...defaults} onAttached={attached} onState={state} onError={vi.fn()} ref={ref}/></StrictMode>)
    act(()=>vi.advanceTimersByTime(1))
    expect(Socket.instances).toHaveLength(1)
    const socket=Socket.instances[0]
    act(()=>{socket.open();socket.output('[arth');socket.output('as@1]$ ')})
    expect(attached).toHaveBeenCalledTimes(1)
    expect(ref.current?.send('memory')).toBe(true)
    expect(socket.send).toHaveBeenLastCalledWith(JSON.stringify({type:'input',data:'memory\r'}))
  })
  it('ignores old close events and delayed stop timers after replacing a connection',()=>{
    const ref=createRef<ArthasTerminalHandle>(),state=vi.fn()
    const props={...defaults,onAttached:vi.fn(),onState:state,onError:vi.fn()}
    const view=render(<ArthasTerminal {...props} ref={ref}/>)
    act(()=>vi.advanceTimersByTime(1))
    const old=Socket.instances[0]
    act(()=>{old.open();old.output('[arthas@1]$ ')})
    const staleClose=old.onclose
    act(()=>ref.current?.stop())
    view.rerender(<ArthasTerminal {...props} podName="java-b" ref={ref}/>)
    act(()=>vi.advanceTimersByTime(1))
    const next=Socket.instances[1]
    act(()=>{next.open();next.output('[arthas@2]$ ');staleClose?.();vi.advanceTimersByTime(1000)})
    expect(state).toHaveBeenLastCalledWith('connected')
    expect(next.close).not.toHaveBeenCalled()
    expect(ref.current?.send('jvm')).toBe(true)
  })
})
