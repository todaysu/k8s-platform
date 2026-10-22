import { useEffect, useId, useRef, useState } from 'react'
import type { ClusterPod } from '../api/clusters'

type Props = {
  disabled: boolean
  loading: boolean
  namespace: string
  pods: ClusterPod[]
  value: string
  onChange: (value: string) => void
}

const keyOf = (pod: ClusterPod) => `${pod.namespace}/${pod.name}`

export default function ArthasPodPicker({ disabled, loading, namespace, pods, value, onChange }: Props) {
  const id = useId()
  const root = useRef<HTMLDivElement | null>(null)
  const popover = useRef<HTMLDivElement | null>(null)
  const list = useRef<HTMLUListElement | null>(null)
  const trigger = useRef<HTMLButtonElement | null>(null)
  const search = useRef<HTMLInputElement | null>(null)
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(-1)
  const selected = pods.find(pod => keyOf(pod) === value)
  const matches = pods.filter(pod => pod.name.toLowerCase().includes(query.trim().toLowerCase()))
  const visible = matches.slice(0, 200)

  useEffect(() => {
    if (!open) return
    const close = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    const panel = popover.current
    const scrollList = (event: WheelEvent) => {
      const options = list.current
      if (!options) return
      // Keep wheel gestures in the picker, including its search field and edges.
      event.preventDefault()
      const delta = event.deltaMode === 1 ? event.deltaY * 24 : event.deltaMode === 2 ? event.deltaY * options.clientHeight : event.deltaY
      options.scrollTop += delta
    }
    panel?.addEventListener('wheel', scrollList, { passive: false })
    document.addEventListener('pointerdown', close)
    const focusTimer = window.setTimeout(() => search.current?.focus({ preventScroll: true }), 0)
    return () => {
      window.clearTimeout(focusTimer)
      panel?.removeEventListener('wheel', scrollList)
      document.removeEventListener('pointerdown', close)
    }
  }, [open])

  useEffect(() => { if (disabled) setOpen(false) }, [disabled])

  const choose = (pod: ClusterPod) => {
    onChange(keyOf(pod))
    setOpen(false)
    setQuery('')
    setActive(-1)
    trigger.current?.focus()
  }

  return <div className="ks-arthas-pod-picker" ref={root}>
    <button
      aria-controls={open ? `${id}-options` : undefined}
      aria-expanded={open}
      aria-haspopup="listbox"
      aria-label="选择 Arthas Pod"
      className="ks-arthas-pod-trigger"
      disabled={disabled || loading}
      onClick={() => setOpen(current => !current)}
      ref={trigger}
      role="combobox"
      type="button"
    >
      <span className={selected ? 'ks-arthas-pod-value' : 'ks-arthas-pod-placeholder'}>
        {selected ? selected.name : disabled ? '先选择命名空间' : loading ? '正在读取…' : '选择 Pod'}
      </span>
      <span aria-hidden="true" className="ks-arthas-pod-chevron">⌄</span>
    </button>

    {open && <div className="ks-arthas-pod-popover" ref={popover}>
      <div className="ks-arthas-pod-search">
        <input
          aria-label="搜索 Pod"
          autoComplete="off"
          onChange={event => { setQuery(event.target.value); setActive(-1) }}
          onKeyDown={event => {
            if (event.key === 'Escape') { setOpen(false); trigger.current?.focus() }
            if (event.key === 'ArrowDown') { event.preventDefault(); setActive(current => Math.min(visible.length - 1, current + 1)) }
            if (event.key === 'ArrowUp') { event.preventDefault(); setActive(current => Math.max(0, current - 1)) }
            if (event.key === 'Enter' && active >= 0 && visible[active]) { event.preventDefault(); choose(visible[active]) }
          }}
          placeholder={namespace ? `搜索 ${namespace} 中的 Pod` : '先选择命名空间'}
          ref={search}
          value={query}
        />
        <span>{matches.length} 个</span>
      </div>
      <ul ref={list} aria-label="选择 Arthas Pod" id={`${id}-options`} role="listbox">
        {visible.map((pod, index) => <li
          aria-selected={keyOf(pod) === value}
          className={index === active ? 'is-active' : ''}
          id={`${id}-option-${index}`}
          key={keyOf(pod)}
          onClick={() => choose(pod)}
          onMouseDown={event => event.preventDefault()}
          role="option"
        >
          <span>{pod.name}</span>
          <small>{pod.ready_containers}/{pod.total_containers} Ready</small>
        </li>)}
      </ul>
      {!loading && !visible.length && <p className="ks-arthas-pod-empty">{namespace ? '该命名空间没有匹配的运行中 Pod。' : '请先选择命名空间。'}</p>}
      {matches.length > visible.length && <p className="ks-arthas-pod-more">还有 {matches.length - visible.length} 个结果，请输入名称缩小范围。</p>}
    </div>}
  </div>
}
