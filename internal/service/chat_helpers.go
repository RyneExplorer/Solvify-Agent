package service

import (
	"context"

	"github.com/google/uuid"
)

func requestIDFromCtx(ctx context.Context) string {
	type iKey string
	const key iKey = "request_id"
	if v := ctx.Value(key); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return uuid.New().String()
}

