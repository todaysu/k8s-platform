export type MonitoringPanel={id:string;title:string;unit:string;hint:string;status:'available'|'missing'|'error';message?:string;series:{labels:Record<string,string>;points:{time:number;value:number|null}[]}[]}
export type MonitoringSnapshot={source:string;start:string;end:string;step:number;collected_at:string;panels:MonitoringPanel[]}
export const metricValue=(value:number|null,unit:string)=>{if(value===null||!Number.isFinite(value))return '—';if(unit==='bytes/s'){const scale=value>=1073741824?1073741824:value>=1048576?1048576:value>=1024?1024:1;const label=scale===1073741824?'GiB/s':scale===1048576?'MiB/s':scale===1024?'KiB/s':'B/s';return `${(value/scale).toFixed(value===0?0:2)} ${label}`};if(unit==='bytes'){const scale=value>=1073741824?1073741824:1048576;return `${(value/scale).toFixed(2)} ${scale===1073741824?'GiB':'MiB'}`};if(unit==='cores')return value<1?`${(value*1000).toFixed(1)} m`:`${value.toFixed(2)} 核`;if(unit==='requests')return `${value.toFixed(0)} 个`;if(unit==='s')return `${(value*1000).toFixed(1)} ms`;return `${value.toFixed(value===0?0:2)}${unit==='%'?'%':unit==='state'?'':` ${unit}`}`}
export const seriesName=(labels:Record<string,string>)=>{
 const kinds:Record<string,string>={readOnly:'只读请求',mutating:'写入请求'}
 const kind=labels.request_kind?(kinds[labels.request_kind]||labels.request_kind):''
 const resource=labels.pod?`${labels.namespace}/${labels.pod}`:Object.entries(labels).filter(([key])=>key!=='__name__'&&key!=='request_kind').map(([key,value])=>`${key}=${value}`).join(' · ')
 return `${kind?`${kind} · `:''}${resource||'集群合计'}`
}
