class_name OWWriter
extends RefCounted
## 二进制写入器，逐字节对齐 Go `internal/protocol/buf.go` 的 Writer。
##
## 全部大端：Go 用 binary.BigEndian，Godot 的 StreamPeerBuffer 默认小端，
## 必须显式 `big_endian = true`。String 是 u16 长度前缀 + UTF-8（Go 的
## Writer.String 用 2 字节，不是 StreamPeer.put_utf8_string 的 4 字节）。
##
## 有符号数用 put_32/put_64（补码），和 Go 的 I32/I64 = U32/U64(uint32/64(v))
## 的字节表示完全一致。

var _buf: StreamPeerBuffer

func _init() -> void:
	_buf = StreamPeerBuffer.new()
	_buf.big_endian = true

func u8(v: int) -> OWWriter:
	_buf.put_u8(v)
	return self

func u16(v: int) -> OWWriter:
	_buf.put_u16(v)
	return self

func u32(v: int) -> OWWriter:
	_buf.put_u32(v)
	return self

func u64(v: int) -> OWWriter:
	_buf.put_u64(v)
	return self

func i32(v: int) -> OWWriter:
	_buf.put_32(v)
	return self

func i64(v: int) -> OWWriter:
	_buf.put_64(v)
	return self

func f32(v: float) -> OWWriter:
	_buf.put_float(v)
	return self

func string(s: String) -> OWWriter:
	var raw: PackedByteArray = s.to_utf8_buffer()
	var n: int = mini(raw.size(), 65535)
	_buf.put_u16(n)
	if n > 0:
		_buf.put_data(raw.slice(0, n))
	return self

func bytes() -> PackedByteArray:
	return _buf.data_array

func size() -> int:
	return _buf.get_size()
