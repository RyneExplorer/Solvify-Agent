export interface DingTalkOAuthConfig {
  client_id: string
  redirect_uri: string
  scope: string
  response_type: string
  prompt: string
  state: string
}

export interface DingTalkAuthCodeExchangeRequest {
  auth_code: string
  state: string
}

export interface DingTalkBinding {
  bound: boolean
  ding_open_id?: string
  ding_union_id?: string
  corp_id?: string
  nickname?: string
  avatar?: string
}

export interface DingTalkWorkspace {
  workspace_id: string
  root_node_id: string
  name: string
  type: string
  icon_url: string
  url: string
}

export interface DingTalkWorkspaceList {
  workspaces: DingTalkWorkspace[]
  next_token: string
}

export interface DingTalkNode {
  node_id: string
  workspace_id: string
  name: string
  type: string
  url: string
  size: number
  modified_at: number
}

export interface DingTalkNodeList {
  nodes: DingTalkNode[]
  next_token: string
}
