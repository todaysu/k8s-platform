export type ClusterEnvironment = 'DEV' | 'PRD'
export type ClusterStatus = 'healthy' | 'warning' | 'unreachable'

export type Cluster = {
  apiServer: string
  environment: ClusterEnvironment
  id: string
  instances: { ready: number; total: number }
  kubernetesVersion: string
  lastChecked: string
  name: string
  nodes: { ready: number; total: number }
  status: ClusterStatus
  warningCount: number
}

export const clusters: Cluster[] = [
  {
    apiServer: 'https://10.21.8.10:6443',
    environment: 'PRD',
    id: 'cluster-01',
    instances: { ready: 318, total: 320 },
    kubernetesVersion: 'v1.32.4',
    lastChecked: '刚刚',
    name: 'prd-shanghai-01',
    nodes: { ready: 24, total: 24 },
    status: 'healthy',
    warningCount: 2,
  },
  {
    apiServer: 'https://10.31.4.10:6443',
    environment: 'PRD',
    id: 'cluster-02',
    instances: { ready: 281, total: 286 },
    kubernetesVersion: 'v1.31.7',
    lastChecked: '18 秒前',
    name: 'prd-beijing-02',
    nodes: { ready: 21, total: 22 },
    status: 'warning',
    warningCount: 7,
  },
  {
    apiServer: 'https://10.16.6.10:6443',
    environment: 'DEV',
    id: 'cluster-03',
    instances: { ready: 146, total: 146 },
    kubernetesVersion: 'v1.32.4',
    lastChecked: '26 秒前',
    name: 'dev-shanghai-01',
    nodes: { ready: 12, total: 12 },
    status: 'healthy',
    warningCount: 0,
  },
  {
    apiServer: 'https://10.41.12.10:6443',
    environment: 'DEV',
    id: 'cluster-04',
    instances: { ready: 93, total: 98 },
    kubernetesVersion: 'v1.30.10',
    lastChecked: '41 秒前',
    name: 'dev-beijing-03',
    nodes: { ready: 8, total: 8 },
    status: 'warning',
    warningCount: 5,
  },
  {
    apiServer: 'https://10.52.3.10:6443',
    environment: 'PRD',
    id: 'cluster-05',
    instances: { ready: 0, total: 0 },
    kubernetesVersion: 'v1.31.7',
    lastChecked: '6 分钟前',
    name: 'prd-guangzhou-01',
    nodes: { ready: 0, total: 18 },
    status: 'unreachable',
    warningCount: 0,
  },
  {
    apiServer: 'https://10.63.9.10:6443',
    environment: 'DEV',
    id: 'cluster-06',
    instances: { ready: 112, total: 112 },
    kubernetesVersion: 'v1.32.4',
    lastChecked: '53 秒前',
    name: 'dev-chengdu-02',
    nodes: { ready: 10, total: 10 },
    status: 'healthy',
    warningCount: 1,
  },
]
