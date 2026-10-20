import {useEffect,useRef,type RefObject} from 'react'
const layers:symbol[]=[]
let previousOverflow=''
export function useFullscreenPanel(active:boolean,onExit:()=>void,root:RefObject<HTMLElement|null>){
 const exit=useRef(onExit)
 useEffect(()=>{exit.current=onExit},[onExit])
 useEffect(()=>{
  if(!active)return
  const token=Symbol('fullscreen');layers.push(token)
  if(layers.length===1){previousOverflow=document.body.style.overflow;document.body.style.overflow='hidden'}
  const previous=document.activeElement as HTMLElement|null
  const frame=requestAnimationFrame(()=>root.current?.querySelector<HTMLElement>('button')?.focus())
  const key=(event:KeyboardEvent)=>{if(layers.at(-1)!==token)return
   if(event.key==='Escape'){event.preventDefault();event.stopPropagation();exit.current()}
   if(event.key==='Tab'){const elements=root.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select:not(:disabled),[tabindex="0"]');if(!elements?.length)return;const first=elements[0],last=elements[elements.length-1];if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus()}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}}
  }
  document.addEventListener('keydown',key)
  return()=>{cancelAnimationFrame(frame);document.removeEventListener('keydown',key);const index=layers.indexOf(token);if(index>=0)layers.splice(index,1);if(!layers.length)document.body.style.overflow=previousOverflow;previous?.focus()}
 },[active,root])
}
