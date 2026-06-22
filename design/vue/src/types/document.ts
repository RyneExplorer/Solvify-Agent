export interface Document {
  id: string
  knowledge_base_id: string
  title: string
  file_name: string
  file_type: string
  file_size: number
  storage_path: string
  file_hash: string
  source_type: string
  external_id: string
  external_url: string
  source_updated_at: string | null
  status: number
  error_message: string
  ready_at: string | null
  created_at: string
  updated_at: string
  deleted_at: string | null
  delete_expired_at: string | null
}

export interface DocumentProcessingJob {
  id: string
  document_id: string
  job_type: string
  status: number
  error_message: string
  started_at: string | null
  finished_at: string | null
  created_at: string
  updated_at: string
}

export interface UploadDocumentResponse {
  document: Document
  job: DocumentProcessingJob
}

export interface DocumentVersionListItem {
  id: string
  document_id: string
  version_no: number
  content_hash: string
  change_summary: string
  created_at: string
}

export interface DocumentVersionDetail {
  id: string
  document_id: string
  version_no: number
  content: string
  content_hash: string
  change_summary: string
  created_at: string
}

export interface CreateDocumentVersionRequest {
  content: string
  change_summary?: string
}
