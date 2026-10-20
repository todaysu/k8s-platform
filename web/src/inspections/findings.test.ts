import { describe, expect, it } from 'vitest'

import type { FleetCapacity } from '../api/clusters'
import { buildInspectionFindings, type InspectionCluster } from './findings'

const cluster: InspectionCluster = {
  apiLatencyMs: 18,
  clusterType: 'G01',
  coreComponents: { ready: 3, total: 3 },
  environment: 'stp',
  id: 'cluster-a',
  instances: { ready: 8, total: 10 },
  lastChecked: '2026-10-19 09:00:00',
  metricsAvailable: false,
  name: 'cluster-a',
  nodes: { ready: 2, total: 3 },
  status: 'warning',
  technologyStack: 'TCS',
}

const capacity: FleetCapacity = {
  collected_at: '2026-10-19T01:00:00Z',
  failures: [],
  items: [{
    cluster_id: 'cluster-a',
    cluster_name: 'cluster-a',
    cluster_type: 'G01',
    environment: 'stp',
    technology_stack: 'TCS',
    capacity: {
      baseline: null,
      baseline_review: { status: 'not_required' },
      business: {
        cpu_limits_milli: 14000,
        cpu_requests_milli: 11000,
        cpu_usage_milli: null,
        memory_limits_bytes: 40,
        memory_requests_bytes: 30,
        memory_usage_bytes: null,
        pods_without_cpu_requests: 2,
        pods_without_memory_requests: 1,
      },
      classification_version_id: 1,
      collected_at: '2026-10-19T01:00:00Z',
      comparison: {
        cpu_allocatable_change_percent: 0,
        memory_allocatable_change_percent: 0,
        platform_cpu_request_change_percent: 25,
        platform_memory_request_change_percent: 10,
        status: 'drift',
      },
      cpu_allocatable_milli: 16000,
      memory_allocatable_bytes: 64,
      metrics_available: false,
      namespaces: [],
      new_platform_namespaces: ['new-system'],
      pending: { cpu_limits_milli: 0, cpu_requests_milli: 1000, cpu_usage_milli: null, memory_limits_bytes: 0, memory_requests_bytes: 4, memory_usage_bytes: null },
      platform: { cpu_limits_milli: 3000, cpu_requests_milli: 2000, cpu_usage_milli: null, memory_limits_bytes: 12, memory_requests_bytes: 8, memory_usage_bytes: null },
      safety_reserve_percent: 10,
      system_namespaces: ['kube-system'],
      unclassified: { cpu_limits_milli: 0, cpu_requests_milli: 0, cpu_usage_milli: null, memory_limits_bytes: 0, memory_requests_bytes: 0, memory_usage_bytes: null },
      worker_nodes: 3,
    },
  }],
}

describe('inspection findings', () => {
  it('turns existing health and capacity snapshots into actionable findings', () => {
    const findings = buildInspectionFindings([cluster], capacity)

    expect(findings.map((finding) => finding.ruleId)).toEqual(expect.arrayContaining([
      'NODE_NOT_READY',
      'INSTANCE_NOT_READY',
      'METRICS_UNAVAILABLE',
      'CAPACITY_LOW',
      'PLATFORM_BASELINE_DRIFT',
      'BUSINESS_REQUESTS_MISSING',
      'NEW_PLATFORM_NAMESPACE',
    ]))
    expect(findings.find((finding) => finding.ruleId === 'NODE_NOT_READY')).toMatchObject({
      clusterId: 'cluster-a',
      severity: 'critical',
      title: '1 个节点未就绪',
    })
    expect(new Set(findings.map((finding) => finding.id)).size).toBe(findings.length)
  })

  it('keeps an unreachable cluster as one clear availability finding', () => {
    const findings = buildInspectionFindings([{ ...cluster, status: 'unreachable' }], null)

    expect(findings).toHaveLength(1)
    expect(findings[0]).toMatchObject({
      category: 'availability',
      ruleId: 'CLUSTER_UNREACHABLE',
      severity: 'critical',
    })
  })
})
