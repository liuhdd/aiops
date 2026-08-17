export interface Alert {
  id: string
  device: string
  metric: string
  severity: string
  message: string
  status: string
  fired_at: string
  created_at: string
}

export interface PipelineRun {
  id: string
  alert_id: string
  status: string
  iteration: number
  max_iteration: number
  created_at: string
  updated_at: string
}

export interface StageResult {
  id: string
  run_id: string
  stage_id: string
  stage_name: string
  status: string
  iteration: number
  output: string
  error?: string
  started_at: string
  ended_at: string
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  alert_id?: string
  created_at: string
}

export interface AlertDetail {
  alert: Alert
  run: PipelineRun
  stages: StageResult[]
}

export type WSEventType = 'alert' | 'stage' | 'run' | 'message'

export interface WSMessage {
  type: WSEventType
  data: unknown
}

export type IntentKind = 'GROUP' | 'SYSTEM' | 'KB' | 'MCP'

export interface IntentNode {
  id: string
  parent_id: string
  name: string
  kind: IntentKind
  code: string
  bind_target: string
  rule_keywords: string[]
  description: string
  examples: string[]
  enabled: boolean
  deleted: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

export interface IntentTreeNode extends IntentNode {
  children: IntentTreeNode[]
}

export interface IntentUpsert {
  parent_id: string
  name: string
  kind: IntentKind
  code: string
  bind_target: string
  rule_keywords: string[]
  description: string
  examples: string[]
  sort_order: number
}
