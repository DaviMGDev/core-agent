// Package wasmtest builds minimal hand-encoded wasm guests for host tests:
// a job-start guest that reads its stashed result back, and a publish guest.
package wasmtest

import "bytes"

// EncodeLEB128U encodes an unsigned LEB128 value.
func EncodeLEB128U(val uint32) []byte {
	var res []byte
	for {
		b := byte(val & 0x7f)
		val >>= 7
		if val != 0 {
			res = append(res, b|0x80)
		} else {
			res = append(res, b)
			break
		}
	}
	return res
}

// EncodeLEB128S encodes a signed LEB128 value.
func EncodeLEB128S(val int32) []byte {
	var res []byte
	for more := true; more; {
		b := byte(val & 0x7f)
		val >>= 7
		if (val == 0 && (b&0x40) == 0) || (val == -1 && (b&0x40) != 0) {
			more = false
		} else {
			b |= 0x80
		}
		res = append(res, b)
	}
	return res
}

// EncodeVec encodes a wasm vector of byte blobs.
func EncodeVec(items [][]byte) []byte {
	var buf bytes.Buffer
	buf.Write(EncodeLEB128U(uint32(len(items))))
	for _, it := range items {
		buf.Write(it)
	}
	return buf.Bytes()
}

// EncodeSection encodes one wasm section.
func EncodeSection(secID byte, content []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(secID)
	buf.Write(EncodeLEB128U(uint32(len(content))))
	buf.Write(content)
	return buf.Bytes()
}

// EncodeString encodes a wasm name.
func EncodeString(s string) []byte {
	b := []byte(s)
	return append(EncodeLEB128U(uint32(len(b))), b...)
}

// I32Const encodes an i32.const instruction.
func I32Const(v int32) []byte {
	return append([]byte{0x41}, EncodeLEB128S(v)...)
}

// ActiveData encodes an active data segment at a fixed offset.
func ActiveData(offset int32, s string) []byte {
	b := append([]byte{0x00}, I32Const(offset)...)
	b = append(b, 0x0b)
	b = append(b, EncodeLEB128U(uint32(len(s)))...)
	return append(b, []byte(s)...)
}

// Import encodes a memento host import of the given type index.
func Import(name string, typeIndex byte) []byte {
	return append(EncodeString("memento"), append(EncodeString(name), 0x00, typeIndex)...)
}

// JobGuest builds a guest whose memento_activate starts a job with the
// request document at offset 32 and reads the stashed result document back
// into offset 4096, logging it; on failure it logs "FAIL".
func JobGuest(request string, respMax uint32) []byte {
	type0 := []byte{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f} // (i32,i32)->i32
	type1 := []byte{0x60, 0x00, 0x01, 0x7f}             // ()->i32
	type2 := []byte{0x60, 0x01, 0x7f, 0x01, 0x7f}       // (i32)->i32
	typeSec := EncodeSection(1, EncodeVec([][]byte{type0, type1, type2}))

	importSec := EncodeSection(2, EncodeVec([][]byte{
		Import("job_start", 0x00),
		Import("job_result_len", 0x01),
		Import("job_result", 0x00),
		Import("log", 0x00),
	}))

	// Defined funcs 4..5: activate (type1), revert (type2).
	funcSec := EncodeSection(3, EncodeVec([][]byte{{0x01}, {0x02}}))
	memSec := EncodeSection(5, EncodeVec([][]byte{{0x00, 0x01}}))
	exportSec := EncodeSection(7, EncodeVec([][]byte{
		append(EncodeString("memory"), 0x02, 0x00),
		append(EncodeString("memento_activate"), 0x00, 0x04),
		append(EncodeString("memento_revert_effect"), 0x00, 0x05),
	}))

	body := []byte{0x01, 0x02, 0x7f} // two i32 locals: n, got
	body = append(body, I32Const(32)...)
	body = append(body, I32Const(int32(len(request)))...)
	body = append(body, 0x10, 0x00) // job_start(32, len)
	body = append(body, 0x45)       // i32.eqz
	body = append(body, 0x04, 0x40) // if
	body = append(body, 0x10, 0x01) // job_result_len()
	body = append(body, 0x21, 0x00) // local.set 0 (n)
	body = append(body, I32Const(4096)...)
	body = append(body, 0x20, 0x00)
	body = append(body, I32Const(int32(respMax))...)
	body = append(body, 0x20, 0x00)
	body = append(body, I32Const(int32(respMax))...)
	body = append(body, 0x49)       // i32.lt_u
	body = append(body, 0x1b)       // select -> min(n, respMax)
	body = append(body, 0x10, 0x02) // job_result(4096, min)
	body = append(body, 0x21, 0x01) // local.set 1 (got)
	body = append(body, I32Const(4096)...)
	body = append(body, 0x20, 0x01)
	body = append(body, 0x10, 0x03) // log(4096, got)
	body = append(body, 0x1a)       // drop
	body = append(body, 0x05)       // else
	body = append(body, I32Const(16)...)
	body = append(body, I32Const(4)...)
	body = append(body, 0x10, 0x03) // log(16, 4) -> "FAIL"
	body = append(body, 0x1a)       // drop
	body = append(body, 0x0b)       // end if
	body = append(body, I32Const(0)...)
	body = append(body, 0x0f, 0x0b) // return

	revert := []byte{0x00}
	revert = append(revert, I32Const(0)...)
	revert = append(revert, 0x0f, 0x0b)

	codeSec := EncodeSection(10, EncodeVec([][]byte{
		append(EncodeLEB128U(uint32(len(body))), body...),
		append(EncodeLEB128U(uint32(len(revert))), revert...),
	}))

	dataSec := EncodeSection(11, EncodeVec([][]byte{
		ActiveData(16, "FAIL"),
		ActiveData(32, request),
	}))

	var wasm bytes.Buffer
	wasm.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	wasm.Write(typeSec)
	wasm.Write(importSec)
	wasm.Write(funcSec)
	wasm.Write(memSec)
	wasm.Write(exportSec)
	wasm.Write(codeSec)
	wasm.Write(dataSec)
	return wasm.Bytes()
}

// PublishGuest builds a guest whose activation publishes one event: topic at
// offset 32, payload at 64.
func PublishGuest(topic, payload string) []byte {
	type0 := []byte{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f}             // (i32,i32)->i32
	type3 := []byte{0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f} // (i32,i32,i32,i32)->i32
	type2 := []byte{0x60, 0x01, 0x7f, 0x01, 0x7f}                   // (i32)->i32
	type1 := []byte{0x60, 0x00, 0x01, 0x7f}                         // ()->i32
	typeSec := EncodeSection(1, EncodeVec([][]byte{type0, type3, type2, type1}))

	importSec := EncodeSection(2, EncodeVec([][]byte{Import("publish", 0x01)}))

	// Defined funcs 1..2: activate (type1), revert (type2).
	funcSec := EncodeSection(3, EncodeVec([][]byte{{0x03}, {0x02}}))
	memSec := EncodeSection(5, EncodeVec([][]byte{{0x00, 0x01}}))
	exportSec := EncodeSection(7, EncodeVec([][]byte{
		append(EncodeString("memory"), 0x02, 0x00),
		append(EncodeString("memento_activate"), 0x00, 0x01),
		append(EncodeString("memento_revert_effect"), 0x00, 0x02),
	}))

	body := []byte{0x00}
	body = append(body, I32Const(32)...)
	body = append(body, I32Const(int32(len(topic)))...)
	body = append(body, I32Const(64)...)
	body = append(body, I32Const(int32(len(payload)))...)
	body = append(body, 0x10, 0x00) // publish(32, topicLen, 64, payloadLen)
	body = append(body, 0x1a)       // drop
	body = append(body, I32Const(0)...)
	body = append(body, 0x0f, 0x0b) // return

	revert := []byte{0x00}
	revert = append(revert, I32Const(0)...)
	revert = append(revert, 0x0f, 0x0b)

	codeSec := EncodeSection(10, EncodeVec([][]byte{
		append(EncodeLEB128U(uint32(len(body))), body...),
		append(EncodeLEB128U(uint32(len(revert))), revert...),
	}))

	dataSec := EncodeSection(11, EncodeVec([][]byte{
		ActiveData(32, topic),
		ActiveData(64, payload),
	}))

	var wasm bytes.Buffer
	wasm.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	wasm.Write(typeSec)
	wasm.Write(importSec)
	wasm.Write(funcSec)
	wasm.Write(memSec)
	wasm.Write(exportSec)
	wasm.Write(codeSec)
	wasm.Write(dataSec)
	return wasm.Bytes()
}
