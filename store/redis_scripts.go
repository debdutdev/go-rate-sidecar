package store

// Lua scripts for atomic rate limiting operations in Redis.
// Each algorithm has its own script so the entire check-and-update
// runs as a single atomic operation (no race conditions).
//
// All scripts use redis.call("TIME") for server-side timestamps
// to avoid clock drift between application servers.

// tokenBucketScript performs an atomic token bucket check-and-update.
//
// KEYS[1] = rate limit key
// ARGV[1] = rate (tokens per second)
// ARGV[2] = burst (bucket capacity)
// ARGV[3] = cost (tokens to consume)
//
// Returns: {allowed(0/1), remaining, retry_after_us, reset_at_us}
var tokenBucketScript = `
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])

-- Server-side time (seconds, microseconds)
local time_result = redis.call("TIME")
local now_us = tonumber(time_result[1]) * 1000000 + tonumber(time_result[2])

local data = redis.call("HMGET", key, "tokens", "last_us")
local tokens = tonumber(data[1])
local last_us = tonumber(data[2])

if tokens == nil then
    tokens = burst
    last_us = now_us
end

-- Lazy refill
local elapsed_s = (now_us - last_us) / 1000000.0
local refilled = math.min(burst, tokens + rate * elapsed_s)

local ttl_ms = math.ceil(burst / rate * 1000) + 1000

if refilled >= cost then
    local new_tokens = refilled - cost
    redis.call("HMSET", key, "tokens", new_tokens, "last_us", now_us)
    redis.call("PEXPIRE", key, ttl_ms)
    local remaining = math.floor(new_tokens)
    local reset_us = now_us + math.ceil((burst - new_tokens) / rate * 1000000)
    return {1, remaining, 0, reset_us}
else
    redis.call("HMSET", key, "tokens", refilled, "last_us", now_us)
    redis.call("PEXPIRE", key, ttl_ms)
    local remaining = math.floor(refilled)
    local retry_us = math.ceil((cost - refilled) / rate * 1000000)
    local reset_us = now_us + math.ceil(burst / rate * 1000000)
    return {0, remaining, now_us + retry_us, reset_us}
end
`

// leakyBucketScript performs an atomic leaky bucket check-and-update.
//
// KEYS[1] = rate limit key
// ARGV[1] = rate (drain rate per second)
// ARGV[2] = burst (bucket capacity)
// ARGV[3] = cost
//
// Returns: {allowed(0/1), remaining, retry_after_us, reset_at_us}
var leakyBucketScript = `
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])

local time_result = redis.call("TIME")
local now_us = tonumber(time_result[1]) * 1000000 + tonumber(time_result[2])

local data = redis.call("HMGET", key, "queue", "last_us")
local queue = tonumber(data[1]) or 0
local last_us = tonumber(data[2])

if last_us ~= nil then
    local elapsed_s = (now_us - last_us) / 1000000.0
    local leaked = rate * elapsed_s
    queue = math.max(0, queue - leaked)
end

local ttl_ms = math.ceil(burst / rate * 1000) + 1000

if queue + cost <= burst then
    local new_queue = queue + cost
    redis.call("HMSET", key, "queue", new_queue, "last_us", now_us)
    redis.call("PEXPIRE", key, ttl_ms)
    local remaining = burst - math.ceil(new_queue)
    local reset_us = now_us + math.ceil(new_queue / rate * 1000000)
    return {1, remaining, 0, reset_us}
else
    redis.call("HMSET", key, "queue", queue, "last_us", now_us)
    redis.call("PEXPIRE", key, ttl_ms)
    local retry_us = math.ceil((queue + cost - burst) / rate * 1000000)
    local reset_us = now_us + math.ceil(queue / rate * 1000000)
    return {0, 0, now_us + retry_us, reset_us}
end
`

// fixedWindowScript performs an atomic fixed window counter increment.
//
// KEYS[1] = rate limit key
// ARGV[1] = window_ms (window duration in milliseconds)
// ARGV[2] = limit (max requests per window)
// ARGV[3] = cost
//
// Returns: {allowed(0/1), remaining, current_count, window_start_us, prev_count}
var fixedWindowScript = `
local key = KEYS[1]
local window_ms = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])

local time_result = redis.call("TIME")
local now_us = tonumber(time_result[1]) * 1000000 + tonumber(time_result[2])
local window_us = window_ms * 1000
local window_start = math.floor(now_us / window_us) * window_us

local data = redis.call("HMGET", key, "count", "window_start", "prev_count")
local count = tonumber(data[1]) or 0
local stored_start = tonumber(data[2]) or 0
local prev_count = tonumber(data[3]) or 0

-- Window rollover
if stored_start ~= window_start then
    prev_count = count
    count = 0
end

local ttl_ms = window_ms * 2

if count + cost <= limit then
    count = count + cost
    redis.call("HMSET", key, "count", count, "window_start", window_start, "prev_count", prev_count)
    redis.call("PEXPIRE", key, ttl_ms)
    local remaining = limit - count
    return {1, remaining, count, window_start, prev_count}
else
    redis.call("HMSET", key, "count", count, "window_start", window_start, "prev_count", prev_count)
    redis.call("PEXPIRE", key, ttl_ms)
    return {0, 0, count, window_start, prev_count}
end
`

// slidingWindowLogScript performs an atomic sliding window log check.
//
// KEYS[1] = sorted set key
// ARGV[1] = window_ms
// ARGV[2] = limit
// ARGV[3] = cost
//
// Uses a Redis sorted set with timestamp as both score and member.
// Returns: {allowed(0/1), remaining, oldest_us}
var slidingWindowLogScript = `
local key = KEYS[1]
local window_ms = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])

local time_result = redis.call("TIME")
local now_us = tonumber(time_result[1]) * 1000000 + tonumber(time_result[2])
local cutoff_us = now_us - window_ms * 1000

-- Remove expired entries
redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff_us)

local count = redis.call("ZCARD", key)

if count + cost <= limit then
    -- Add new timestamps (use now_us + i to ensure unique members)
    for i = 0, cost - 1 do
        redis.call("ZADD", key, now_us, tostring(now_us) .. ":" .. tostring(i))
    end
    redis.call("PEXPIRE", key, window_ms + 1000)
    local remaining = limit - count - cost
    return {1, remaining, 0}
else
    redis.call("PEXPIRE", key, window_ms + 1000)
    -- Get oldest entry for retry-after calculation
    local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
    local oldest_us = 0
    if #oldest > 0 then
        oldest_us = tonumber(oldest[2])
    end
    return {0, 0, oldest_us}
end
`

// slidingWindowCounterScript performs an atomic sliding window counter check.
//
// KEYS[1] = rate limit key
// ARGV[1] = window_ms
// ARGV[2] = limit
// ARGV[3] = cost
//
// Returns: {allowed(0/1), remaining, window_start_us}
var slidingWindowCounterScript = `
local key = KEYS[1]
local window_ms = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])

local time_result = redis.call("TIME")
local now_us = tonumber(time_result[1]) * 1000000 + tonumber(time_result[2])
local window_us = window_ms * 1000
local window_start = math.floor(now_us / window_us) * window_us

local data = redis.call("HMGET", key, "count", "window_start", "prev_count")
local count = tonumber(data[1]) or 0
local stored_start = tonumber(data[2]) or 0
local prev_count = tonumber(data[3]) or 0

-- Window rollover
if stored_start ~= window_start then
    if stored_start == window_start - window_us then
        prev_count = count
    else
        prev_count = 0
    end
    count = 0
end

-- Weighted estimate
local elapsed_us = now_us - window_start
local weight = 1.0 - (elapsed_us / window_us)
local estimated = prev_count * weight + count

local ttl_ms = window_ms * 2

if estimated + cost <= limit then
    count = count + cost
    redis.call("HMSET", key, "count", count, "window_start", window_start, "prev_count", prev_count)
    redis.call("PEXPIRE", key, ttl_ms)
    local new_estimated = prev_count * weight + count
    local remaining = math.floor(limit - new_estimated)
    if remaining < 0 then remaining = 0 end
    return {1, remaining, window_start}
else
    redis.call("HMSET", key, "count", count, "window_start", window_start, "prev_count", prev_count)
    redis.call("PEXPIRE", key, ttl_ms)
    return {0, 0, window_start}
end
`
