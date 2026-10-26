import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import KubectlTTY from './KubectlTTY'
const mocks=vi.hoisted(()=>({input:null as null|((data:string)=>void),write:vi.fn(),dispose:vi.fn(),opened:null as null|HTMLElement}))
vi.mock('@xterm/xterm',()=>({Terminal:class {cols=100;rows=30;loadAddon(){}open(element:HTMLElement){mocks.opened=element}focus(){}clear(){}write=mocks.write;writeln=mocks.write;dispose=mocks.dispose;onData(callback:(data:string)=>void){mocks.input=callback;return{dispose(){}}}onResize(){return{dispose(){}}}}}))
vi.mock('@xterm/addon-fit',()=>({FitAddon:class{fit(){}}}))
class Socket {
 static OPEN=1;static instances:Socket[]=[];readyState=1;binaryType='';url:string
 onopen:null|(()=>void)=null;onmessage:null|((event:{data:string|ArrayBuffer})=>void)=null;onclose:null|(()=>void)=null
 send=vi.fn();close=vi.fn(()=>this.onclose?.())
 constructor(url:string){this.url=url;Socket.instances.push(this)}
}
afterEach(()=>{vi.unstubAllGlobals();Socket.instances=[];vi.clearAllMocks()})
it('keeps its socket for multiple commands and sends raw input to the running PTY',()=>{
 vi.stubGlobal('WebSocket',Socket);vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 const view=render(<KubectlTTY clusterId="a" clusterName="test01"/>);const socket=Socket.instances[0]
 expect(socket.url).toContain('/clusters/a/kubectl/tty')
 act(()=>{socket.onopen?.();socket.onmessage?.({data:JSON.stringify({type:'ready'})});mocks.input?.('kubectl exec -it web -- sh');mocks.input?.('\r');mocks.input?.('ls\r')})
 expect(socket.send).toHaveBeenCalledWith(JSON.stringify({type:'run',args:['exec','-it','web','--','sh']}))
 expect(socket.send).toHaveBeenCalledWith(JSON.stringify({type:'input',data:'ls\r'}))
 act(()=>{socket.onmessage?.({data:JSON.stringify({type:'ready'})});mocks.input?.('kubectl get nodes');mocks.input?.('\r')})
 expect(socket.send).toHaveBeenCalledWith(JSON.stringify({type:'run',args:['get','nodes']}));expect(Socket.instances).toHaveLength(1)
 view.unmount();expect(socket.close).toHaveBeenCalledOnce()
})
it('mounts xterm into a padding-free inner view so the bottom row is not clipped',()=>{
 vi.stubGlobal('WebSocket',Socket);vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<KubectlTTY clusterId="a" clusterName="test01"/>)
 // FitAddon 只按宿主的 border-box 高度换算行数，宿主带 padding 会多算行数，导致终端底部被裁掉
 expect(mocks.opened?.className).toContain('ks-kubectl-tty-view')
 expect(mocks.opened?.parentElement?.className).toContain('ks-kubectl-tty')
})
it('closes the old socket before reconnecting',async()=>{
 vi.stubGlobal('WebSocket',Socket);vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<KubectlTTY clusterId="a" clusterName="test01"/>);const first=Socket.instances[0]
 act(()=>first.onopen?.());await userEvent.click(screen.getByRole('button',{name:'重新连接'}));expect(first.close).toHaveBeenCalledOnce();expect(Socket.instances).toHaveLength(2)
})
