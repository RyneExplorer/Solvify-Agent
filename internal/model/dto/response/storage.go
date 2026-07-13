package response

// StorageQuotaResponse 描述用户存储配额响应
type StorageQuotaResponse struct {
	MaxStorageBytes       int64 `json:"max_storage_bytes"`
	UsedStorageBytes      int64 `json:"used_storage_bytes"`
	RemainingStorageBytes int64 `json:"remaining_storage_bytes"`
}
