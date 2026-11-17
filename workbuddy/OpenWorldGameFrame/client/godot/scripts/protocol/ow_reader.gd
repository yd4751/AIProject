class_name OWReader
extends RefCounted
## 二进制读取器，对齐 Go `internal/protocol/buf.go` 的 Reader。
##
## Go 的 Reader 在越界时记录一次错误并让后续读取返回零值。这里用
## `failed` 标志达到同样效果：任何一次读取前检查剩余字节，不够就置位，
## 调用方在 decode 末尾统一判断（对齐 Go 的 `r.Err()`）。

var _buf: StreamPeerBuffer
var failed: bool = false

func _init(b: PackedByteArray) -> void:
	_buf = StreamPeerBuffer.new()
	_buf.big_endian = true
	_buf.data_array = b

func _has(n: int) -> bool:
	if _buf.get_available_bytes() < n:
		failed = true
		return false
	return true

func u8() -> int:
	if not _has(1):
		return 0
	return _buf.get_u8()

func u16() -> int:
	if not _has(2):
		return 0
	return _buf.get_u16()

func u32() -> int:
	if not _has(4):
		return 0
	return _buf.get_u32()

func u64() -> int:
	if not _has(8):
		return 0
	return _buf.get_u64()

func i32() -> int:
	if not _has(4):
		return 0
	return _buf.get_32()

func i64() -> int:
	if not _has(8):
		return 0
	return _buf.get_64()

func f32() -> float:
	if not _has(4):
		return 0.0
	return _buf.get_float()

func string() -> String:
	var n := u16()
	if failed or n == 0:
		return ""
	if not _has(n):
		return ""
	var ret: Array = _buf.get_data(n)
	if ret[0] != OK:
		failed = true
		return ""
	var raw: PackedByteArray = ret[1]
	return raw.get_string_from_utf8()

func remaining() -> int:
	return _buf.get_available_bytes()
