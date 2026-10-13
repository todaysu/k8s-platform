import { Box, Chip, Container, Stack, Typography } from '@mui/material'
import { useEffect, useState } from 'react'

type HealthState = 'checking' | 'connected' | 'unavailable'

const healthDisplay = {
  checking: { color: 'default' as const, label: '正在检查后端' },
  connected: { color: 'success' as const, label: '后端连接正常' },
  unavailable: { color: 'error' as const, label: '后端连接失败' },
}

function App() {
  const [health, setHealth] = useState<HealthState>('checking')

  useEffect(() => {
    let active = true

    async function checkHealth() {
      try {
        const response = await fetch('/api/v1/health')
        if (!response.ok) {
          throw new Error('health request failed')
        }

        const body = await response.json()
        if (body.data?.status !== 'ok') {
          throw new Error('unexpected health response')
        }

        if (active) {
          setHealth('connected')
        }
      } catch {
        if (active) {
          setHealth('unavailable')
        }
      }
    }

    void checkHealth()

    return () => {
      active = false
    }
  }, [])

  const healthStatus = healthDisplay[health]

  return (
    <Box
      component="main"
      sx={{
        alignItems: 'center',
        backgroundColor: 'grey.50',
        display: 'flex',
        minHeight: '100vh',
      }}
    >
      <Container maxWidth="md">
        <Stack spacing={3} sx={{ alignItems: 'flex-start' }}>
          <Chip
            color={healthStatus.color}
            label={healthStatus.label}
            size="small"
          />
          <Typography component="h1" variant="h3" sx={{ fontWeight: 700 }}>
            Kubernetes 多集群管理平台
          </Typography>
          <Typography color="text.secondary" variant="h6">
            聚焦集群稳定性的轻量运维平台
          </Typography>
        </Stack>
      </Container>
    </Box>
  )
}

export default App
