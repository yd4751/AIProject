class_name OWAuth
extends RefCounted
## 无状态登录令牌，对齐 Go `pkg/authtoken/token.go` 的 Sign。
##
##   token = playerID + ":" + unixTs + ":" + hex(HMAC_SHA256(secret, playerID+":"+unixTs))
##
## 开发模式下客户端自己签发（secret 和服务端 -secret 一致即可），不需要
## 独立登录服。生产环境应由登录服签发、客户端只做透传——见 README。
##
## 服务端验签有 TTL（默认 5 分钟）且允许 1 分钟时钟偏移，所以客户端必须
## 用系统当前 Unix 秒，不能写死时间戳。

static func sign_token(secret: String, player_id: String, unix_ts: int) -> String:
	var payload := player_id + ":" + str(unix_ts)
	var hmac := HMACContext.new()
	var err: int = hmac.start(HashingContext.HASH_SHA256, secret.to_utf8_buffer())
	if err != OK:
		push_error("OWAuth: HMAC start failed: %d" % err)
		return ""
	hmac.update(payload.to_utf8_buffer())
	var mac: PackedByteArray = hmac.finish()
	return payload + ":" + mac.hex_encode()

## 便捷入口：用当前系统时间签发。
static func sign_now(secret: String, player_id: String) -> String:
	return sign_token(secret, player_id, int(Time.get_unix_time_from_system()))
