package rediscache

const appendMessageLua = `
local idsKey = KEYS[1]
local messagesKey = KEYS[2]
local messageID = ARGV[1]
local messageJSON = ARGV[2]
local maxMessages = tonumber(ARGV[3])
local ttlSeconds = tonumber(ARGV[4])

redis.call("HSET", messagesKey, messageID, messageJSON)
redis.call("RPUSH", idsKey, messageID)

while redis.call("LLEN", idsKey) > maxMessages do
  local oldID = redis.call("LPOP", idsKey)
  if oldID then
    redis.call("HDEL", messagesKey, oldID)
  end
end

if ttlSeconds and ttlSeconds > 0 then
  redis.call("EXPIRE", idsKey, ttlSeconds)
  redis.call("EXPIRE", messagesKey, ttlSeconds)
end

return redis.call("LLEN", idsKey)
`

const deleteLastIfExpectedLua = `
local idsKey = KEYS[1]
local messagesKey = KEYS[2]
local expectedID = ARGV[1]
local ttlSeconds = tonumber(ARGV[2])

local lastID = redis.call("LINDEX", idsKey, -1)
if not lastID or lastID ~= expectedID then
  return 0
end

redis.call("RPOP", idsKey)
redis.call("HDEL", messagesKey, expectedID)

if ttlSeconds and ttlSeconds > 0 then
  redis.call("EXPIRE", idsKey, ttlSeconds)
  redis.call("EXPIRE", messagesKey, ttlSeconds)
end

return 1
`
