import { CssBaseline, ThemeProvider, createTheme } from '@mui/material'

import ClusterManagementPage from './pages/ClusterManagementPage'

const theme = createTheme({
  palette: {
    background: { default: '#f3f5f4', paper: '#ffffff' },
    error: { main: '#c8473a' },
    primary: { dark: '#083c43', main: '#0a6671' },
    success: { main: '#21835b' },
    text: { primary: '#172a2d', secondary: '#607276' },
    warning: { main: '#d48318' },
  },
  shape: { borderRadius: 10 },
  typography: {
    fontFamily: '"Avenir Next", "PingFang SC", "Noto Sans SC", sans-serif',
    h4: { fontSize: '1.9rem', fontWeight: 720, letterSpacing: '-0.03em' },
    h6: { fontWeight: 700 },
  },
})

function App() {
  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      <ClusterManagementPage />
    </ThemeProvider>
  )
}

export default App
