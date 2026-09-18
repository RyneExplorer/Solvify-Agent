package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// errTxManagerNoDB 表示事务管理器没有拿到数据库连接（装配错误，不是业务错误）
var errTxManagerNoDB = errors.New("repository: TxManager 未初始化数据库连接")

// TxManager 提供「一个业务操作里的多次写必须同生共死」的唯一入口。
//
// 为什么必须有它：仓库方法各自持有连接池，service 层一旦把一个业务操作拆成
// 两次跨仓库的写（例如「先删消息、再删会话」），中间失败就会留下永久不一致
// —— 消息没了、会话还在，用户点进去是空的。此前这类缺陷在代码里没有任何
// 表达方式：service 拿不到 *gorm.DB，也没人规定必须开事务。
//
// 用法：
//
//	return s.txMgr.InTx(ctx, func(ctx context.Context) error {
//	    if err := s.messageRepo.DeleteBySessionID(ctx, sessionID); err != nil {
//	        return fmt.Errorf("删除会话消息失败: %w", err)
//	    }
//	    return s.sessionRepo.Delete(ctx, sessionID)
//	})
//
// 关键性质：**回调收到的 ctx 里带着事务句柄，所有仓库方法都会自动加入这个事务**
// （见 dbFor）。因此「已经进了 InTx、却漏了某个仓库」在结构上不会发生 ——
// 不需要逐处记得把 tx 传下去。
type TxManager interface {
	// InTx 在单个数据库事务内执行 fn；fn 返回非 nil 错误则整体回滚。
	//
	// ⚠️ fn 必须使用它收到的 ctx（而不是外层 ctx），否则那个仓库操作不会进事务。
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type gormTxManager struct {
	db *gorm.DB
}

// NewTxManager 基于主库连接创建事务管理器
func NewTxManager(db *gorm.DB) TxManager {
	return &gormTxManager{db: db}
}

func (m *gormTxManager) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if fn == nil {
		return nil
	}
	if m == nil || m.db == nil {
		return errTxManagerNoDB
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(withTx(ctx, tx))
	})
}

// txContextKey 是事务句柄在 context 里的键。
// 用私有空结构体：包外无法构造同名键，也无法从外部伪造一个事务。
type txContextKey struct{}

// withTx 把事务句柄放进 context
func withTx(ctx context.Context, tx *gorm.DB) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, txContextKey{}, tx)
}

// txFrom 取出 context 里的事务句柄；没有事务时返回 nil
func txFrom(ctx context.Context) *gorm.DB {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value(txContextKey{}).(*gorm.DB)
	return tx
}

// dbFor 是所有仓库方法获取数据库句柄的唯一入口：
//   - ctx 里带着事务 → 用事务（同一个 InTx 内的多次写于是共享一个事务）
//   - 否则 → 用仓库自己的连接池，并把 ctx 挂上（保留可取消 / 超时语义）
//
// ⚠️ 仓库实现里禁止再直接写 r.db 或 r.db.WithContext(ctx)：那会让该方法绕过
// 外层事务，且没有任何编译期或运行期的提示。tx_accessor_guard_test.go 守住这条。
func dbFor(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if tx := txFrom(ctx); tx != nil {
		return tx.WithContext(ctx)
	}
	return fallback.WithContext(ctx)
}
