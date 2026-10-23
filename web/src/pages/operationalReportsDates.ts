import type { ReportPeriod } from '../api/reports'

export function shanghaiDay(now = new Date()): string {
  return new Date(now.getTime() + 8 * 60 * 60 * 1000).toISOString().slice(0, 10)
}

export function previousReportDate(period: ReportPeriod, now = new Date()): string {
  const today = new Date(`${shanghaiDay(now)}T00:00:00Z`)
  if (period === 'weekly') today.setUTCDate(today.getUTCDate() - (today.getUTCDay() + 6) % 7 - 7)
  else if (period === 'monthly') today.setUTCMonth(today.getUTCMonth() - 1, 1)
  else today.setUTCMonth(Math.floor(today.getUTCMonth() / 3) * 3 - 3, 1)
  return today.toISOString().slice(0, 10)
}

export function reportRange(period: ReportPeriod, value: string): string {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return '选择日期所在的自然周期'
  const start = new Date(`${value}T00:00:00Z`)
  if (Number.isNaN(start.getTime())) return '选择日期所在的自然周期'
  if (period === 'weekly') start.setUTCDate(start.getUTCDate() - (start.getUTCDay() + 6) % 7)
  else if (period === 'monthly') start.setUTCDate(1)
  else start.setUTCMonth(Math.floor(start.getUTCMonth() / 3) * 3, 1)
  const end = new Date(start)
  if (period === 'weekly') end.setUTCDate(end.getUTCDate() + 6)
  else { end.setUTCMonth(end.getUTCMonth() + (period === 'monthly' ? 1 : 3)); end.setUTCDate(0) }
  return `${start.toISOString().slice(0, 10)} 至 ${end.toISOString().slice(0, 10)}`
}
