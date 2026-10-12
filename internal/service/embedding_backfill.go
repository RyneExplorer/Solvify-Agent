package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/logger"
)

// 历史消息向量回填。
//
// 语义召回要求消息表里有向量，而向量是「消息创建时」才写的。引入语义召回之前
// 存下的历史消息一条向量都没有（实测 932 行、0 行有向量），于是语义召回在历史
// 数据上恒为空 —— 聊得越久越召不回来。新消息不再有这个问题，这个任务只负责存量。
//
// 三条设计约束：
//   - **可续跑**：用 keyset 分页（afterID）而不是 OFFSET，中途挂了/被 kill 再跑一遍
//     不会重复处理已完成的行，也不会因为前面的行已更新而漏掉后面的行。
//   - **不无限重试**：游标只前进不回头，单条失败记下来最后统一报。反过来做（失败就
//     停在原地重试）会在某条永久失败的消息上死循环，回填永远跑不完。
//   - **失败要响亮**：连续失败达到熔断阈值就判定 embedding 侧整体不可用（服务没起、
//     维度错配、额度耗尽），立刻中止而不是把剩下几百条全跑成失败 —— 否则日志里全是
//     同一个根因刷出来的噪音，真正的第一条反而被埋掉（红线 19）。

const (
	// backfillBatchSize 每批取多少条。太大则失败时白跑的多，太小则往返次数多。
	backfillBatchSize = 50
	// backfillConcurrency 单批内并发向量化数。再高对本地 Ollama 无益，反而抢内存。
	backfillConcurrency = 4
	// backfillCircuitLimit 连续失败多少条后熔断。批大小是 50，阈值取一条批的量级，
	// 保证「一批全废」一定会被抓住，而单条脏数据（超长/空）不会误触发。
	backfillCircuitLimit = backfillBatchSize
	// backfillSingleTimeout 单条向化的超时。
	backfillSingleTimeout = 10 * time.Second
)

// MessageEmbeddingBackfiller 回填历史消息向量。
type MessageEmbeddingBackfiller struct {
	messageRepo repository.ChatMessageRepo
	embedFn     rag.EmbeddingFunc
}

// NewMessageEmbeddingBackfiller 创建回填器。
func NewMessageEmbeddingBackfiller(messageRepo repository.ChatMessageRepo, embedFn rag.EmbeddingFunc) *MessageEmbeddingBackfiller {
	return &MessageEmbeddingBackfiller{messageRepo: messageRepo, embedFn: embedFn}
}

// BackfillResult 回填结果统计。
type BackfillResult struct {
	Scanned  int      // 扫描到的消息数（不含向量已存在的行）
	Written  int      // 成功写入向量数
	Failed   int      // 单条向量化/写入失败数
	FailedID []string // 失败的消息 ID（回填完成后仍然召不回来的那些）
	CircuitB bool     // 是否因连续失败熔断提前结束
}

// BackfillMessageEmbeddings 回填所有缺向量的历史消息。
//
// 这是**一次性**任务：不排进周期调度，由启动流程异步调一次。语义召回的判据是
// 「有向量」，所以只要启动过、embedding 侧可用，跑一次就永远收敛 —— 排成周期任务
// 只会每隔一阵就全表扫一遍「还缺不缺」，那是把一次性动作伪装成常态。
func (b *MessageEmbeddingBackfiller) BackfillMessageEmbeddings(ctx context.Context) BackfillResult {
	var res BackfillResult
	if b == nil || b.messageRepo == nil || b.embedFn == nil {
		logger.Warn("[backfill] 跳过历史消息向量回填：messageRepo 或 embedFn 未就绪")
		return res
	}

	consecutiveFailures := 0
	cursor := "" // keyset 游标：本批最后一条的 ID

	for {
		batch, err := b.messageRepo.FindMessagesMissingEmbedding(ctx, cursor, backfillBatchSize)
		if err != nil {
			logger.Errorf("[backfill] 读取缺向量的消息失败（已处理 %d 条，写入 %d 条，失败 %d 条）: %v",
				res.Scanned, res.Written, res.Failed, err)
			return res
		}
		if len(batch) == 0 {
			break
		}

		written, failed, failedIDs := b.embedBatch(ctx, batch)
		res.Scanned += len(batch)
		res.Written += written
		res.Failed += failed
		res.FailedID = append(res.FailedID, failedIDs...)

		consecutiveFailures = advanceCircuit(consecutiveFailures, failed, len(batch))
		if consecutiveFailures >= backfillCircuitLimit {
			res.CircuitB = true
			logger.Errorf("[backfill] 连续 %d 条回填失败，判定 embedding 侧不可用，提前中止（已写入 %d 条）。"+
				"请先确认 embedding 服务可达且维度与 chat_messages.embedding 一致（启动期 EnsureChatMessageEmbeddingSchema 会报维度错配），"+
				"修好后重启即可续跑 —— 游标只前进，已写入的不会重来",
				consecutiveFailures, res.Written)
			return res
		}

		cursor = batch[len(batch)-1].ID
		logger.Infof("[backfill] 回填进度：已扫描 %d 条，写入 %d 条，失败 %d 条", res.Scanned, res.Written, res.Failed)
	}

	logger.Infof("[backfill] 历史消息向量回填完成：扫描 %d 条，写入 %d 条，失败 %d 条", res.Scanned, res.Written, res.Failed)
	if len(res.FailedID) > 0 {
		ids := res.FailedID
		if len(ids) > 10 {
			ids = ids[:10]
		}
		// 这些消息在语义召回里永远查不到，逐条点名（只列前 10 个 ID，全量进日志会淹掉别的）
		logger.Errorf("[backfill] %d 条历史消息回填失败，语义召回永远召不到它们（示例 ID: %v）。"+
			"可按这些 ID 单独排查（内容为空/超长/embedding 服务报错），修好后重跑本任务即可",
			len(res.FailedID), ids)
	}
	return res
}

// embedBatch 对一批消息并发向量化，一次往返写回。
// 单条失败不影响同批其它条目 —— 一条脏数据不该让整批白跑。
//
// ⚠️ 这里等的是「所有 worker 真的跑完」而不是「派发完成」：
// 信号量只保证有空闲槽位，dispatcher 遍历完 batch 时后面的 worker 可能还没被调度，
// 此时读 results 会拿到零值 item（err=nil, vec=nil）—— 表现为
// 「一批 50 条只有 40 条被写，剩下 10 条静默消失，失败数也不对」。
// 这个缺陷是实测出来的：注入前 5 条回填用例里有 4 条红，形态全都是「每批少 10 条」。
func (b *MessageEmbeddingBackfiller) embedBatch(ctx context.Context, batch []entity.ChatMessage) (written, failed int, failedIDs []string) {
	type item struct {
		msg  entity.ChatMessage
		vec  entity.FloatVector
		skip bool // 内容为空，无需向量化
		err  error
	}

	results := make([]item, len(batch))
	sem := make(chan struct{}, backfillConcurrency)
	var wg sync.WaitGroup

	// dispatcher 与 worker 同在一个 goroutine 里：w.Wait() 才是「全部执行完」的屏障
	wg.Add(len(batch))
	for i := range batch {
		sem <- struct{}{}
		go func(idx int) {
			defer func() {
				<-sem
				wg.Done()
			}()
			vec, skip, err := b.embedOne(ctx, batch[idx].Content)
			results[idx] = item{msg: batch[idx], vec: vec, skip: skip, err: err}
		}(i)
	}
	wg.Wait()

	updates := make([]repository.MessageEmbeddingUpdate, 0, len(batch))
	for _, r := range results {
		switch {
		case r.skip:
			// 内容为空：既不是成功也不是失败 —— 计入扫描但两边都不算，
			// 否则会把它算成「写入 1 条」，账目与实际不符
			continue
		case r.err != nil:
			failed++
			failedIDs = append(failedIDs, r.msg.ID)
			continue
		}
		updates = append(updates, repository.MessageEmbeddingUpdate{ID: r.msg.ID, Embedding: r.vec})
	}
	if len(updates) == 0 {
		return 0, failed, failedIDs
	}
	if err := b.messageRepo.BatchUpdateEmbedding(ctx, updates); err != nil {
		// 整批写入失败：每条都算失败。维度错配是这里最可能的根因，
		// 而它报在整批上、不报在单条上 —— 所以日志措辞不能只说「写失败」
		for _, u := range updates {
			failedIDs = append(failedIDs, u.ID)
		}
		logger.Warnf("[backfill] 批量写入 %d 条向量失败（若报维度不符，说明配置 dimension 与 chat_messages.embedding 列不一致）: %v",
			len(updates), err)
		return 0, failed + len(updates), failedIDs
	}
	return len(updates), failed, failedIDs
}

// embedOne 单条向量化。skip=true 表示这条不需要处理（内容为空）。
func (b *MessageEmbeddingBackfiller) embedOne(ctx context.Context, content string) (vec entity.FloatVector, skip bool, err error) {
	if len(content) == 0 {
		return nil, true, nil
	}
	embedCtx, cancel := context.WithTimeout(ctx, backfillSingleTimeout)
	defer cancel()
	f64, err := b.embedFn(embedCtx, content)
	if err != nil {
		return nil, false, fmt.Errorf("向量化失败: %w", err)
	}
	f32 := make(entity.FloatVector, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32, false, nil
}

// advanceCircuit 更新连续失败计数。
//
// 判据是「这一批整体废了」还是「混着几条脏数据」：
//   - 整批全失败 → 累加，embedding 侧很可能整体不可用（服务没起 / 维度错配）；
//   - 部分失败   → 清零，部分成功证明通道是通的，剩下的就是个别脏数据，
//     若也累加的话，一条超长消息就能把整个回填卡死在同一批上。
func advanceCircuit(consecutive, failed, batchSize int) int {
	if failed == batchSize {
		return consecutive + failed
	}
	return 0
}
