import {
  Alert,
  Button,
  Form,
  FormGroup,
  FormSelect,
  FormSelectOption,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Tab,
  Tabs,
  TabTitleIcon,
  TabTitleText,
  TextArea,
  TextInput,
} from '@patternfly/react-core'
import { ClipboardIcon, UploadIcon } from '@patternfly/react-icons'
import { useState } from 'react'
import type { ChangeEvent } from 'react'

import {
  onboardCluster,
} from '../api/clusters'
import type { ManagedCluster } from '../api/clusters'

import './AddClusterDialog.css'

type Props = {
  environmentOptions: string[]
  onClose: () => void
  onSuccess: (cluster: ManagedCluster) => void
  open: boolean
}

type InputMode = 'upload' | 'paste'
function AddClusterDialog({ environmentOptions, onClose, onSuccess, open }: Props) {
  const [mode, setMode] = useState<InputMode>('upload')
  const [name, setName] = useState('')
  const [environment, setEnvironment] = useState('')
  const [clusterType, setClusterType] = useState('')
  const [technologyStack, setTechnologyStack] = useState('')
  const [kubeconfig, setKubeconfig] = useState('')
  const [fileName, setFileName] = useState('')
  const [isOnboarding, setIsOnboarding] = useState(false)
  const [error, setError] = useState('')

  const handleClose = () => {
    setMode('upload')
    setName('')
    setEnvironment('')
    setClusterType('')
    setTechnologyStack('')
    setKubeconfig('')
    setFileName('')
    setError('')
    setIsOnboarding(false)
    onClose()
  }

  const handleFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) return

    setError('')
    if (file.size > 1024 * 1024) {
      setKubeconfig('')
      setFileName('')
      setError('Kubeconfig 不能超过 1 MiB。')
      return
    }

    try {
      setKubeconfig(await file.text())
      setFileName(file.name)
    } catch {
      setKubeconfig('')
      setFileName('')
      setError('无法读取该文件，请重新选择。')
    }
  }

  const handleOnboard = async () => {
    setIsOnboarding(true)
    setError('')
    try {
      const managedCluster = await onboardCluster(name.trim(), environment.trim(), clusterType, technologyStack, kubeconfig)
      handleClose()
      onSuccess(managedCluster)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '纳管集群失败，请稍后重试。')
      setIsOnboarding(false)
    }
  }

  return (
    <Modal
      aria-labelledby="add-cluster-title"
      isOpen={open}
      onClose={handleClose}
      variant="medium"
    >
      <ModalHeader
        description="填写集群名称并提供 Kubeconfig。平台会自动使用其中的 current-context。"
        labelId="add-cluster-title"
        title="纳管新集群"
      />
      <ModalBody>
        <Form>
          <FormGroup fieldId="cluster-name" isRequired label="集群名称">
            <TextInput
              aria-label="集群名称"
              id="cluster-name"
              isRequired
              onChange={(_, value) => setName(value)}
              placeholder="例如：cluster-shanghai-01"
              value={name}
            />
          </FormGroup>

          <FormGroup fieldId="cluster-environment" isRequired label="所属环境">
            <TextInput
              aria-label="所属环境"
              id="cluster-environment"
              isRequired
              list="cluster-environment-options"
              maxLength={32}
              onChange={(_, value) => {
                setEnvironment(value)
                setError('')
              }}
              placeholder="输入或选择环境，例如：生产环境"
              value={environment}
            />
            <datalist id="cluster-environment-options">
              {environmentOptions.map((option) => <option key={option} value={option} />)}
            </datalist>
          </FormGroup>

          <FormGroup fieldId="cluster-type" isRequired label="集群类型">
            <FormSelect
              aria-label="集群类型"
              id="cluster-type"
              isRequired
              onChange={(_, value) => {
                setClusterType(value)
                setTechnologyStack(value === 'OCP' ? 'OCP' : '')
                setError('')
              }}
              value={clusterType}
            >
              <FormSelectOption isPlaceholder label="请选择集群类型" value="" />
              <FormSelectOption label="G01" value="G01" />
              <FormSelectOption label="OCP" value="OCP" />
            </FormSelect>
          </FormGroup>

          <FormGroup fieldId="cluster-technology-stack" isRequired label="技术栈">
            <FormSelect
              aria-label="技术栈"
              isDisabled={!clusterType || clusterType === 'OCP'}
              id="cluster-technology-stack"
              isRequired
              onChange={(_, value) => {
                setTechnologyStack(value)
                setError('')
              }}
              value={technologyStack}
            >
              <FormSelectOption isPlaceholder label={clusterType ? '请选择技术栈' : '请先选择集群类型'} value="" />
              {clusterType === 'G01' && <FormSelectOption label="TCS" value="TCS" />}
              {clusterType === 'G01' && <FormSelectOption label="CMBK8S" value="CMBK8S" />}
              {clusterType === 'OCP' && <FormSelectOption label="OCP（随集群类型自动选择）" value="OCP" />}
            </FormSelect>
          </FormGroup>

          <Tabs
            activeKey={mode}
            aria-label="Kubeconfig 录入方式"
            onSelect={(_, key) => setMode(key as InputMode)}
          >
            <Tab
              eventKey="upload"
              title={
                <>
                  <TabTitleIcon><UploadIcon /></TabTitleIcon>
                  <TabTitleText>上传文件</TabTitleText>
                </>
              }
            />
            <Tab
              eventKey="paste"
              title={
                <>
                  <TabTitleIcon><ClipboardIcon /></TabTitleIcon>
                  <TabTitleText>粘贴内容</TabTitleText>
                </>
              }
            />
          </Tabs>

          {mode === 'upload' ? (
            <div className="ks-file-drop">
              <UploadIcon className="ks-file-drop__icon" />
              <strong>拖放 Kubeconfig 到这里</strong>
              <span>支持 .yaml、.yml 或无扩展名文件，最大 1 MiB</span>
              <Button component="label" variant="secondary">
                选择文件
                <input
                  accept=".yaml,.yml,application/yaml,text/yaml,text/plain"
                  hidden
                  onChange={handleFile}
                  type="file"
                />
              </Button>
              {fileName && <span className="ks-file-drop__selection">已选择：{fileName}</span>}
            </div>
          ) : (
            <FormGroup fieldId="kubeconfig-content" label="Kubeconfig 内容">
              <TextArea
                aria-label="Kubeconfig 内容"
                id="kubeconfig-content"
                onChange={(_, value) => {
                  setKubeconfig(value)
                  setError('')
                }}
                placeholder={'apiVersion: v1\nclusters: ...'}
                resizeOrientation="vertical"
                rows={10}
                value={kubeconfig}
              />
            </FormGroup>
          )}

          <Alert isInline title="凭据安全说明" variant="info">
            支持内嵌 Token 或客户端证书。凭据不会显示在页面中，原始 Kubeconfig 不会长期保留。
          </Alert>

          {error && (
            <Alert isInline title="纳管失败" variant="danger">
              {error}
            </Alert>
          )}

        </Form>
      </ModalBody>
      <ModalFooter>
        <Button
          isDisabled={
            !name.trim()
            || !environment.trim()
            || !clusterType
            || !technologyStack
            || !kubeconfig.trim()
            || isOnboarding
          }
          isLoading={isOnboarding}
          onClick={handleOnboard}
          variant="primary"
        >
          {isOnboarding ? '正在纳管' : '纳管集群'}
        </Button>
        <Button onClick={handleClose} variant="link">取消</Button>
      </ModalFooter>
    </Modal>
  )
}

export default AddClusterDialog
