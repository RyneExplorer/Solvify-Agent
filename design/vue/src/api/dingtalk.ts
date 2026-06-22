import { request } from './client'
import type {
  DingTalkAuthCodeExchangeRequest,
  DingTalkBinding,
  DingTalkNodeList,
  DingTalkOAuthConfig,
  DingTalkWorkspaceList,
} from '@/types/dingtalk'

// 获取钉钉扫码参数
export function getDingTalkOAuthConfig() {
  return request<DingTalkOAuthConfig>('/dingtalk/oauth-config')
}

// 兑换钉钉授权码并保存绑定
export function exchangeDingTalkAuthCode(data: DingTalkAuthCodeExchangeRequest) {
  return request<DingTalkBinding>('/dingtalk/auth-code/exchange', {
    method: 'POST',
    body: data,
  })
}

// 查询当前用户钉钉绑定状态
export function getDingTalkBinding() {
  return request<DingTalkBinding>('/dingtalk/binding')
}

// 删除当前用户钉钉绑定
export function deleteDingTalkBinding() {
  return request<{ deleted: boolean }>('/dingtalk/binding', { method: 'DELETE' })
}

// 查询钉钉知识库列表
export function listDingTalkWorkspaces(nextToken = '', maxResults = 30) {
  const params = new URLSearchParams()
  if (nextToken) params.set('next_token', nextToken)
  params.set('max_results', String(maxResults))
  return request<DingTalkWorkspaceList>(`/dingtalk/workspaces?${params.toString()}`)
}

// 查询钉钉知识库节点列表
export function listDingTalkNodes(
  workspaceID: string,
  parentNodeID = '',
  nextToken = '',
  maxResults = 50,
) {
  const params = new URLSearchParams()
  if (parentNodeID) params.set('parent_node_id', parentNodeID)
  if (nextToken) params.set('next_token', nextToken)
  params.set('max_results', String(maxResults))
  return request<DingTalkNodeList>(`/dingtalk/workspaces/${workspaceID}/nodes?${params.toString()}`)
}
