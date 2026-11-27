local stamp = redis.call('TIME')
local now = tonumber(stamp[1]) * 1000 + math.floor(tonumber(stamp[2]) / 1000)
local function available(key, rate, burst)
  local saved = redis.call('HMGET', key, 'tokens', 'at')
  if not saved[1] then return burst, now end
  local previous = tonumber(saved[2])
  local effective = math.max(now, previous)
  return math.min(burst, tonumber(saved[1]) + (effective - previous) * rate / 1000), effective
end
local rates = {tonumber(ARGV[1]), tonumber(ARGV[3])}
local bursts = {tonumber(ARGV[2]), tonumber(ARGV[4])}
local balance, updated = {}, {}
for i = 1, 2 do
  balance[i], updated[i] = available(KEYS[i], rates[i], bursts[i])
  if balance[i] < 1 then return 0 end
end
for i = 1, 2 do
  redis.call('HSET', KEYS[i], 'tokens', string.format('%.17g', balance[i] - 1), 'at', updated[i])
  redis.call('PEXPIRE', KEYS[i], math.ceil(bursts[i] * 1000 / rates[i]))
end
return 1
