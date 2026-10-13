import { Box, Chip, Container, Stack, Typography } from '@mui/material'

function App() {
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
          <Chip color="primary" label="项目骨架" size="small" />
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
