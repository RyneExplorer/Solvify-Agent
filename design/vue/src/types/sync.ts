export interface SyncSourceConfig {
  operator_union_id?: string
  workspace_id: string
  root_node_id: string
  sync_mode: string
}

export interface SyncSource {
  id: string
  knowledge_base_id: string
  name: string
  platform: string
  source_config: SyncSourceConfig
  status: number
  last_sync_at: string | null
  last_error_message: string
  created_at: string
  updated_at: string
  deleted_at: string | null
}

export interface CreateSyncSourceRequest {
  knowledge_base_id: string
  name: string
  platform: 'dingtalk'
  source_config: SyncSourceConfig
}

export interface UpdateSyncSourceRequest {
  name: string
  source_config: SyncSourceConfig
  status?: number
}

export interface SyncJob {
  id: string
  sync_source_id: string
  knowledge_base_id: string
  job_type: string
  status: number
  total_count: number
  success_count: number
  failed_count: number
  error_message: string
  started_at: string | null
  finished_at: string | null
  created_at: string
  updated_at: string
}

export interface SyncItem {
  id: string
  sync_source_id: string
  knowledge_base_id: string
  external_id: string
  parent_external_id: string
  name: string
  item_type: string
  category: string
  extension: string
  external_url: string
  file_size: number
  has_children: boolean
  source_updated_at: string | null
  local_document_id: string
  import_status: number
  error_message: string
  created_at: string
  updated_at: string
}
