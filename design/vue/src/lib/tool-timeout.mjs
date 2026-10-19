// 空值保持未配置；填写30也必须显式保存，不能与MCP调用默认300秒混淆。
export function toolTimeoutValue(value) {
  if (value === '' || value === undefined || value === null) return undefined
  const seconds = Number(value)
  if (!Number.isInteger(seconds) || seconds < 1 || seconds > 86400) {
    throw new Error('超时时间必须为1～86400秒的整数，或留空使用默认值')
  }
  return seconds
}

export function toolTimeoutFromConfig(value) {
  return value === undefined || value === null || value === 0 ? '' : value
}
