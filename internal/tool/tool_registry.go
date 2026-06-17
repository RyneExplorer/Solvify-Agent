package tool

// providerRegistry 供应商注册表实现
type providerRegistry struct {
	providers map[string]Provider
}

// NewProviderRegistry 创建供应商注册表
func NewProviderRegistry() ProviderRegistry {
	return &providerRegistry{
		providers: make(map[string]Provider),
	}
}

// Register 注册供应商
func (r *providerRegistry) Register(key string, provider Provider) {
	r.providers[key] = provider
}

// Get 获取供应商
func (r *providerRegistry) Get(key string) Provider {
	return r.providers[key]
}

// List 列出所有供应商
func (r *providerRegistry) List() map[string]Provider {
	return r.providers
}

// Keys 返回所有已注册的 provider_key
func (r *providerRegistry) Keys() []string {
	keys := make([]string, 0, len(r.providers))
	for k := range r.providers {
		keys = append(keys, k)
	}
	return keys
}
