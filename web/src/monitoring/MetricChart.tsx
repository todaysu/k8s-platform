import {useEffect,useRef,useState} from 'react'
import {createPortal} from 'react-dom'
import {useFullscreenPanel} from './useFullscreenPanel'
import {metricValue,seriesName,type MonitoringPanel} from './types'
const colors=['#3983c6','#41a987','#c79436','#9171c5','#d66e6e','#5aa9bc','#9a9b43','#c479aa']
export default function MetricChart({panel,start,end,step,onZoom}:{panel:MonitoringPanel;start:number;end:number;step:number;onZoom?:(start:number,end:number)=>void}){
 const [cursor,setCursor]=useState<number|null>(null),[hidden,setHidden]=useState<string[]>([])
 const [fullscreen,setFullscreen]=useState(false),[drag,setDrag]=useState<number|null>(null)
 const root=useRef<HTMLElement|null>(null),svg=useRef<SVGSVGElement|null>(null)
 const [size,setSize]=useState({width:640,height:190})
 useEffect(()=>{const node=svg.current;if(!node)return
  const measure=()=>{const rect=node.getBoundingClientRect();if(rect.width>0&&rect.height>0){const width=Math.round(rect.width),height=Math.round(rect.height);setSize(previous=>previous.width===width&&previous.height===height?previous:{width,height})}}
  const observer=typeof ResizeObserver!=='undefined'?new ResizeObserver(measure):null;observer?.observe(node)
  const frame=requestAnimationFrame(measure);window.addEventListener('resize',measure)
  return()=>{observer?.disconnect();cancelAnimationFrame(frame);window.removeEventListener('resize',measure)}
 },[fullscreen,panel.status])
 const left=76,right=size.width-14,top=18,bottom=size.height-32,plotWidth=Math.max(10,right-left),plotHeight=Math.max(10,bottom-top)
 useFullscreenPanel(fullscreen,()=>setFullscreen(false),root)
 const fraction=(event:React.PointerEvent<SVGSVGElement>)=>{const bounds=event.currentTarget.getBoundingClientRect();return Math.max(0,Math.min(1,(event.clientX-bounds.left-left)/Math.max(10,bounds.width-left-14)))}
 const lines=panel.series.filter(s=>!hidden.includes(seriesName(s.labels)))
 const max=Math.max(1e-9,...lines.flatMap(s=>s.points.filter(p=>p.value!==null).map(p=>p.value!)))*1.1
 const ceiling=max<1e-8?1:max
 const x=(time:number)=>left+Math.max(0,Math.min(1,(time-start)/(end-start)))*plotWidth
 const y=(value:number)=>bottom-Math.min(1,Math.max(0,value/ceiling))*plotHeight
 const path=(points:MonitoringPanel['series'][number]['points'])=>{let previous:number|null=null;return points.map(point=>{if(point.value===null){previous=null;return ''};const command=previous===null||point.time-previous>step*1.8?'M':'L';previous=point.time;return `${command}${x(point.time).toFixed(2)},${y(point.value).toFixed(2)}`}).join(' ')}
 const selected=cursor===null?null:start+(end-start)*cursor
 const content=<section ref={root} className={`ks-metric-panel${fullscreen?' is-fullscreen':''}`} role={fullscreen?'dialog':undefined} aria-modal={fullscreen||undefined} aria-label={fullscreen?`${panel.title}全屏`:undefined}><header><h3>{panel.title}</h3><span className={`ks-metric-state is-${panel.status}`}>{panel.status==='available'?`${panel.series.length} 条曲线`:panel.status==='missing'?'无有效样本':'查询失败'}</span><button className="ks-metric-expand" onClick={()=>setFullscreen(value=>!value)} aria-label={`${fullscreen?'退出全屏':'全屏'} ${panel.title}`}>{fullscreen?'退出全屏':'全屏'}</button></header>
 {panel.status!=='available'?<div className="ks-metric-empty"><strong>{panel.status==='missing'?'数据不足，无法判断健康':'暂时无法查询'}</strong><p>{panel.message}</p></div>:<><div className="ks-metric-plot"><svg ref={svg} viewBox={`0 0 ${size.width} ${size.height}`} preserveAspectRatio="xMidYMid meet" role="img" aria-label={`${panel.title}历史趋势`} onPointerDown={event=>{if(event.button!==0||!onZoom)return;setDrag(fraction(event));event.currentTarget.setPointerCapture?.(event.pointerId)}} onPointerMove={event=>setCursor(fraction(event))} onPointerUp={event=>{if(drag!==null&&onZoom){const current=fraction(event);if(Math.abs(current-drag)>.015){const left=start+(end-start)*Math.min(drag,current),right=start+(end-start)*Math.max(drag,current);if(right-left>=30)onZoom(left,right)}}setDrag(null);if(event.currentTarget.hasPointerCapture?.(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId)}} onPointerCancel={()=>setDrag(null)} onPointerLeave={()=>{if(drag===null)setCursor(null)}}>
 {[0,.5,1].map(fraction=><g key={fraction}><line x1={left} x2={right} y1={y(ceiling*fraction)} y2={y(ceiling*fraction)} stroke="#344353" strokeDasharray="3 5"/><text x={left-8} y={y(ceiling*fraction)+4} textAnchor="end" fill="#91a5b9" fontSize="11">{panel.unit==='requests'||panel.unit==='state'?String(Number((ceiling*fraction).toFixed(1))):['IOPS','changes/s','proposals/s'].includes(panel.unit)?(ceiling*fraction).toFixed(2):metricValue(ceiling*fraction,panel.unit)}</text></g>)}
 {panel.series.map((s,i)=>hidden.includes(seriesName(s.labels))?null:<path key={seriesName(s.labels)} d={path(s.points)} stroke={colors[i%colors.length]} strokeWidth="2" fill="none"><title>{seriesName(s.labels)}</title></path>)}
 {(size.width<400?[0,1]:[0,.5,1]).map(fraction=><text key={fraction} x={left+plotWidth*fraction} y={size.height-8} textAnchor={fraction===0?'start':fraction===1?'end':'middle'} fill="#91a5b9" fontSize="10">{new Date((start+(end-start)*fraction)*1000).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false})}</text>)}
 {drag!==null&&cursor!==null&&<rect x={x(start+(end-start)*Math.min(drag,cursor))} y="24" width={Math.abs(cursor-drag)*plotWidth} height="196" fill="#6aabdf" fillOpacity=".2"/>}
 {selected!==null&&<line x1={x(selected)} x2={x(selected)} y1={top} y2={bottom} stroke="#afc7df" strokeDasharray="3 3"/>}</svg></div>
 <label className="ks-metric-time-scrubber">{selected===null?(onZoom?'悬停取值 · 拖选时间段放大':'悬停曲线查看采样值'):new Date(selected*1000).toLocaleString('zh-CN',{hour12:false})}<input aria-label={`${panel.title}采样时间`} type="range" min="0" max="1000" value={Math.round((cursor??1)*1000)} onChange={e=>setCursor(Number(e.target.value)/1000)}/></label>
 <div className="ks-metric-legends">{panel.series.map((s,i)=>{const name=seriesName(s.labels);const point=selected===null?s.points.at(-1):s.points.reduce< typeof s.points[number]|undefined>((nearest,p)=>!nearest||Math.abs(p.time-selected)<Math.abs(nearest.time-selected)?p:nearest,undefined);const value=point&&(selected===null||Math.abs(point.time-selected)<=step*1.8)?point.value:null;return <button key={name} aria-pressed={!hidden.includes(name)} title={`${name}；点击显示或隐藏曲线`} onClick={()=>setHidden(current=>current.includes(name)?current.filter(n=>n!==name):[...current,name])}><i style={{background:colors[i%colors.length]}}/><span>{name}</span><strong>{metricValue(value??null,panel.unit)}</strong></button>})}</div></>}
 <footer>{panel.hint}</footer></section>
 return fullscreen?createPortal(content,document.body):content
}
