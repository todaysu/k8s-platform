export type JvmSeverity = 'critical' | 'warning' | 'info'

export type JvmFinding = {
  severity: JvmSeverity
  title: string
  detail: string
  action: string
}

export type JstatSample = {
  s0: number
  s1: number
  e: number
  o: number
  m: number
  ccs: number
  ygc: number
  ygct: number
  fgc: number
  fgct: number
  cgc: number
  cgct: number
  gct: number
}

export type JstatReport = { samples: JstatSample[]; mode: 'gcutil' | 'gc' }

export type HeapReport = {
  totalBytes: number
  usedBytes: number
  metaspaceUsedBytes: number
  metaspaceCommittedBytes: number
  classSpaceUsedBytes: number
}

export type HistogramEntry = { rank: number; instances: number; bytes: number; className: string }
export type HistogramReport = { entries: HistogramEntry[]; totalBytes: number }

export type GcReport = {
  pauses: number
  fullPauses: number
  maxPauseMs: number
  totalPauseMs: number
  lastAfterBytes: number
  lastTotalBytes: number
}

export type JvmInput = {
  jstat: string
  heap: string
  histogram: string
  gc: string
  containerLimit: string
  containerUsage: string
}

export type JvmReport = {
  jstat: JstatReport | null
  heap: HeapReport | null
  histogram: HistogramReport | null
  gc: GcReport | null
  findings: JvmFinding[]
  summary: string
}

const numberValue = (value: string | undefined) => {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

export function parseJstat(text: string): JstatReport | null {
  const lines = text.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
  const headerIndex = lines.findIndex(line => /\bYGC\b/.test(line) && /\b(FGC|GCT)\b/.test(line) && /\b(S0|S0C)\b/.test(line))
  if (headerIndex < 0) return null
  const headers = lines[headerIndex].split(/\s+/)
  const position = (name: string) => headers.indexOf(name)
  const mode: 'gcutil' | 'gc' = position('S0') >= 0 ? 'gcutil' : 'gc'
  const samples: JstatSample[] = []
  for (const line of lines.slice(headerIndex + 1)) {
    const values = line.split(/\s+/)
    if (values.length < headers.length) continue
    const get = (name: string) => numberValue(values[position(name)])
    if (mode === 'gcutil') {
      const sample: JstatSample = {
        s0: get('S0'), s1: get('S1'), e: get('E'), o: get('O'), m: get('M'), ccs: get('CCS'),
        ygc: get('YGC'), ygct: get('YGCT'), fgc: get('FGC'), fgct: get('FGCT'),
        cgc: get('CGC'), cgct: get('CGCT'), gct: get('GCT'),
      }
      if (!Number.isFinite(sample.o)) continue
      samples.push(sample)
      continue
    }
    const capacity = (name: string) => get(name)
    const used = (name: string) => get(name)
    const percent = (usedName: string, capacityName: string) => {
      const total = capacity(capacityName)
      return total > 0 ? used(usedName) / total * 100 : 0
    }
    samples.push({
      s0: percent('S0U', 'S0C'), s1: percent('S1U', 'S1C'), e: percent('EU', 'EC'), o: percent('OU', 'OC'),
      m: percent('MU', 'MC'), ccs: 0, ygc: get('YGC'), ygct: get('YGCT'), fgc: get('FGC'),
      fgct: get('FGCT'), cgc: get('CGC'), cgct: get('CGCT'), gct: get('GCT'),
    })
  }
  return samples.length ? { samples, mode } : null
}

export function parseHeapInfo(text: string): HeapReport | null {
  const pair = text.match(/total\s+(\d+)K,\s+used\s+(\d+)K/i)
  if (!pair) return null
  const metaspace = text.match(/Metaspace\s+used\s+(\d+)K,\s+committed\s+(\d+)K/i)
  const classSpace = text.match(/class space\s+used\s+(\d+)K/i)
  return {
    totalBytes: numberValue(pair[1]) * 1024,
    usedBytes: numberValue(pair[2]) * 1024,
    metaspaceUsedBytes: numberValue(metaspace?.[1]) * 1024,
    metaspaceCommittedBytes: numberValue(metaspace?.[2]) * 1024,
    classSpaceUsedBytes: numberValue(classSpace?.[1]) * 1024,
  }
}

export function parseHistogram(text: string): HistogramReport | null {
  const entries: HistogramEntry[] = []
  for (const line of text.split(/\r?\n/)) {
    const match = line.match(/^\s*(\d+):\s+(\d+)\s+(\d+)\s+(.+?)\s*$/)
    if (!match || /^total/i.test(match[4])) continue
    entries.push({
      rank: Number(match[1]),
      instances: Number(match[2]),
      bytes: Number(match[3]),
      className: match[4].replace(/\s+\(.+\)$/, ''),
    })
  }
  if (!entries.length) return null
  entries.sort((left, right) => right.bytes - left.bytes)
  return { entries, totalBytes: entries.reduce((total, entry) => total + entry.bytes, 0) }
}

export function parseGcLog(text: string): GcReport | null {
  let pauses = 0
  let fullPauses = 0
  let maxPauseMs = 0
  let totalPauseMs = 0
  let lastAfterBytes = 0
  let lastTotalBytes = 0
  for (const line of text.split(/\r?\n/)) {
    const unified = line.match(/GC\(\d+\)\s+Pause\s+(Young|Full)[^\n]*?(\d+)M->(\d+)M\((\d+)M\)\s+([\d.]+)ms/i)
    if (unified) {
      const pause = numberValue(unified[5])
      pauses++
      if (/^full$/i.test(unified[1])) fullPauses++
      maxPauseMs = Math.max(maxPauseMs, pause)
      totalPauseMs += pause
      lastAfterBytes = numberValue(unified[3]) * 1024 * 1024
      lastTotalBytes = numberValue(unified[4]) * 1024 * 1024
      continue
    }
    const legacy = line.match(/\[(?:Full )?GC[^\]]*?([\d.]+)\s*secs\][^\n]*?(\d+)M->(\d+)M\((\d+)M\)/i)
    if (legacy) {
      const pause = numberValue(legacy[1]) * 1000
      pauses++
      if (/\[Full GC/i.test(line)) fullPauses++
      maxPauseMs = Math.max(maxPauseMs, pause)
      totalPauseMs += pause
      lastAfterBytes = numberValue(legacy[3]) * 1024 * 1024
      lastTotalBytes = numberValue(legacy[4]) * 1024 * 1024
    }
  }
  if (!pauses) return null
  return { pauses, fullPauses, maxPauseMs, totalPauseMs, lastAfterBytes, lastTotalBytes }
}

export function parseSize(value: string): number {
  const match = value.trim().match(/^([\d.]+)\s*(B|K|KB|KiB|Ki|M|MB|MiB|Mi|G|GB|GiB|Gi|T|TB|TiB|Ti)?$/i)
  if (!match) return 0
  const amount = Number(match[1])
  const unit = (match[2] ?? 'MiB').toLowerCase()
  const multiplier = unit.startsWith('t') ? 1024 ** 4 : unit.startsWith('g') ? 1024 ** 3 : unit.startsWith('m') ? 1024 ** 2 : unit.startsWith('k') ? 1024 : 1
  return Number.isFinite(amount) ? amount * multiplier : 0
}

const formatPercent = (value: number) => `${value.toFixed(1)}%`
const formatBytes = (value: number) => {
  if (!value) return '—'
  const gib = value / 1024 ** 3
  if (gib >= 1) return `${gib.toFixed(2)} GiB`
  return `${(value / 1024 ** 2).toFixed(0)} MiB`
}

export function analyzeJvm(input: JvmInput): JvmReport {
  const jstat = parseJstat(input.jstat)
  const heap = parseHeapInfo(input.heap)
  const histogram = parseHistogram(input.histogram)
  const gc = parseGcLog(input.gc)
  const findings: JvmFinding[] = []
  const latest = jstat?.samples.at(-1)

  if (latest) {
    if (latest.o >= 90) {
      findings.push({ severity: 'critical', title: '老年代接近占满', detail: `jstat 显示老年代使用率 ${formatPercent(latest.o)}。`, action: '立即查看 Full GC 后老年代是否回落，并抓取类直方图确认增长对象。' })
    } else if (latest.o >= 75) {
      findings.push({ severity: 'warning', title: '老年代占用偏高', detail: `jstat 显示老年代使用率 ${formatPercent(latest.o)}。`, action: '结合 Full GC 次数与堆直方图判断是流量增长还是对象滞留。' })
    }
    if (jstat && jstat.samples.length > 1) {
      const first = jstat.samples[0]
      if (latest.o - first.o > 10) {
        findings.push({ severity: 'warning', title: '老年代持续增长', detail: `采样期间老年代从 ${formatPercent(first.o)} 升至 ${formatPercent(latest.o)}。`, action: '在内存升高前后各导出一份类直方图，对比对象数量与字节数。' })
      }
      if (latest.fgc - first.fgc > 2) {
        findings.push({ severity: 'critical', title: 'Full GC 持续发生', detail: `采样期间 Full GC 从 ${first.fgc} 次增至 ${latest.fgc} 次。`, action: '检查堆上限与容器 limit，并确认 Full GC 后老年代是否明显下降。' })
      }
    }
    if (latest.fgc > 0) {
      findings.push({
        severity: latest.fgct >= 5 || latest.fgc >= 10 ? 'critical' : 'warning',
        title: '存在 Full GC',
        detail: `累计 Full GC ${latest.fgc} 次，耗时 ${latest.fgct.toFixed(2)} 秒。`,
        action: 'Full GC 不频繁且老年代能回落通常可接受；频繁或耗时长需检查堆大小、对象生命周期和显式 System.gc。',
      })
    }
    if (latest.ygc > 0 && latest.o < 50) {
      findings.push({ severity: 'info', title: '年轻代回收活跃', detail: `Young GC ${latest.ygc} 次，耗时 ${latest.ygct.toFixed(2)} 秒，老年代 ${formatPercent(latest.o)}。`, action: '若应用延迟正常，这通常是分配速率高的正常表现；可对比 Eden 大小与分配速率。' })
    }
  }

  if (heap) {
    const ratio = heap.totalBytes > 0 ? heap.usedBytes / heap.totalBytes * 100 : 0
    if (ratio >= 90) {
      findings.push({ severity: 'critical', title: '堆使用率接近上限', detail: `GC.heap_info 显示 ${formatBytes(heap.usedBytes)} / ${formatBytes(heap.totalBytes)}（${formatPercent(ratio)}）。`, action: '导出堆直方图，并检查 MaxHeapSize 与容器 limit 的差值。' })
    } else if (ratio >= 75) {
      findings.push({ severity: 'warning', title: '堆使用率偏高', detail: `GC.heap_info 显示 ${formatBytes(heap.usedBytes)} / ${formatBytes(heap.totalBytes)}（${formatPercent(ratio)}）。`, action: '观察 Full GC 后回落情况；若长期维持高位，抓取直方图。' })
    }
    if (heap.metaspaceUsedBytes > 256 * 1024 * 1024) {
      findings.push({ severity: 'warning', title: 'Metaspace 占用较高', detail: `Metaspace 已使用 ${formatBytes(heap.metaspaceUsedBytes)}。`, action: '检查动态类加载、字节码生成、热部署和反射代理；Metaspace 泄漏不会随 Full GC 明显回落。' })
    }
  }

  if (gc) {
    if (gc.fullPauses > 0) {
      findings.push({ severity: 'warning', title: 'GC 日志包含 Full GC', detail: `Full GC ${gc.fullPauses} 次，最大停顿 ${gc.maxPauseMs.toFixed(0)} ms。`, action: '按时间对齐 Full GC 与内存曲线，确认每次 Full GC 后老年代是否下降。' })
    }
    if (gc.maxPauseMs >= 1000) {
      findings.push({ severity: 'critical', title: '单次 GC 停顿超过 1 秒', detail: `最大停顿 ${gc.maxPauseMs.toFixed(0)} ms。`, action: '检查堆大小、GC 算法和容器 CPU limit；大堆 Full GC 会造成明显停顿。' })
    } else if (gc.maxPauseMs >= 200) {
      findings.push({ severity: 'warning', title: 'GC 停顿偏高', detail: `最大停顿 ${gc.maxPauseMs.toFixed(0)} ms，累计 ${(gc.totalPauseMs / 1000).toFixed(1)} 秒。`, action: '结合流量与 GC 次数判断是否需要调整堆或 GC 参数。' })
    }
    if (gc.fullPauses > 0 && gc.lastAfterBytes >= gc.lastTotalBytes * 0.9) {
      findings.push({ severity: 'critical', title: 'Full GC 后堆仍接近上限', detail: `最近一次 Full GC 后 ${formatBytes(gc.lastAfterBytes)} / ${formatBytes(gc.lastTotalBytes)}。`, action: '高度怀疑对象滞留或堆上限不足；导出直方图并检查大对象与缓存。' })
    }
  }

  if (histogram && histogram.totalBytes > 0) {
    const top = histogram.entries[0]
    if (top && top.bytes / histogram.totalBytes >= 0.4) {
      findings.push({ severity: 'warning', title: '单一类型占用过高', detail: `${top.className} 占 ${formatPercent(top.bytes / histogram.totalBytes * 100)}（${formatBytes(top.bytes)}）。`, action: '确认该类型是否来自业务缓存、字节数组、字符串或框架；在内存升高前后对比实例数。' })
    }
  }

  const limit = parseSize(input.containerLimit)
  const usage = parseSize(input.containerUsage)
  if (limit > 0 && usage > 0) {
    const ratio = usage / limit
    if (ratio >= 0.9) findings.push({ severity: 'critical', title: '容器内存接近 limit', detail: `容器使用 ${formatBytes(usage)} / ${formatBytes(limit)}（${formatPercent(ratio * 100)}）。`, action: '即使 JVM 堆未满，容器也可能因堆外内存、线程栈或 Native 内存被 OOMKill；检查 Xmx/MaxRAMPercentage 与堆外使用。' })
    else if (ratio >= 0.8) findings.push({ severity: 'warning', title: '容器内存余量偏低', detail: `容器使用 ${formatBytes(usage)} / ${formatBytes(limit)}。`, action: '为堆外内存、元空间、线程栈和系统缓存预留 20% 以上空间。' })
  }
  if (limit > 0 && heap) {
    if (heap.totalBytes > limit) findings.push({ severity: 'critical', title: 'JVM 堆超过容器 limit', detail: `堆 ${formatBytes(heap.totalBytes)} > 容器 limit ${formatBytes(limit)}。`, action: '容器 OOMKill 风险很高；降低 Xmx 或提高 limit。' })
    else if (heap.totalBytes >= limit * 0.85) findings.push({ severity: 'warning', title: '堆上限接近容器 limit', detail: `堆 ${formatBytes(heap.totalBytes)}，容器 limit ${formatBytes(limit)}。`, action: '为 Metaspace、线程栈、直接内存和 Native 库预留空间。' })
  }
  if (usage > 0 && heap && usage > heap.usedBytes) {
    const offHeap = usage - heap.usedBytes
    if (offHeap >= 512 * 1024 * 1024) findings.push({ severity: 'warning', title: '堆外内存占用明显', detail: `容器用量比 JVM 已用堆多 ${formatBytes(offHeap)}。`, action: '检查 Metaspace、直接内存、Netty、线程栈、JNI 与 Native 分配；堆外泄漏不会体现在 heap 直方图。' })
  }

  if (!findings.length) {
    findings.push({ severity: 'info', title: '未发现明显异常信号', detail: jstat || heap || histogram || gc ? '当前数据未触发高风险规则，建议结合内存曲线和 Full GC 后回落情况继续观察。' : '尚未导入任何 JVM 数据。', action: '至少提供 jstat -gcutil 或 jcmd GC.heap_info；内存持续升高时补充类直方图和 GC 日志。' })
  }
  const rank: Record<JvmSeverity, number> = { critical: 0, warning: 1, info: 2 }
  findings.sort((left, right) => rank[left.severity] - rank[right.severity])
  const critical = findings.filter(finding => finding.severity === 'critical').length
  const warning = findings.filter(finding => finding.severity === 'warning').length
  const summary = critical
    ? `发现 ${critical} 个高风险信号${warning ? `、${warning} 个警告` : ''}，建议优先处理。`
    : warning
      ? `发现 ${warning} 个需要关注的信号，建议结合业务流量和内存曲线判断。`
      : '当前数据未发现明显风险，建议继续观察 Full GC 后堆是否回落。'
  return { jstat, heap, histogram, gc, findings, summary }
}

export const jvmSample: JvmInput = {
  jstat: `S0     S1     E      O      M     CCS    YGC     YGCT    FGC    FGCT    CGC    CGCT     GCT
0.00  99.90  45.32  42.10  95.20  92.10   123    1.234   0      0.000   0      0.000    1.234
0.00  99.90  78.30  68.40  95.30  92.10   145    1.510   1      0.210   0      0.000    1.720
0.00  99.90  91.20  82.70  95.40  92.20   168    1.820   2      0.460   0      0.000    2.280`,
  heap: `garbage-first heap   total 2097152K, used 1730150K [0x0000000700000000, 0x0000000800000000)
  region size 1024K, 32 young (32768K), 3 survivors (3072K)
 Metaspace       used 123456K, committed 130000K, reserved 1056768K
  class space    used 15000K, committed 16000K, reserved 1048576K`,
  histogram: ` num     #instances         #bytes  class name (module)
-------------------------------------------------------
   1:        820000       67108864  [B (java.base@17.0.1)
   2:        410000       19680000  java.lang.String (java.base@17.0.1)
   3:        120000        9600000  com.example.order.CacheEntry (app@1.0.0)
Total      1350000      96388864`,
  gc: `[2026-10-20T10:00:00.000+0800][info][gc] GC(1) Pause Young (Normal) (G1 Evacuation Pause) 1024M->512M(2048M) 12.345ms
[2026-10-20T10:05:00.000+0800][info][gc] GC(2) Pause Full (G1 Compaction Pause) 2048M->1900M(2048M) 880.000ms`,
  containerLimit: '2Gi',
  containerUsage: '1.95Gi',
}
