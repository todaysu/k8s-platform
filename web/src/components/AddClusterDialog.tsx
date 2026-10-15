import { ContentPasteRounded, UploadFileRounded } from '@mui/icons-material'
import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from '@mui/material'
import { useState } from 'react'

type Props = {
  onClose: () => void
  open: boolean
}

type InputMode = 'upload' | 'paste'

function AddClusterDialog({ onClose, open }: Props) {
  const [mode, setMode] = useState<InputMode>('upload')
  const [name, setName] = useState('')

  return (
    <Dialog
      fullWidth
      maxWidth="sm"
      onClose={onClose}
      open={open}
      slotProps={{ paper: { sx: { borderRadius: 3, overflow: 'hidden' } } }}
    >
      <DialogTitle component="h2" sx={{ pb: 1, pt: 3 }}>
        纳管新集群
      </DialogTitle>
      <DialogContent>
        <Typography color="text.secondary" sx={{ mb: 3 }} variant="body2">
          填写集群名称并提供 Kubeconfig。平台会自动使用其中的 current-context。
        </Typography>

        <Stack spacing={2.5}>
          <TextField
            autoFocus
            fullWidth
            label="集群名称"
            onChange={(event) => setName(event.target.value)}
            placeholder="例如：prd-shanghai-01"
            value={name}
          />

          <Box sx={{ borderBottom: 1, borderColor: 'divider' }}>
            <Tabs
              aria-label="Kubeconfig 录入方式"
              onChange={(_, value: InputMode) => setMode(value)}
              value={mode}
            >
              <Tab
                icon={<UploadFileRounded fontSize="small" />}
                iconPosition="start"
                label="上传文件"
                value="upload"
              />
              <Tab
                icon={<ContentPasteRounded fontSize="small" />}
                iconPosition="start"
                label="粘贴内容"
                value="paste"
              />
            </Tabs>
          </Box>

          {mode === 'upload' ? (
            <Box
              sx={{
                alignItems: 'center',
                bgcolor: '#f6f9f8',
                border: '1px dashed #9bb5b6',
                borderRadius: 2,
                display: 'flex',
                flexDirection: 'column',
                gap: 1,
                px: 3,
                py: 4,
                textAlign: 'center',
              }}
            >
              <UploadFileRounded color="primary" sx={{ fontSize: 34 }} />
              <Typography sx={{ fontWeight: 700 }}>拖放 Kubeconfig 到这里</Typography>
              <Typography color="text.secondary" variant="body2">
                支持 .yaml、.yml 或无扩展名文件，最大 1 MiB
              </Typography>
              <Button component="label" size="small" variant="outlined">
                选择文件
                <input hidden type="file" />
              </Button>
            </Box>
          ) : (
            <TextField
              fullWidth
              label="Kubeconfig 内容"
              minRows={9}
              multiline
              placeholder="apiVersion: v1&#10;clusters: ..."
            />
          )}

          <Alert severity="info">
            测试连接成功后才会保存。Token 不会显示在页面中，原始 Kubeconfig
            不会长期保留。
          </Alert>
        </Stack>
      </DialogContent>
      <DialogActions sx={{ borderColor: 'divider', borderTop: '1px solid', p: 2.5 }}>
        <Button color="inherit" onClick={onClose}>
          取消
        </Button>
        <Button disabled={!name.trim()} variant="contained">
          测试连接
        </Button>
      </DialogActions>
    </Dialog>
  )
}

export default AddClusterDialog
