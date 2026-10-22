import {expect,it} from 'vitest'

import type {AuditRequest} from './audit'
import {
  auditExportCSV,
  auditExportFilename,
  auditExportJSON,
  type AuditExportScope,
} from './auditExport'

const request = (overrides: Partial<AuditRequest> = {}): AuditRequest => ({
  id: 'audit-1',
  time: '2026-10-21T00:00:00.000Z',
  end: '2026-10-21T00:00:01.000Z',
  verb: 'patch',
  user: 'deployer',
  sourceIPs: ['192.0.2.10'],
  stage: 'ResponseComplete',
  stages: ['RequestReceived', 'ResponseComplete'],
  code: 200,
  reason: '',
  level: 'Metadata',
  resource: {resource: 'deployments', namespace: 'payments', name: 'web', uid: 'uid-1'},
  owners: [],
  images: ['nginx:1.27'],
  replicas: 3,
  evidence: {auditID: 'audit-1', safe: true},
  ...overrides,
})

const scope: AuditExportScope = {
  type: 'resource',
  cluster: 'prod east / 01',
  resource: {resource: 'deployments', namespace: 'payments', name: 'web', uid: 'uid-1'},
  includeChildren: true,
  filters: {namespace: 'payments', query: '', sourceIp: '', user: '', verb: '', result: ''},
  coverage: {start: '2026-10-21T00:00:00.000Z', end: '2026-10-21T01:00:00.000Z'},
}

it('exports filtered requests as UTF-8 BOM CSV with stable columns', () => {
  const csv = auditExportCSV([{item: request(), relation: 'self'}])
  expect(csv.startsWith('\uFEFF时间(UTC),结束时间(UTC)')).toBe(true)
  expect(csv).toContain('2026-10-21T00:00:00.000Z')
  expect(csv).toContain('deployments,payments,web,uid-1')
  expect(csv).toContain('成功,200')
  expect(csv).toContain('当前资源')
  expect(csv).toContain('RequestReceived -> ResponseComplete')
  expect(csv).not.toContain('audit-1,safe')
  expect(csv.endsWith('\r\n')).toBe(true)
})

it('escapes CSV cells and neutralizes spreadsheet formulas', () => {
  const csv = auditExportCSV([{
    item: request({
      user: '=HYPERLINK("https://example.invalid")',
      reason: 'line one\nline two',
      resource: {resource: 'pods', namespace: 'payments', name: 'web,canary', uid: ''},
    }),
    relation: 'match',
  }])
  expect(csv).toContain("'=HYPERLINK")
  expect(csv).toContain('"web,canary"')
  expect(csv).toContain('line one line two')
})

it('exports JSON metadata and makes evidence optional', () => {
  const withEvidence = auditExportJSON(
    [{item: request(), relation: 'child'}],
    {generatedAt: '2026-10-21T02:00:00.000Z', scope},
    true,
  )
  const parsed = JSON.parse(withEvidence) as {schema: string; count: number; requests: {relation: string; evidence?: unknown}[]}
  expect(parsed.schema).toBe('kubescope.audit.analysis/v1')
  expect(parsed.count).toBe(1)
  expect(parsed.requests[0].relation).toBe('child')
  expect(parsed.requests[0].evidence).toEqual({auditID: 'audit-1', safe: true})

  const withoutEvidence = auditExportJSON(
    [{item: request(), relation: 'child'}],
    {generatedAt: '2026-10-21T02:00:00.000Z', scope},
    false,
  )
  expect(withoutEvidence).not.toContain('"evidence"')
})

it('builds a safe filename from cluster and selected resource', () => {
  const filename = auditExportFilename(scope, 'csv', new Date('2026-10-21T12:34:56.789Z'))
  expect(filename).toBe('kubescope-audit-prod-east-01-payments-deployments-web-20261021T123456Z.csv')
})
