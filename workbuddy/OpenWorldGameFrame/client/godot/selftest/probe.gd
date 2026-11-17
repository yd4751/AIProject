extends SceneTree
## 一次性 API 探针：确认 Godot 4.7 有客户端框架需要的三块能力。
## 运行：godot --headless --path client/godot --script res://selftest/probe.gd

func _initialize() -> void:
	print("=== Godot API probe ===")

	# 1. HMAC-SHA256 —— 签发登录 token 必需（对齐 Go 的 authtoken.Sign）
	var hmac := HMACContext.new()
	var e: int = hmac.start(HashingContext.HASH_SHA256, "dev-secret-change-me".to_utf8_buffer())
	print("hmac.start err=", e, " (0=OK)")
	hmac.update("player1:1730000000".to_utf8_buffer())
	var mac: PackedByteArray = hmac.finish()
	print("mac len=", mac.size(), " hex=", mac.hex_encode())

	# 2. StreamPeerBuffer 大端编解码 —— 对齐 Go 的 binary.BigEndian
	var w := StreamPeerBuffer.new()
	w.big_endian = true
	w.put_u32(0x01020304)
	w.put_float(1.5)
	w.put_u64(0x0102030405060708)
	w.put_u8(0xAB)
	var data: PackedByteArray = w.data_array
	print("writer bytes=", data.hex_encode(), " size=", data.size())

	var r := StreamPeerBuffer.new()
	r.big_endian = true
	r.data_array = data
	print("read u32=0x%08X float=%f u64=0x%016X u8=0x%02X" % [
		r.get_u32(), r.get_float(), r.get_u64(), r.get_u8()
	])

	# 3. StreamPeerTCP —— 网络连接
	var tcp := StreamPeerTCP.new()
	print("tcp status=", tcp.get_status(), " (0=STATUS_NONE)")
	print("=== probe done ===")
	quit()
