import {
  Alert,
  Button,
  Checkbox,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalVariant,
  Radio,
} from '@patternfly/react-core'
import {useState} from 'react'

import {
  auditExportCSV,
  auditExportFilename,
  auditExportJSON,
  auditExportMime,
  type AuditExportFormat,
  type AuditExportRow,
  type AuditExportScope,
} from '../tools/auditExport'
import './AuditDataExportDialog.css'

type Props = {
  open: boolean
  onClose: () => void
  rows: AuditExportRow[]
  scope: AuditExportScope
}

const formatLabels: Record<AuditExportFormat, string> = {
  csv: 'CSV',
  json: 'JSON',
}

const showTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', {hour12: false}) : '未记录'

function downloadText(content: string, filename: string, mime: string) {
  const url = URL.createObjectURL(new Blob([content], {type: mime}))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}

function filterChips(scope: AuditExportScope) {
  const chips: string[] = []
  if (scope.filters.namespace) chips.push(`命名空间：${scope.filters.namespace}`)
  if (scope.filters.query) chips.push(`资源搜索：${scope.filters.query}`)
  if (scope.filters.sourceIp) chips.push(`来源 IP：${scope.filters.sourceIp}`)
  if (scope.filters.user) chips.push(`操作人：${scope.filters.user}`)
  if (scope.filters.verb) chips.push(`动作：${scope.filters.verb}`)
  if (scope.filters.result) chips.push(`结果：${scope.filters.result}`)
  return chips
}

export default function AuditDataExportDialog({open, onClose, rows, scope}: Props) {
  const [format, setFormat] = useState<AuditExportFormat>('csv')
  const [includeEvidence, setIncludeEvidence] = useState(true)
  const chips = filterChips(scope)
  const target = scope.type === 'resource' && scope.resource
    ? `${scope.resource.namespace || '集群级'} / ${scope.resource.resource}/${scope.resource.name}`
    : '当前过滤后的全部匹配请求'

  const handleDownload = () => {
    const generatedAt = new Date().toISOString()
    const content = format === 'csv'
      ? auditExportCSV(rows)
      : auditExportJSON(rows, {generatedAt, scope}, includeEvidence)
    downloadText(content, auditExportFilename(scope, format), auditExportMime(format))
    onClose()
  }

  return (
    <Modal
      isOpen={open}
      variant={ModalVariant.medium}
      onClose={onClose}
      aria-labelledby="ks-audit-data-export-title"
    >
      <ModalHeader title="导出审计分析数据" labelId="ks-audit-data-export-title" />
      <ModalBody>
        <div className="ks-audit-data-export">
          <p>将当前筛选结果导出为文件。导出在浏览器本地生成，不会上传审计日志。</p>
          <dl className="ks-audit-data-export__summary">
            <div><dt>来源集群</dt><dd>{scope.cluster || '未绑定（离线分析）'}</dd></div>
            <div><dt>导出范围</dt><dd>{target}{scope.type === 'resource' && scope.includeChildren ? ' · 包含关联子资源' : ''}</dd></div>
            <div><dt>请求数量</dt><dd>{rows.length} 条</dd></div>
            <div><dt>日志覆盖</dt><dd>{showTime(scope.coverage.start)} — {showTime(scope.coverage.end)}</dd></div>
            <div>
              <dt>请求过滤</dt>
              <dd>{chips.length
                ? <span className="ks-audit-data-export__filters">{chips.map(chip => <span key={chip}>{chip}</span>)}</span>
                : '未设置额外过滤'}</dd>
            </div>
          </dl>
          <fieldset className="ks-audit-data-export__formats">
            <legend>文件格式</legend>
            <Radio
              id="ks-audit-export-csv"
              name="ks-audit-export-format"
              label="CSV（Excel 可直接打开）"
              isChecked={format === 'csv'}
              onChange={() => setFormat('csv')}
            />
            <Radio
              id="ks-audit-export-json"
              name="ks-audit-export-format"
              label="JSON（保留完整结构）"
              isChecked={format === 'json'}
              onChange={() => setFormat('json')}
            />
          </fieldset>
          {format === 'json' && (
            <Checkbox
              id="ks-audit-export-evidence"
              label="包含证据字段"
              description="只包含页面已展示的安全投影，不包含请求正文、Secret 数据或环境变量。"
              isChecked={includeEvidence}
              onChange={(_event, checked) => setIncludeEvidence(checked)}
            />
          )}
          <Alert
            isInline
            variant="info"
            title="CSV 使用 UTF-8 BOM 和固定列，适合二次分析；JSON 带 scope、过滤条件和证据，适合留档或交给其他工具处理。"
          />
        </div>
      </ModalBody>
      <ModalFooter>
        <Button isDisabled={!rows.length} onClick={handleDownload}>
          下载 {formatLabels[format]}（{rows.length} 条）
        </Button>
        <Button variant="link" onClick={onClose}>取消</Button>
      </ModalFooter>
    </Modal>
  )
}
