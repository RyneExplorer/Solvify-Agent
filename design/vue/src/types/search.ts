export interface ChatMessageSearchResult {
  id: string
  session_id: string
  session_title: string
  role: string
  content: string
  score: number
  created_at: string
}

export interface DocumentSearchResult {
  id: string
  knowledge_base_id: string
  document_id: string
  title: string
  content: string
  score: number
}

export interface SearchResult {
  chat_messages: ChatMessageSearchResult[]
  documents: DocumentSearchResult[]
}
