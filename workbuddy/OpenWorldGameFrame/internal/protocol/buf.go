package protocol

import (
	"encoding/binary"
	"errors"
	"math"
)

// ErrShortBuffer 表示解码时数据不够长，通常是协议版本不一致。
var ErrShortBuffer = errors.New("protocol: short buffer")

// Writer 是一个只增不删的二进制写入器。
type Writer struct {
	buf []byte
}

// NewWriter 预分配 n 字节容量，减少扩容。
func NewWriter(n int) *Writer { return &Writer{buf: make([]byte, 0, n)} }

func (w *Writer) U8(v uint8)    { w.buf = append(w.buf, v) }
func (w *Writer) U16(v uint16)  { w.buf = binary.BigEndian.AppendUint16(w.buf, v) }
func (w *Writer) U32(v uint32)  { w.buf = binary.BigEndian.AppendUint32(w.buf, v) }
func (w *Writer) U64(v uint64)  { w.buf = binary.BigEndian.AppendUint64(w.buf, v) }
func (w *Writer) I32(v int32)   { w.U32(uint32(v)) }
func (w *Writer) I64(v int64)   { w.U64(uint64(v)) }
func (w *Writer) F32(v float32) { w.U32(math.Float32bits(v)) }

// String 写入 2 字节长度前缀 + UTF-8 内容，超长截断到 uint16 上限。
func (w *Writer) String(s string) {
	if len(s) > math.MaxUint16 {
		s = s[:math.MaxUint16]
	}
	w.U16(uint16(len(s)))
	w.buf = append(w.buf, s...)
}

// Bytes 返回底层切片，调用后不应再向 Writer 写入。
func (w *Writer) Bytes() []byte { return w.buf }

// Len 返回已写入字节数。
func (w *Writer) Len() int { return len(w.buf) }

// Reader 顺序读取二进制数据。任何一次越界都会记录错误，
// 后续读取返回零值，调用方在末尾统一检查 Err()。
type Reader struct {
	buf []byte
	off int
	err error
}

func NewReader(b []byte) *Reader { return &Reader{buf: b} }

func (r *Reader) fail() {
	if r.err == nil {
		r.err = ErrShortBuffer
	}
}

func (r *Reader) U8() uint8 {
	if r.off+1 > len(r.buf) {
		r.fail()
		return 0
	}
	v := r.buf[r.off]
	r.off++
	return v
}

func (r *Reader) U16() uint16 {
	if r.off+2 > len(r.buf) {
		r.fail()
		return 0
	}
	v := binary.BigEndian.Uint16(r.buf[r.off:])
	r.off += 2
	return v
}

func (r *Reader) U32() uint32 {
	if r.off+4 > len(r.buf) {
		r.fail()
		return 0
	}
	v := binary.BigEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v
}

func (r *Reader) U64() uint64 {
	if r.off+8 > len(r.buf) {
		r.fail()
		return 0
	}
	v := binary.BigEndian.Uint64(r.buf[r.off:])
	r.off += 8
	return v
}

func (r *Reader) I32() int32   { return int32(r.U32()) }
func (r *Reader) I64() int64   { return int64(r.U64()) }
func (r *Reader) F32() float32 { return math.Float32frombits(r.U32()) }

func (r *Reader) String() string {
	n := int(r.U16())
	if r.err != nil {
		return ""
	}
	if r.off+n > len(r.buf) {
		r.fail()
		return ""
	}
	s := string(r.buf[r.off : r.off+n])
	r.off += n
	return s
}

// Err 返回第一个解码错误。
func (r *Reader) Err() error { return r.err }

// Remaining 返回还没读的字节数。
func (r *Reader) Remaining() int { return len(r.buf) - r.off }
