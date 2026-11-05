-- 分层令牌桶：一次原子检查并扣减 n 个桶。
--
-- 为什么不复用 rate_limit.lua：那个脚本的 EXPIRE 被注释掉（按令牌建桶会无上限
-- 泄漏）、只取 TIME[1] 的整秒（截断误差会相互抵消而不累积，但上界是 RPM/60 个
-- 令牌，且算不出亚秒级的重试提示）、且返回值只有 0/1，拿不到建议重试时间。
-- 它还被 ModelRequestRateLimit 用着，直接改会影响那条路径。
--
-- 接口按多个桶设计，v1 只传一个（令牌桶）。后续要加用户桶和平台桶时，
-- 调用方多传 KEYS 和参数即可，脚本不用改。
--
-- KEYS[1..n]  各层桶的 key
-- ARGV[1]     本次请求要扣的令牌数
-- ARGV[2]     桶数量 n
-- ARGV[3..]   每个桶两个参数：rate_per_min, capacity（按 KEYS 顺序排列）
--
-- 返回 {allowed, blocked_index, retry_after_ms}
--   allowed        1 允许，0 拒绝
--   blocked_index  被哪个桶拦住（1-based，allowed=1 时为 0）
--   retry_after_ms 建议重试毫秒数（allowed=1 时为 0）

local requested = tonumber(ARGV[1])
local bucket_count = tonumber(ARGV[2])

-- 用毫秒而不是整秒：last_time 每次都会被重写成当前时刻，取整秒会把
-- 不到一秒的零头永久丢掉，导致实际额度低于配置值。
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

-- 第一遍：只读。算出每个桶补币后的余额，任意一个不够就整体拒绝，
-- 不写入任何桶——分多次调用各自扣减会产生部分扣减，下层拒绝时上层额度已被白耗。
local pending = {}
for i = 1, bucket_count do
    local key = KEYS[i]
    local rate_per_min = tonumber(ARGV[1 + i * 2])
    local capacity = tonumber(ARGV[2 + i * 2])

    -- rate 或 capacity 非正表示该层不限流，跳过。
    if rate_per_min > 0 and capacity > 0 then
        local rate_per_ms = rate_per_min / 60000
        local bucket = redis.call('HMGET', key, 'tokens', 'last_time')
        local tokens = tonumber(bucket[1])
        local last_time = tonumber(bucket[2])

        if not tokens or not last_time then
            tokens = capacity
        else
            local elapsed = now_ms - last_time
            if elapsed < 0 then
                -- Redis 时钟回拨：不补币也不惩罚，仅重置时间基准。
                elapsed = 0
            end
            tokens = math.min(capacity, tokens + elapsed * rate_per_ms)
        end

        if tokens < requested then
            -- 还差多少令牌，换算成毫秒。至少返回 1ms，避免客户端拿到 0 立刻重试。
            local deficit = requested - tokens
            local retry_after_ms = math.ceil(deficit / rate_per_ms)
            if retry_after_ms < 1 then
                retry_after_ms = 1
            end
            return {0, i, retry_after_ms}
        end

        pending[#pending + 1] = {
            key = key,
            tokens = tokens - requested,
            ttl = math.ceil(capacity / rate_per_min * 60) + 60,
        }
    end
end

-- 第二遍：全部桶都够，统一扣减。
for i = 1, #pending do
    local entry = pending[i]
    redis.call('HSET', entry.key, 'tokens', entry.tokens, 'last_time', now_ms)
    -- rate_limit.lua 把这行注释掉了，于是桶永不过期。按令牌建桶时每个被删掉的
    -- 令牌都会留下一个永久 hash，必须设 TTL。
    redis.call('EXPIRE', entry.key, entry.ttl)
end

return {1, 0, 0}
