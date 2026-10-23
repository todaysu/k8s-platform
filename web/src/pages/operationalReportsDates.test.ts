import { describe, expect, it } from 'vitest'
import { previousReportDate, reportRange, shanghaiDay } from './operationalReportsDates'

describe('operational report dates', () => {
  const now = new Date('2026-10-23T01:00:00Z')

  it('uses Shanghai calendar days and the previous complete natural period', () => {
    expect(shanghaiDay(now)).toBe('2026-10-23')
    expect(previousReportDate('weekly', now)).toBe('2026-10-12')
    expect(previousReportDate('monthly', now)).toBe('2026-09-01')
    expect(previousReportDate('quarterly', now)).toBe('2026-07-01')
  })

  it('shows natural report ranges', () => {
    expect(reportRange('weekly', '2026-10-22')).toBe('2026-10-19 至 2026-10-25')
    expect(reportRange('monthly', '2026-10-22')).toBe('2026-10-01 至 2026-10-31')
    expect(reportRange('quarterly', '2026-08-02')).toBe('2026-07-01 至 2026-09-30')
  })
})
