import { Alert, Card } from '@patternfly/react-core'
import { useState } from 'react'
import { usePermission } from '../auth/context'
import KubectlTTY from './KubectlTTY'
import { useTerminalFullscreen } from './useTerminalFullscreen'
import './ClusterKubectl.css'
type Props = { clusterId: string; clusterName: string }
export default function ClusterKubectl({ clusterId, clusterName }: Props) {
 const allowed=usePermission('configure')
 const [fullscreen,setFullscreen]=useState(false)
 useTerminalFullscreen(fullscreen,()=>setFullscreen(false))
 if(!allowed)return <Alert isInline variant="info" title="集群管理终端仅管理员可用"/>
 return <Card className={`ks-cluster-kubectl${fullscreen?' is-fullscreen':''}`} role={fullscreen?'dialog':undefined} aria-modal={fullscreen||undefined} aria-label={fullscreen?`全屏 kubectl 终端 · ${clusterName}`:undefined}><KubectlTTY clusterId={clusterId} clusterName={clusterName} fullscreen={fullscreen} onToggleFullscreen={()=>setFullscreen(value=>!value)}/></Card>
}
