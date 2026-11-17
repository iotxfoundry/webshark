package pcapng

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// pcapngBlock 流中单个块(body 不含首尾 total 长度字段).
type pcapngBlock struct {
	typ  uint32
	body []byte
}

// parsePcapng 解析小端 pcapng 字节流为块序列, 校验块长度自洽与 SHB magic
// (测试内自证落盘字节是合法 pcapng).
func parsePcapng(t *testing.T, raw []byte) []pcapngBlock {
	t.Helper()
	var blocks []pcapngBlock
	for i := 0; i < len(raw); {
		require.LessOrEqual(t, i+8, len(raw), "块头越界")
		typ := binary.LittleEndian.Uint32(raw[i:])
		total := int(binary.LittleEndian.Uint32(raw[i+4:]))
		require.GreaterOrEqual(t, total, 12, "块长下限 12B")
		require.LessOrEqual(t, i+total, len(raw), "块长越界")
		require.Equal(t, uint32(total), binary.LittleEndian.Uint32(raw[i+total-4:]), "尾部总长须等于头部")
		require.Zero(t, total%4, "块长须 4 字节对齐")
		blocks = append(blocks, pcapngBlock{typ: typ, body: raw[i+8 : i+total-4]})
		i += total
	}
	require.NotEmpty(t, blocks, "无块")
	require.Equal(t, uint32(blockSHB), blocks[0].typ, "首块须 SHB")
	require.Equal(t, uint32(0x1A2B3C4D), binary.LittleEndian.Uint32(blocks[0].body[0:4]), "小端 magic")
	return blocks
}

// idbInfo 接口块: linktype 与 if_name(缺省空).
func idbInfo(t *testing.T, b pcapngBlock) (capture.LinkType, string) {
	t.Helper()
	lt := capture.LinkType(binary.LittleEndian.Uint16(b.body[0:2]))
	name := ""
	for off := 8; off+4 <= len(b.body); {
		code := binary.LittleEndian.Uint16(b.body[off:])
		length := int(binary.LittleEndian.Uint16(b.body[off+2:]))
		if code == optEndOfOpt {
			break
		}
		if code == optIfName {
			name = string(b.body[off+4 : off+4+length])
		}
		off += 4 + length + pad4(length)
	}
	return lt, name
}

// epbInfo 包块: 接口号/时间戳/数据(caplen=origlen 校验).
func epbInfo(t *testing.T, b pcapngBlock) (uint16, time.Time, []byte) {
	t.Helper()
	iface := binary.LittleEndian.Uint32(b.body[0:4])
	micros := uint64(binary.LittleEndian.Uint32(b.body[4:8]))<<32 | uint64(binary.LittleEndian.Uint32(b.body[8:12]))
	caplen := int(binary.LittleEndian.Uint32(b.body[12:16]))
	require.Equal(t, binary.LittleEndian.Uint32(b.body[16:20]), uint32(caplen), "caplen 须等于 origlen")
	require.LessOrEqual(t, caplen, len(b.body)-20, "数据越界")
	return uint16(iface), time.Unix(int64(micros/1e6), int64(micros%1e6)*1e3), b.body[20 : 20+caplen]
}

// linkTypeEthernet libpcap DLT_EN10MB(1).
const linkTypeEthernet = capture.LinkType(1)

func TestWriterRoundTripDualInterface(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool", "cap") // 顺带验证 MkdirAll 深层目录
	w := NewWriter(dir)
	idEth, err := w.AddInterface(linkTypeEthernet, "eth0")
	require.NoError(t, err)
	require.Equal(t, uint16(0), idEth)
	idUser, err := w.AddInterface(capture.LinkType(147), "user0") // LINKTYPE_USER0
	require.NoError(t, err)
	require.Equal(t, uint16(1), idUser)

	base := time.Unix(1700000000, 0)
	type sentFrame struct {
		iface uint16
		ts    time.Time
		data  []byte
	}
	var sentFrames []sentFrame
	for i := range 8 {
		iface := uint16(i % 2)
		ts := base.Add(time.Duration(i) * time.Millisecond)
		data := make([]byte, 40+i)
		for j := range data {
			data[j] = byte(i*7 + j)
		}
		require.NoError(t, w.Write(iface, ts, data))
		sentFrames = append(sentFrames, sentFrame{iface, ts, data})
	}
	require.NoError(t, w.Close())

	files, err := filepath.Glob(filepath.Join(dir, "*.pcapng"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	raw, err := os.ReadFile(files[0])
	require.NoError(t, err)

	blocks := parsePcapng(t, raw)
	require.Len(t, blocks, 3+len(sentFrames), "SHB + 双 IDB + 8 EPB")
	lt, name := idbInfo(t, blocks[1])
	assert.Equal(t, linkTypeEthernet, lt)
	assert.Equal(t, "eth0", name)
	lt, name = idbInfo(t, blocks[2])
	assert.EqualValues(t, 147, lt)
	assert.Equal(t, "user0", name)
	for i, want := range sentFrames {
		iface, ts, data := epbInfo(t, blocks[3+i])
		assert.Equal(t, want.iface, iface, "第 %d 帧 iface", i)
		assert.Equal(t, want.ts.UnixNano(), ts.UnixNano(), "第 %d 帧 ts", i)
		assert.Equal(t, want.data, data, "第 %d 帧 data", i)
	}
}

func TestWriterLifecycle(t *testing.T) {
	w := NewWriter(t.TempDir())
	err := w.Write(0, time.Now(), []byte{1})
	assert.Error(t, err, "无接口写帧应报错")

	id, err := w.AddInterface(linkTypeEthernet, "eth0")
	require.NoError(t, err)
	require.Equal(t, uint16(0), id)

	ts := time.Unix(1700000000, 123)
	require.NoError(t, w.Write(0, ts, []byte("hello")))
	_, err = w.AddInterface(capture.LinkType(101), "late") // DLT_RAW
	assert.Error(t, err, "首包后 AddInterface 应报错")
	err = w.Write(9, ts, []byte("y"))
	assert.Error(t, err, "不存在的接口 id 应报错")

	assert.NoError(t, w.Close())
	assert.NoError(t, w.Close(), "Close 幂等")
	_, err = w.AddInterface(linkTypeEthernet, "x")
	assert.ErrorIs(t, err, ErrClosed)
	err = w.Write(0, ts, []byte("z"))
	assert.ErrorIs(t, err, ErrClosed)
}

func TestRollingNewValidation(t *testing.T) {
	_, err := NewRolling(t.TempDir(), 0, 3)
	assert.Error(t, err, "maxFileSize 必须为正")
	_, err = NewRolling(t.TempDir(), 1024, 0)
	assert.Error(t, err, "maxFiles 必须至少为 1")
}

func TestRollingRotationAndPrune(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRolling(dir, 256, 3)
	require.NoError(t, err)
	assert.Empty(t, w.Files(), "未写前无分片")
	_, err = w.AddInterface(linkTypeEthernet, "eth0")
	require.NoError(t, err)

	base := time.Unix(1700000000, 0)
	// 每帧 200B: SHB+IDB 64B + 单帧块 232B 即超 256B → 每个分片恰好一帧,
	// data[0] 即帧序号
	for i := range 5 {
		data := make([]byte, 200)
		for j := range data {
			data[j] = byte(i + j)
		}
		require.NoError(t, w.Write(0, base.Add(time.Duration(i)*time.Second), data))
	}

	// 只保留最新 3 个分片, 更旧的已从盘上删除
	files := w.Files()
	require.Len(t, files, 3)
	all, err := filepath.Glob(filepath.Join(dir, "*.pcapng"))
	require.NoError(t, err)
	assert.Len(t, all, 3, "盘上应只剩 maxFiles 个分片")

	wantSeq := []byte{2, 3, 4}
	for k, path := range files {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		blocks := parsePcapng(t, raw)
		require.Len(t, blocks, 3, "分片 %d: SHB + IDB + 单 EPB", k)
		_, ts, data := epbInfo(t, blocks[2])
		assert.Equal(t, int(wantSeq[k]), int(data[0]), "分片 %d 应为第 %d 帧", k, wantSeq[k])
		assert.Len(t, data, 200)
		assert.Equal(t, base.Add(time.Duration(wantSeq[k])*time.Second).UnixNano(), ts.UnixNano())
	}
	require.NoError(t, w.Close())
	require.NoError(t, w.Close(), "Close 幂等")
}

func TestRollingRebuildsInterfaces(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRolling(dir, 512, 2)
	require.NoError(t, err)
	_, err = w.AddInterface(linkTypeEthernet, "eth0")
	require.NoError(t, err)
	_, err = w.AddInterface(capture.LinkType(147), "user0")
	require.NoError(t, err)

	base := time.Unix(1700000000, 0)
	// 双接口头 88B + 200B 帧块 232B → 每分片装 2 帧(写满 512B 后滚), maxFiles=2
	// 恰好保两分片: 片 k 首帧序号 = 2k
	for i := range 4 {
		data := make([]byte, 200)
		for j := range data {
			data[j] = byte(i + j)
		}
		require.NoError(t, w.Write(uint16(i%2), base.Add(time.Duration(i)*time.Second), data))
	}
	require.NoError(t, w.Close())

	files := w.Files()
	require.Len(t, files, 2)
	for k, path := range files {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		blocks := parsePcapng(t, raw)
		require.Len(t, blocks, 5, "分片 %d: SHB + 双 IDB + 双 EPB(分片自含全部元数据)", k)
		lt, name := idbInfo(t, blocks[1])
		assert.Equal(t, linkTypeEthernet, lt, "分片 %d 接口 0", k)
		assert.Equal(t, "eth0", name)
		lt, name = idbInfo(t, blocks[2])
		assert.EqualValues(t, 147, lt, "分片 %d 接口 1", k)
		assert.Equal(t, "user0", name)
		// 每分片恰好两帧: iface 交替 0/1, data[0] 即帧序号
		for n := range 2 {
			iface, _, data := epbInfo(t, blocks[3+n])
			seq := uint16(2*k + n)
			assert.Equal(t, seq, uint16(data[0]), "分片 %d 第 %d 帧序号", k, n)
			assert.Equal(t, int(seq%2), int(iface), "分片 %d 第 %d 帧 iface", k, n)
		}
	}
}

// TestCanFrameBE can_id 主机序(LE) → 落盘大端换转: 仅前 4B, 其余原样.
func TestCanFrameBE(t *testing.T) {
	le := make([]byte, 16)
	binary.LittleEndian.PutUint32(le, 0x123) // can_id=0x123, 经典帧
	le[4] = 2
	le[8], le[9] = 0xaa, 0xbb
	be := CanFrameBE(le)
	assert.Equal(t, []byte{0, 0, 0x01, 0x23}, be[:4])
	assert.Equal(t, le[4:], be[4:])

	assert.Len(t, CanFrameBE([]byte{1, 2}), 2) // 不足 4B 原样返回(防御)
}

// TestLinkTypeConstants 常量锚: 147(USER0)/227(SocketCAN) 值域锁定.
func TestLinkTypeConstants(t *testing.T) {
	assert.EqualValues(t, 147, capture.LinkTypeUser0)
	assert.EqualValues(t, 227, capture.LinkTypeSocketCAN)
}
