import {
  AddRounded,
  AppsRounded,
  CheckCircleRounded,
  ChevronRightRounded,
  CloudOffRounded,
  DashboardRounded,
  ErrorOutlineRounded,
  HubRounded,
  LanRounded,
  MoreHorizRounded,
  SearchRounded,
  SettingsRounded,
  StorageRounded,
  SyncRounded,
  WarningAmberRounded,
} from '@mui/icons-material'
import {
  Avatar,
  Box,
  Button,
  Chip,
  Divider,
  Drawer,
  FormControl,
  IconButton,
  InputAdornment,
  MenuItem,
  Paper,
  Select,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import { useMemo, useState } from 'react'

import AddClusterDialog from '../components/AddClusterDialog'
import {
  clusters,
  type Cluster,
  type ClusterEnvironment,
  type ClusterStatus,
} from '../fixtures/mockClusters'

const sidebarWidth = 236

const statusMeta: Record<
  ClusterStatus,
  { color: string; icon: typeof CheckCircleRounded; label: string; tint: string }
> = {
  healthy: {
    color: '#21835b',
    icon: CheckCircleRounded,
    label: '运行正常',
    tint: '#eaf6f0',
  },
  unreachable: {
    color: '#c8473a',
    icon: CloudOffRounded,
    label: '连接失败',
    tint: '#fbecea',
  },
  warning: {
    color: '#bd7314',
    icon: WarningAmberRounded,
    label: '需要关注',
    tint: '#fff4df',
  },
}

const navItems = [
  { icon: DashboardRounded, label: '健康概览' },
  { active: true, icon: StorageRounded, label: '集群管理' },
  { icon: AppsRounded, label: '业务全景', soon: true },
  { icon: WarningAmberRounded, label: '事件中心', soon: true },
  { icon: HubRounded, label: '巡检分析', soon: true },
]

function StatusBadge({ status }: { status: ClusterStatus }) {
  const meta = statusMeta[status]
  const StatusIcon = meta.icon
  return (
    <Chip
      icon={<StatusIcon sx={{ color: `${meta.color} !important` }} />}
      label={meta.label}
      size="small"
      sx={{ bgcolor: meta.tint, color: meta.color, fontWeight: 700 }}
    />
  )
}

function SummaryCard({
  accent,
  label,
  value,
}: {
  accent: string
  label: string
  value: string
}) {
  return (
    <Paper
      elevation={0}
      sx={{
        border: '1px solid #dfe6e4',
        borderRadius: 2.5,
        minWidth: 0,
        overflow: 'hidden',
        p: 2.25,
        position: 'relative',
      }}
    >
      <Box
        sx={{ bgcolor: accent, bottom: 0, left: 0, position: 'absolute', top: 0, width: 4 }}
      />
      <Typography color="text.secondary" sx={{ fontSize: 12, fontWeight: 700 }}>
        {label}
      </Typography>
      <Typography
        sx={{ fontFamily: '"SFMono-Regular", Consolas, monospace', mt: 0.6 }}
        variant="h4"
      >
        {value}
      </Typography>
    </Paper>
  )
}

function AppSidebar() {
  return (
    <Box
      component="aside"
      sx={{
        bgcolor: '#102f34',
        bottom: 0,
        color: '#d9e8e8',
        display: { md: 'flex', xs: 'none' },
        flexDirection: 'column',
        left: 0,
        position: 'fixed',
        top: 0,
        width: sidebarWidth,
        zIndex: 5,
      }}
    >
      <Stack direction="row" spacing={1.5} sx={{ alignItems: 'center', px: 2.5, py: 3 }}>
        <Avatar
          variant="rounded"
          sx={{ bgcolor: '#54d1c2', color: '#0b3438', height: 38, width: 38 }}
        >
          <LanRounded />
        </Avatar>
        <Box>
          <Typography color="white" sx={{ fontWeight: 800, letterSpacing: '-.02em' }}>
            KubeScope
          </Typography>
          <Typography color="#8fb0b2" sx={{ fontSize: 10, letterSpacing: '.14em' }}>
            CONTROL PLANE
          </Typography>
        </Box>
      </Stack>

      <Typography color="#73979a" sx={{ fontSize: 10, fontWeight: 800, px: 2.5, py: 1 }}>
        运维工作台
      </Typography>
      <Stack spacing={0.5} sx={{ px: 1.5 }}>
        {navItems.map((item) => {
          const NavIcon = item.icon
          return (
            <Button
              color="inherit"
              key={item.label}
              startIcon={<NavIcon fontSize="small" />}
              sx={{
                bgcolor: item.active ? 'rgba(84,209,194,.14)' : 'transparent',
                border: item.active
                  ? '1px solid rgba(84,209,194,.22)'
                  : '1px solid transparent',
                color: item.active ? '#75e0d3' : '#b7cccd',
                justifyContent: 'flex-start',
                px: 1.5,
                py: 1.1,
                textTransform: 'none',
              }}
            >
              <Box component="span" sx={{ flexGrow: 1, textAlign: 'left' }}>
                {item.label}
              </Box>
              {item.soon && (
                <Typography color="#6f9294" sx={{ fontSize: 9 }}>
                  后续
                </Typography>
              )}
            </Button>
          )
        })}
      </Stack>

      <Box sx={{ flexGrow: 1 }} />
      <Divider sx={{ borderColor: 'rgba(255,255,255,.09)' }} />
      <Button
        color="inherit"
        startIcon={<SettingsRounded />}
        sx={{ justifyContent: 'flex-start', m: 1.5, px: 1.5, textTransform: 'none' }}
      >
        系统设置
      </Button>
      <Stack direction="row" spacing={1.2} sx={{ alignItems: 'center', px: 2.5, pb: 2.5 }}>
        <Avatar sx={{ bgcolor: '#f1b55c', color: '#3c2a0e', height: 32, width: 32 }}>
          管
        </Avatar>
        <Box>
          <Typography color="white" sx={{ fontSize: 13, fontWeight: 700 }}>
            平台管理员
          </Typography>
          <Typography color="#7fa0a2" sx={{ fontSize: 10 }}>
            单管理员模式
          </Typography>
        </Box>
      </Stack>
    </Box>
  )
}

function ClusterDetailDrawer({ cluster, onClose }: { cluster: Cluster | null; onClose: () => void }) {
  if (!cluster) return null
  const meta = statusMeta[cluster.status]

  return (
    <Drawer anchor="right" onClose={onClose} open={Boolean(cluster)}>
      <Box sx={{ p: 3.5, width: { sm: 420, xs: '90vw' } }}>
        <Stack direction="row" sx={{ alignItems: 'flex-start', justifyContent: 'space-between' }}>
          <Box>
            <Typography
              color="text.secondary"
              sx={{ fontSize: 11, fontWeight: 800, letterSpacing: '.12em' }}
            >
              CLUSTER PROFILE
            </Typography>
            <Typography sx={{ mt: 0.7 }} variant="h5">
              {cluster.name}
            </Typography>
          </Box>
          <IconButton aria-label="关闭详情" onClick={onClose}>
            <ChevronRightRounded />
          </IconButton>
        </Stack>

        <Stack direction="row" spacing={1} sx={{ mt: 2 }}>
          <StatusBadge status={cluster.status} />
          <Chip label={cluster.environment} size="small" variant="outlined" />
        </Stack>

        <Paper sx={{ bgcolor: '#f6f9f8', mt: 3, p: 2.5 }} variant="outlined">
          <Typography color="text.secondary" sx={{ fontSize: 12 }}>
            API SERVER
          </Typography>
          <Typography sx={{ fontFamily: 'monospace', fontSize: 13, mt: 0.5 }}>
            {cluster.apiServer}
          </Typography>
        </Paper>

        <Stack divider={<Divider flexItem />} sx={{ mt: 2 }}>
          {[
            ['Kubernetes 版本', cluster.kubernetesVersion],
            ['节点状态', `${cluster.nodes.ready} / ${cluster.nodes.total} Ready`],
            ['实例状态', `${cluster.instances.ready} / ${cluster.instances.total} Ready`],
            ['Warning 事件', `${cluster.warningCount} 条`],
            ['最后检查', cluster.lastChecked],
          ].map(([label, value]) => (
            <Stack
              direction="row"
              key={label}
              sx={{ justifyContent: 'space-between', py: 1.8 }}
            >
              <Typography color="text.secondary" variant="body2">
                {label}
              </Typography>
              <Typography sx={{ fontWeight: 700 }} variant="body2">
                {value}
              </Typography>
            </Stack>
          ))}
        </Stack>

        {cluster.status !== 'healthy' && (
          <Box sx={{ bgcolor: meta.tint, borderRadius: 2, color: meta.color, mt: 2, p: 2 }}>
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
              <ErrorOutlineRounded fontSize="small" />
              <Typography sx={{ fontWeight: 800 }} variant="body2">
                {cluster.status === 'unreachable'
                  ? 'API Server 连接超时，请检查网络或凭据。'
                  : '发现节点或实例异常，建议进入资源视图排查。'}
              </Typography>
            </Stack>
          </Box>
        )}

        <Button endIcon={<ChevronRightRounded />} fullWidth sx={{ mt: 3 }} variant="contained">
          进入集群详情
        </Button>
      </Box>
    </Drawer>
  )
}

function ClusterManagementPage() {
  const [dialogOpen, setDialogOpen] = useState(false)
  const [environment, setEnvironment] = useState<'ALL' | ClusterEnvironment>('ALL')
  const [query, setQuery] = useState('')
  const [selectedCluster, setSelectedCluster] = useState<Cluster | null>(null)
  const [status, setStatus] = useState<'ALL' | ClusterStatus>('ALL')

  const visibleClusters = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    return clusters.filter((cluster) => {
      const matchesQuery =
        !normalizedQuery ||
        cluster.name.toLowerCase().includes(normalizedQuery) ||
        cluster.apiServer.toLowerCase().includes(normalizedQuery)
      const matchesEnvironment = environment === 'ALL' || cluster.environment === environment
      const matchesStatus = status === 'ALL' || cluster.status === status
      return matchesQuery && matchesEnvironment && matchesStatus
    })
  }, [environment, query, status])

  return (
    <Box sx={{ minHeight: '100vh' }}>
      <AppSidebar />
      <Box
        component="main"
        sx={{
          backgroundImage:
            'linear-gradient(rgba(10,102,113,.035) 1px, transparent 1px), linear-gradient(90deg, rgba(10,102,113,.035) 1px, transparent 1px)',
          backgroundSize: '28px 28px',
          ml: { md: `${sidebarWidth}px`, xs: 0 },
          minHeight: '100vh',
        }}
      >
        <Box sx={{ mx: 'auto', maxWidth: 1500, px: { lg: 5, sm: 3, xs: 2 }, py: 4 }}>
          <Stack
            direction={{ sm: 'row', xs: 'column' }}
            spacing={2}
            sx={{
              alignItems: { sm: 'flex-end', xs: 'flex-start' },
              justifyContent: 'space-between',
            }}
          >
            <Box>
              <Stack direction="row" spacing={1} sx={{ alignItems: 'center', mb: 0.8 }}>
                <Typography
                  color="primary.main"
                  sx={{ fontSize: 11, fontWeight: 800, letterSpacing: '.14em' }}
                >
                  FLEET / CLUSTERS
                </Typography>
                <Chip label="UI 原型数据" size="small" sx={{ fontSize: 10, height: 20 }} />
              </Stack>
              <Typography component="h1" variant="h4">
                集群管理
              </Typography>
              <Typography color="text.secondary" sx={{ mt: 0.8 }}>
                统一维护 DEV 与 PRD 集群连接，快速识别异常和凭据状态。
              </Typography>
            </Box>
            <Stack direction="row" spacing={1.2}>
              <Button color="inherit" startIcon={<SyncRounded />} variant="outlined">
                刷新状态
              </Button>
              <Button
                onClick={() => setDialogOpen(true)}
                startIcon={<AddRounded />}
                variant="contained"
              >
                添加集群
              </Button>
            </Stack>
          </Stack>

          <Box
            sx={{
              display: 'grid',
              gap: 1.5,
              gridTemplateColumns: {
                lg: 'repeat(4, 1fr)',
                sm: 'repeat(2, 1fr)',
                xs: '1fr',
              },
              mt: 3.5,
            }}
          >
            <SummaryCard accent="#0a6671" label="已纳管集群" value="60" />
            <SummaryCard accent="#21835b" label="连接正常" value="54" />
            <SummaryCard accent="#d48318" label="需要关注" value="4" />
            <SummaryCard accent="#c8473a" label="连接失败" value="2" />
          </Box>

          <Paper
            elevation={0}
            sx={{ border: '1px solid #dfe6e4', borderRadius: 2.5, mt: 2, overflow: 'hidden' }}
          >
            <Stack
              direction={{ lg: 'row', xs: 'column' }}
              spacing={1.2}
              sx={{ alignItems: { lg: 'center', xs: 'stretch' }, p: 2 }}
            >
              <TextField
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索集群名称或 API Server"
                size="small"
                slotProps={{
                  htmlInput: {
                    'aria-label': '搜索集群',
                  },
                  input: {
                    startAdornment: (
                      <InputAdornment position="start">
                        <SearchRounded fontSize="small" />
                      </InputAdornment>
                    ),
                  },
                }}
                sx={{ flexBasis: 340 }}
                value={query}
              />
              <FormControl size="small" sx={{ minWidth: 132 }}>
                <Select
                  aria-label="环境筛选"
                  onChange={(event) => setEnvironment(event.target.value as typeof environment)}
                  value={environment}
                >
                  <MenuItem value="ALL">全部环境</MenuItem>
                  <MenuItem value="PRD">PRD</MenuItem>
                  <MenuItem value="DEV">DEV</MenuItem>
                </Select>
              </FormControl>
              <FormControl size="small" sx={{ minWidth: 142 }}>
                <Select
                  aria-label="状态筛选"
                  onChange={(event) => setStatus(event.target.value as typeof status)}
                  value={status}
                >
                  <MenuItem value="ALL">全部状态</MenuItem>
                  <MenuItem value="healthy">运行正常</MenuItem>
                  <MenuItem value="warning">需要关注</MenuItem>
                  <MenuItem value="unreachable">连接失败</MenuItem>
                </Select>
              </FormControl>
              <Box sx={{ flexGrow: 1 }} />
              <Typography color="text.secondary" variant="body2">
                共 60 套集群 · 当前展示 {visibleClusters.length} 条原型数据
              </Typography>
            </Stack>
            <Divider />

            <TableContainer>
              <Table aria-label="集群列表" sx={{ minWidth: 1080 }}>
                <TableHead>
                  <TableRow>
                    {['集群', '环境', '连接状态', 'Kubernetes', '节点', '实例', '异常', '最后检查', ''].map(
                      (header) => (
                        <TableCell
                          key={header || 'actions'}
                          sx={{
                            bgcolor: '#f7f9f8',
                            color: '#687a7d',
                            fontSize: 11,
                            fontWeight: 800,
                            letterSpacing: '.04em',
                            py: 1.4,
                          }}
                        >
                          {header}
                        </TableCell>
                      ),
                    )}
                  </TableRow>
                </TableHead>
                <TableBody>
                  {visibleClusters.map((cluster) => (
                    <TableRow
                      hover
                      key={cluster.id}
                      onClick={() => setSelectedCluster(cluster)}
                      sx={{ cursor: 'pointer', '&:last-child td': { borderBottom: 0 } }}
                    >
                      <TableCell>
                        <Typography sx={{ fontWeight: 800 }} variant="body2">
                          {cluster.name}
                        </Typography>
                        <Typography
                          color="text.secondary"
                          sx={{ fontFamily: 'monospace', fontSize: 11 }}
                        >
                          {cluster.apiServer}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Chip
                          label={cluster.environment}
                          size="small"
                          sx={{
                            bgcolor: cluster.environment === 'PRD' ? '#202e38' : '#e8eff0',
                            color: cluster.environment === 'PRD' ? '#fff' : '#365559',
                            fontWeight: 800,
                          }}
                        />
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={cluster.status} />
                      </TableCell>
                      <TableCell>
                        <Typography sx={{ fontFamily: 'monospace', fontSize: 12 }}>
                          {cluster.kubernetesVersion}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Typography sx={{ fontWeight: 750 }} variant="body2">
                          {cluster.nodes.ready}
                          <Box component="span" sx={{ color: 'text.secondary', fontWeight: 500 }}>
                            {' '}/ {cluster.nodes.total}
                          </Box>
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Typography sx={{ fontWeight: 750 }} variant="body2">
                          {cluster.instances.ready}
                          <Box component="span" sx={{ color: 'text.secondary', fontWeight: 500 }}>
                            {' '}/ {cluster.instances.total}
                          </Box>
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Typography
                          color={cluster.warningCount > 0 ? 'warning.main' : 'text.secondary'}
                          sx={{ fontWeight: 800 }}
                          variant="body2"
                        >
                          {cluster.warningCount}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Typography color="text.secondary" variant="body2">
                          {cluster.lastChecked}
                        </Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Tooltip title="更多操作">
                          <IconButton
                            aria-label={`${cluster.name} 更多操作`}
                            onClick={(event) => event.stopPropagation()}
                            size="small"
                          >
                            <MoreHorizRounded />
                          </IconButton>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                  ))}
                  {visibleClusters.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={9} sx={{ py: 8, textAlign: 'center' }}>
                        <SearchRounded color="disabled" sx={{ fontSize: 36 }} />
                        <Typography sx={{ fontWeight: 700, mt: 1 }}>
                          没有匹配的集群
                        </Typography>
                        <Typography color="text.secondary" variant="body2">
                          尝试修改搜索词或筛选条件。
                        </Typography>
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </TableContainer>
          </Paper>

          <Stack
            direction={{ sm: 'row', xs: 'column' }}
            spacing={1}
            sx={{ alignItems: { sm: 'center', xs: 'flex-start' }, mt: 2, px: 0.5 }}
          >
            <Stack direction="row" spacing={0.7} sx={{ alignItems: 'center' }}>
              <Box sx={{ bgcolor: '#21835b', borderRadius: '50%', height: 7, width: 7 }} />
              <Typography color="text.secondary" variant="caption">
                最近刷新：14:12:08
              </Typography>
            </Stack>
            <Typography color="text.disabled" variant="caption">
              ·
            </Typography>
            <Typography color="text.secondary" variant="caption">
              页面为 UI 原型，尚未连接真实集群数据
            </Typography>
          </Stack>
        </Box>
      </Box>

      <AddClusterDialog onClose={() => setDialogOpen(false)} open={dialogOpen} />
      <ClusterDetailDrawer cluster={selectedCluster} onClose={() => setSelectedCluster(null)} />
    </Box>
  )
}

export default ClusterManagementPage
