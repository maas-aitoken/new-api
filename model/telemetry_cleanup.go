package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"
)

// purgeBefore 分块删除 T 对应的表里 created_at 早于 before 的行，避免一条大 DELETE 长时间持锁。
// 每块的行数由环境变量 chunkEnv 调整（默认 1000，范围 1-10000）：块越小单次持锁越短，
// 块越大清理越快；上限保证 id IN (...) 的占位符数远低于各数据库的单语句上限。
// T 必须有 id 和 created_at 两列。ClickHouse 日志库靠表 TTL 过期，这里不处理。
// ctx 取消时返回已删除的行数和 ctx 的错误。
func purgeBefore[T any](ctx context.Context, chunkEnv string, before int64) (int64, error) {
	if before <= 0 || common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return 0, nil
	}
	chunk := min(max(common.GetEnvOrDefault(chunkEnv, 1000), 1), 10000)
	db := LOG_DB.WithContext(ctx)
	var deleted int64
	for {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		var ids []int64
		if err := db.Model(new(T)).
			Where("created_at < ?", before).
			Order("created_at ASC").
			Limit(chunk).
			Pluck("id", &ids).Error; err != nil {
			return deleted, err
		}
		if len(ids) == 0 {
			return deleted, nil
		}
		result := db.Where("id IN ?", ids).Delete(new(T))
		if result.Error != nil {
			return deleted, result.Error
		}
		deleted += result.RowsAffected
		if len(ids) < chunk || result.RowsAffected == 0 {
			return deleted, nil
		}
	}
}

// hasRowsBefore 报告 T 对应的表里是否还有 created_at 早于 before 的行，供清理任务判断要不要运行。
func hasRowsBefore[T any](before int64) bool {
	if before <= 0 || common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return false
	}
	var ids []int64
	err := LOG_DB.Model(new(T)).
		Where("created_at < ?", before).
		Limit(1).
		Pluck("id", &ids).Error
	return err == nil && len(ids) > 0
}
