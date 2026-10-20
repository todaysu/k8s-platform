import { describe, expect, it } from 'vitest'
import { analyzeJvm, jvmSample, parseGcLog, parseHeapInfo, parseHistogram, parseJstat, parseSize } from './jvm'

const jstat = `S0 S1 E O M CCS YGC YGCT FGC FGCT CGC CGCT GCT
0.00 99.90 45.32 42.10 95.20 92.10 123 1.234 0 0.000 0 0.000 1.234
0.00 99.90 78.30 68.40 95.30 92.10 145 1.510 1 0.210 0 0.000 1.720
0.00 99.90 91.20 82.70 95.40 92.20 168 1.820 2 0.460 0 0.000 2.280`

describe('JVM diagnostics parser', () => {
  it('parses jstat -gcutil samples and trends', () => {
    const report = parseJstat(jstat)
    expect(report?.samples).toHaveLength(3)
    expect(report?.samples.at(-1)?.o).toBeCloseTo(82.7)
    expect(report?.samples.at(-1)?.fgc).toBe(2)
  })

  it('parses GC.heap_info and histogram', () => {
    expect(parseHeapInfo(jvmSample.heap)?.totalBytes).toBe(2097152 * 1024)
    expect(parseHeapInfo(jvmSample.heap)?.metaspaceUsedBytes).toBe(123456 * 1024)
    const histogram = parseHistogram(jvmSample.histogram)
    expect(histogram?.entries[0].className).toBe('[B')
    expect(histogram?.entries[0].bytes).toBe(67108864)
  })

  it('parses unified GC log lines and sizes', () => {
    const gc = parseGcLog(jvmSample.gc)
    expect(gc?.pauses).toBe(2)
    expect(gc?.fullPauses).toBe(1)
    expect(gc?.maxPauseMs).toBeCloseTo(880)
    expect(parseSize('2Gi')).toBe(2 * 1024 ** 3)
    expect(parseSize('512MiB')).toBe(512 * 1024 ** 2)
  })

  it('reports old generation, full GC and container-limit risks', () => {
    const report = analyzeJvm(jvmSample)
    expect(report.findings.some(finding => finding.severity === 'critical')).toBe(true)
    expect(report.findings.some(finding => finding.title.includes('老年代'))).toBe(true)
    expect(report.findings.some(finding => finding.title.includes('Full GC'))).toBe(true)
  })

  it('returns an informational result when nothing is provided', () => {
    const report = analyzeJvm({ jstat: '', heap: '', histogram: '', gc: '', containerLimit: '', containerUsage: '' })
    expect(report.findings[0].severity).toBe('info')
  })
})
