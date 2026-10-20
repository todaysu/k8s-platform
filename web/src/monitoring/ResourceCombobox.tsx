import {useEffect,useId,useRef,useState} from 'react'
export default function ResourceCombobox({label,value,options,placeholder,disabled=false,onChange}:{label:string;value:string;options:string[];placeholder:string;disabled?:boolean;onChange:(value:string)=>void}){
 const id=useId(),root=useRef<HTMLDivElement|null>(null),input=useRef<HTMLInputElement|null>(null)
 const [open,setOpen]=useState(false),[filter,setFilter]=useState(''),[active,setActive]=useState(-1)
 const matches=options.filter(option=>option.toLowerCase().includes(filter.toLowerCase()))
 const visible=matches.slice(0,100)
 const choose=(option:string)=>{onChange(option);setFilter('');setOpen(false);setActive(-1);input.current?.focus()}
 useEffect(()=>{if(!open)return;const close=(event:PointerEvent)=>{if(!root.current?.contains(event.target as Node)){setOpen(false);setActive(-1)}};document.addEventListener('pointerdown',close);return()=>document.removeEventListener('pointerdown',close)},[open])
 const reveal=()=>{if(!disabled){setFilter('');setActive(-1);setOpen(true)}}
 return <div ref={root} className="ks-resource-combo"><label htmlFor={id}>{label==='监控命名空间'?'命名空间':'Pod'}</label><div className="ks-resource-combo-control"><input id={id} ref={input} role="combobox" aria-label={label} aria-expanded={open} aria-autocomplete="list" aria-controls={`${id}-options`} aria-activedescendant={open&&active>=0?`${id}-option-${active}`:undefined} autoComplete="off" placeholder={placeholder} disabled={disabled} value={value} onFocus={reveal} onClick={()=>{if(!open)reveal()}} onChange={event=>{onChange(event.target.value);setFilter(event.target.value);setOpen(true);setActive(-1)}} onKeyDown={event=>{
  if(event.key==='Escape'){event.preventDefault();event.stopPropagation();setOpen(false);setActive(-1)}
  if(event.key==='Tab'){setOpen(false);setActive(-1)}
  if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();if(!open){reveal();return};const maximum=visible.length-1;setActive(current=>event.key==='ArrowDown'?Math.min(maximum,current+1):Math.max(-1,current-1))}
  if(event.key==='Enter'&&open){event.preventDefault();if(active>=0)choose(visible[active]);else{setOpen(false);setActive(-1)}}
 }}/><button type="button" aria-label={`展开${label==='监控命名空间'?'命名空间':'Pod'}选项`} disabled={disabled} tabIndex={-1} onPointerDown={event=>event.preventDefault()} onClick={()=>{if(open)setOpen(false);else{input.current?.focus();reveal()}}}><span aria-hidden="true">⌄</span></button></div>
 {open&&!disabled&&<div className="ks-resource-combo-popup"><ul id={`${id}-options`} role="listbox" aria-label={`${label}选项`}><li role="option" aria-selected={!value} onPointerDown={e=>e.preventDefault()} onClick={()=>choose('')}>{placeholder}</li>{visible.map((option,index)=><li id={`${id}-option-${index}`} key={option} role="option" aria-selected={value===option} className={index===active?'is-active':''} onPointerDown={event=>event.preventDefault()} onClick={()=>choose(option)}>{option}</li>)}</ul>{!matches.length&&<p>暂无匹配项，可手动输入名称。</p>}{matches.length>100&&<p>仅显示前 100 项，请输入名称缩小范围。</p>}<p>输入可搜索，也可填写历史资源名称。</p></div>}
 </div>
}
