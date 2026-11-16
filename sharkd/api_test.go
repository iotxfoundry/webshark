package sharkd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// envResult 生成按序应答的 handler：原样返回实测响应串（不经过 map 重编码，
// 保证解码路径与真实 sharkd 字节一致）。样本取自本机 sharkd 4.6.9 实测。
func envResult(raw string) func(map[string]any) any {
	return func(req map[string]any) any {
		return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": json.RawMessage(raw)}
	}
}

// expectParams 生成应答前断言请求 params 的 handler（want 为 nil 表示 params 须为空）。
func expectParams(t *testing.T, want map[string]any, raw string) func(map[string]any) any {
	t.Helper()
	return func(req map[string]any) any {
		var got map[string]any
		if p, ok := req["params"].(map[string]any); ok {
			got = p
		}
		require.Equal(t, want, got)
		return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": json.RawMessage(raw)}
	}
}

func TestAPILoad(t *testing.T) {
	f := newFakeSharkd(t,
		expectParams(t, map[string]any{"file": "/tmp/a.pcapng"}, `{"status":"OK"}`), // 零值 max_packets/max_bytes 不发
		expectParams(t, map[string]any{"file": "/tmp/a.pcapng", "max_packets": float64(100)}, `{"status":"Input/output error","err":5}`),
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.Load(context.Background(), LoadParams{File: "/tmp/a.pcapng"})
	require.NoError(t, err)
	require.Equal(t, "OK", res.Status)

	_, err = c.Load(context.Background(), LoadParams{File: "/tmp/a.pcapng", MaxPackets: 100})
	require.ErrorContains(t, err, "Input/output error") // result 体 err != 0 → error
	require.ErrorContains(t, err, "err=5")
}

func TestAPIInfo(t *testing.T) {
	f := newFakeSharkd(t, envResult(`{"version":"4.6.9","columns":[{"name":"No.","format":"%m"},{"name":"Time","format":"%t"}],"stats":[{"name":"IPv4 Statistics","tap":"ip:0"}],"nstat":[],"convs":[],"seqa":[],"taps":[{"name":"Expert Information","tap":"expert"}],"eo":[],"srt":[],"rtd":[],"follow":[],"capture_types":[{"name":"pcapng-file","description":"pcapng file"}],"encap_types":[{"name":"Ethernet","description":"Ethernet"}],"ftypes":["FT_NONE","FT_STRING"]}`))
	c := NewClient(f.ln.Addr().String())
	info, err := c.Info(context.Background())
	require.NoError(t, err)
	require.Equal(t, "4.6.9", info.Version)
	require.Equal(t, "No.", info.Columns[0].Name)
	require.Equal(t, "%t", info.Columns[1].Format)
	require.Equal(t, "ip:0", info.Stats[0].Tap)
	require.Equal(t, "expert", info.Taps[0].Tap)
	require.Equal(t, "pcapng file", info.CaptureTypes[0].Description)
	require.Equal(t, "Ethernet", info.EncapTypes[0].Name)
	require.Equal(t, []string{"FT_NONE", "FT_STRING"}, info.Ftypes)
}

func TestAPIStatus(t *testing.T) {
	// 实测：visible 是 JSON 布尔
	f := newFakeSharkd(t, envResult(`{"frames":4,"duration":0.070345,"filename":"dhcp.pcapng","filesize":1508,"columns":["No.","Time"],"column_info":[{"title":"No.","format":"%m","visible":true,"display":"R"},{"title":"Time","format":"%Aut","visible":true,"display":"R"}]}`))
	c := NewClient(f.ln.Addr().String())
	res, err := c.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint32(4), res.Frames)
	require.InDelta(t, 0.070345, res.Duration, 1e-9)
	require.Equal(t, "dhcp.pcapng", res.Filename)
	require.Equal(t, int64(1508), res.Filesize)
	require.Equal(t, []string{"No.", "Time"}, res.Columns)
	require.True(t, res.ColumnInfo[0].Visible)
	require.Equal(t, "R", res.ColumnInfo[1].Display)
}

func TestAPIAnalyse(t *testing.T) {
	f := newFakeSharkd(t, envResult(`{"frames":4,"protocols":["frame","eth","ethertype","ip","udp","dhcp"],"first":1102274184.317453,"last":1102274184.387798}`))
	c := NewClient(f.ln.Addr().String())
	res, err := c.Analyse(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint32(4), res.Frames)
	require.Equal(t, []string{"frame", "eth", "ethertype", "ip", "udp", "dhcp"}, res.Protocols)
	require.InDelta(t, 1102274184.317453, res.First, 1e-6)
}

func TestAPIField(t *testing.T) {
	// 实测：id/parent_id 是字符串
	f := newFakeSharkd(t, envResult(`{"id":"86870","parent_id":"86854","name":"ip.src","display_name":"Source Address","type":"FT_IPv4"}`))
	c := NewClient(f.ln.Addr().String())
	fi, err := c.Field(context.Background(), FieldParams{Name: "ip.src"})
	require.NoError(t, err)
	require.Equal(t, "86870", fi.ID)
	require.Equal(t, "86854", fi.ParentID)
	require.Equal(t, "ip.src", fi.Name)
	require.Equal(t, "Source Address", fi.DisplayName)
	require.Equal(t, "FT_IPv4", fi.Type)
}

func TestAPIFields(t *testing.T) {
	f := newFakeSharkd(t, envResult(`{"fields":[{"id":"244159","parent_id":"-1","name":"_ws.lua","display_name":"Lua Dissection","type":"FT_PROTOCOL"},{"id":"244160","parent_id":"244159","name":"_ws.lua.name","display_name":"Name","type":"FT_STRING"}]}`))
	c := NewClient(f.ln.Addr().String())
	res, err := c.Fields(context.Background())
	require.NoError(t, err)
	require.Len(t, res.Fields, 2)
	require.Equal(t, "-1", res.Fields[0].ParentID)
	require.Equal(t, "244160", res.Fields[1].ID)
}

func TestAPIFrames(t *testing.T) {
	rows := `[{"c":["1","23:57:30.0000010Z","10.0.0.1","10.0.0.2","ICMP","74","Echo (ping) request  id=0x0001, seq=1/256, ttl=64 (no response found!)"],"num":1,"bg":"fce0ff","fg":"12272e"},{"c":["2"],"num":2,"comments":["hello"],"ct":true,"i":true,"m":true}]`
	f := newFakeSharkd(t,
		expectParams(t, map[string]any{"skip": float64(1), "limit": float64(2), "filter": "icmp"}, rows),
		// Columns 展开为 column0/column1（元素为列格式编号或自定义 "dfilter:occurrence"）+ refs
		expectParams(t, map[string]any{"limit": float64(1), "column0": "0", "column1": "frame.time_epoch:0", "refs": "1"}, `[{"c":["1","1102274184.317453000"],"num":1}]`),
		expectParams(t, nil, rows), // 全零值 → 不发 params
	)
	c := NewClient(f.ln.Addr().String())
	raw, err := c.Frames(context.Background(), FramesParams{Skip: 1, Limit: 2, Filter: "icmp"})
	require.NoError(t, err)
	require.Len(t, raw, 2)
	decoded, err := DecodeFrameRows(raw)
	require.NoError(t, err)
	require.Equal(t, int64(1), decoded[0].Num)
	require.Equal(t, "fce0ff", decoded[0].Bg)
	require.Equal(t, "12272e", decoded[0].Fg)
	require.Equal(t, "ICMP", decoded[0].C[4])
	require.False(t, decoded[0].I)
	require.True(t, decoded[1].I)
	require.True(t, decoded[1].M)
	require.True(t, decoded[1].CT)
	require.Equal(t, []string{"hello"}, decoded[1].Comments)

	_, err = c.Frames(context.Background(), FramesParams{Limit: 1, Columns: []string{"0", "frame.time_epoch:0"}, Refs: "1"})
	require.NoError(t, err)
}

func TestAPIFrame(t *testing.T) {
	f := newFakeSharkd(t,
		// 布尔开关只传 true 的；Frame 必发
		expectParams(t, map[string]any{"frame": float64(1), "proto": true, "columns": true, "bytes": true}, `{"tree":[{"l":"Frame 1: 74 bytes on wire (592 bits), 74 bytes captured","h":[0,74],"n":[{"l":"Ethernet II","h":[0,14],"n":[{"l":"Destination: 10:00:00:00:00:02","h":[0,6],"e":2,"t":"str"}]}]},{"l":"Internet Control Message Protocol","h":[34,8],"f":"icmp","fn":"icmp.type","e":8,"t":"uint","ds":2,"fnum":1,"url":"https://example.com","s":"Warn","g":true,"v":true,"p":[34,4]}],"bytes":"AQIDBAUGBw==","col":["1","ICMP"],"comment":["hello"],"i":true,"bg":"fce0ff","fg":"12272e","fol":[],"followers":[]}`),
	)
	c := NewClient(f.ln.Addr().String())
	fr, err := c.Frame(context.Background(), FrameParams{Frame: 1, Proto: true, Columns: true, Bytes: true})
	require.NoError(t, err)
	require.Len(t, fr.Tree, 2)
	require.Contains(t, fr.Tree[0].L, "Frame 1")
	require.Equal(t, "icmp", fr.Tree[1].F)
	require.Equal(t, "icmp.type", fr.Tree[1].FN)
	require.Equal(t, int64(2), fr.Tree[0].N[0].N[0].E)
	require.Equal(t, int64(1), fr.Tree[1].FNum)
	require.True(t, fr.Tree[1].G)
	require.True(t, fr.Tree[1].V) // 隐藏字段标记
	require.Equal(t, []int{34, 4}, fr.Tree[1].P)
	require.Equal(t, []byte{1, 2, 3, 4, 5, 6, 7}, fr.Bytes)
	require.Equal(t, []string{"1", "ICMP"}, fr.Columns)
	require.Equal(t, []string{"hello"}, fr.Comments)
	require.True(t, fr.I)
	require.Equal(t, "fce0ff", fr.Bg)
	require.Equal(t, json.RawMessage("[]"), fr.Fol)
}

func TestAPICheck(t *testing.T) {
	f := newFakeSharkd(t,
		expectParams(t, map[string]any{"filter": "icmp"}, `{"status":"OK"}`),
		expectParams(t, map[string]any{"filter": "icmp &&"}, `{"status":"Warning","warning":"Filter contains deprecated tokens"}`),
		envResult(`{"status":"OK"}`), // field 分支同样返回 status
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.Check(context.Background(), CheckParams{Filter: "icmp"})
	require.NoError(t, err)
	require.Equal(t, "OK", res.Status)
	require.Empty(t, res.Warning)

	res, err = c.Check(context.Background(), CheckParams{Filter: "icmp &&"})
	require.NoError(t, err)
	require.Equal(t, "Warning", res.Status)
	require.Equal(t, "Filter contains deprecated tokens", res.Warning)

	res, err = c.Check(context.Background(), CheckParams{Field: "dhcp"})
	require.NoError(t, err)
	require.Equal(t, "OK", res.Status)
}

func TestAPIComplete(t *testing.T) {
	f := newFakeSharkd(t,
		envResult(`{"field":[{"f":"icmp","t":1,"n":"Internet Control Message Protocol"}]}`),
		envResult(`{"pref":[{"f":"gui.console_open","d":"Open a console window"}]}`),
	)
	c := NewClient(f.ln.Addr().String())
	cs, err := c.Complete(context.Background(), CompleteParams{Field: "icmp"})
	require.NoError(t, err)
	require.Equal(t, "icmp", cs.Field[0].F)
	require.Equal(t, 1, cs.Field[0].T)
	require.Equal(t, "Internet Control Message Protocol", cs.Field[0].N)
	cs, err = c.Complete(context.Background(), CompleteParams{Pref: "gui"})
	require.NoError(t, err)
	require.Equal(t, "gui.console_open", cs.Pref[0].F)
	require.Equal(t, "Open a console window", cs.Pref[0].D)
}

func TestAPIFollow(t *testing.T) {
	f := newFakeSharkd(t,
		expectParams(t, map[string]any{"follow": "UDP", "filter": "udp.stream eq 0"}, `{"shost":"NONE","sport":"0","sbytes":0,"chost":"NONE","cport":"0","cbytes":0}`), // sub_stream 零值不发
		envResult(`{"shost":"10.0.0.1","sport":"1234","sbytes":8,"chost":"10.0.0.2","cport":"5678","cbytes":8,"payloads":[{"n":1,"d":"aGVsbG8=","s":1}]}`),
		expectParams(t, map[string]any{"follow": "UDP", "filter": "udp.stream eq 0", "sub_stream": float64(2)}, `{"shost":"a","sport":"1","sbytes":0,"chost":"b","cport":"2","cbytes":0}`),
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.Follow(context.Background(), FollowParams{Follow: "UDP", Filter: "udp.stream eq 0"})
	require.NoError(t, err)
	require.Equal(t, "NONE", res.SHost)
	require.Equal(t, "0", res.SPort) // 实测：端口是字符串
	require.Equal(t, "0", res.CPort)
	require.Equal(t, int64(0), res.SBytes)
	require.Empty(t, res.Payloads)
	res, err = c.Follow(context.Background(), FollowParams{Follow: "UDP", Filter: "udp.stream eq 0"})
	require.NoError(t, err)
	require.Equal(t, "1234", res.SPort)
	require.Len(t, res.Payloads, 1)
	require.Equal(t, int64(1), res.Payloads[0].N)
	require.Equal(t, 1, res.Payloads[0].S) // 服务端方向标记
	require.Equal(t, []byte("hello"), res.Payloads[0].D)

	_, err = c.Follow(context.Background(), FollowParams{Follow: "UDP", Filter: "udp.stream eq 0", SubStream: 2})
	require.NoError(t, err)
}

func TestAPITap(t *testing.T) {
	f := newFakeSharkd(t,
		expectParams(t, map[string]any{"tap0": "expert"}, `{"taps":[{"tap":"expert","type":"expert","details":[{"f":3,"s":"Warning","g":"Checksum","p":"ICMP","m":"Bad checksum ..."}]}]}`),
		expectParams(t, map[string]any{"tap0": "expert", "tap1": "expert", "filter": "dhcp"}, `{"taps":[{"tap":"expert","type":"expert","details":[{"f":1,"s":"Comment","g":"Comment","m":"hello","p":"Packet"}]},{"tap":"expert","type":"expert","details":[{"f":1,"s":"Comment","g":"Comment","m":"hello","p":"Packet"}]}]}`),
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.Tap(context.Background(), TapParams{Taps: []string{"expert"}})
	require.NoError(t, err)
	require.Len(t, res.Taps, 1)
	require.Equal(t, "expert", res.Taps[0].Tap)
	require.Equal(t, "expert", res.Taps[0].Type)
	require.Equal(t, int64(3), res.Taps[0].Details[0].F)
	require.Equal(t, "Warning", res.Taps[0].Details[0].S)
	require.Equal(t, "Checksum", res.Taps[0].Details[0].G)
	require.Equal(t, "ICMP", res.Taps[0].Details[0].P)
	require.Contains(t, res.Taps[0].Details[0].M, "Bad checksum")

	res, err = c.Tap(context.Background(), TapParams{Taps: []string{"expert", "expert"}, Filter: "dhcp"})
	require.NoError(t, err)
	require.Len(t, res.Taps, 2)

	_, err = c.Tap(context.Background(), TapParams{Taps: make([]string, 17)})
	require.ErrorContains(t, err, "16") // 超出 tap0..tap15 → error
}

func TestAPIIntervals(t *testing.T) {
	f := newFakeSharkd(t,
		expectParams(t, map[string]any{"interval": float64(1000)}, `{"intervals":[[0,4,1312]],"last":0,"frames":4,"bytes":1312}`),
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.Intervals(context.Background(), IntervalsParams{Interval: 1000})
	require.NoError(t, err)
	require.Equal(t, [][3]int64{{0, 4, 1312}}, res.Intervals)
	require.Equal(t, int64(0), res.Last)
	require.Equal(t, int64(4), res.Frames)
	require.Equal(t, int64(1312), res.Bytes)
}

func TestAPIIOGraph(t *testing.T) {
	f := newFakeSharkd(t,
		expectParams(t, map[string]any{"graph0": "packets", "filter0": "dhcp"}, `{"iograph":[{"items":[1.0,"127",1.0,"1118f",1.0,"112c9",1.0]}]}`), // interval/interval_units 零值不发
		expectParams(t, map[string]any{
			"interval": float64(1000), "interval_units": "ms",
			"graph0": "packets", "graph1": "bytes",
			"filter1": "udp", "aot1": true,
		}, `{"iograph":[{"items":[4.0]},{"items":[524.0]}]}`),
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.IOGraph(context.Background(), IOGraphParams{Graphs: []string{"packets"}, Filters: []string{"dhcp"}})
	require.NoError(t, err)
	require.Len(t, res.IOGraph, 1)
	require.Len(t, res.IOGraph[0].Items, 7) // 数字与十六进制占位串混合
	require.JSONEq(t, "1.0", string(res.IOGraph[0].Items[0]))
	require.JSONEq(t, `"127"`, string(res.IOGraph[0].Items[1])) // 0 值桶的十六进制下标占位

	_, err = c.IOGraph(context.Background(), IOGraphParams{
		Interval: 1000, IntervalUnits: "ms",
		Graphs: []string{"packets", "bytes"}, Filters: []string{"", "udp"}, AOT: []bool{false, true},
	})
	require.NoError(t, err)

	_, err = c.IOGraph(context.Background(), IOGraphParams{Graphs: make([]string, 11)})
	require.ErrorContains(t, err, "10") // 超出 graph0..graph9 → error
}

func TestAPIDownload(t *testing.T) {
	f := newFakeSharkd(t,
		envResult(`{"file":"foo.txt","mime":"text/plain","data":"aGVsbG8="}`),
		envResult(`{}`), // token 无对应条目 → 空对象
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.Download(context.Background(), DownloadParams{Token: "eo:http_3"})
	require.NoError(t, err)
	require.Equal(t, "foo.txt", res.File)
	require.Equal(t, "text/plain", res.Mime)
	require.Equal(t, []byte("hello"), res.Data)

	res, err = c.Download(context.Background(), DownloadParams{Token: "eo:http_99"})
	require.NoError(t, err)
	require.Zero(t, res.File)
	require.Nil(t, res.Data)
}

func TestAPIDumpConf(t *testing.T) {
	// 实测：prefs 是扁平全名键（即使 pref 给模块名）；u/b 为数字、e 为 {v,s,d}
	f := newFakeSharkd(t,
		envResult(`{"prefs":{"dhcp.novellserverstring":{"b":0},"dhcp.pkt.ccc.protocol_version":{"e":[{"v":1,"d":"PKT-SP-PROV-I05-021127"},{"v":2,"d":"IETF Draft 5"},{"v":3,"s":1,"d":"RFC 3495"}]},"dhcp.pkt.ccc.option":{"u":122},"dhcp.custom_dhcp_table":{"t":[["a","b"]]},"dhcp.udp.port":{"r":"67-68,4011"}}}`),
		envResult(`{"prefs":{"tcp.desegment_tcp_streams":{"b":1}}}`),
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.DumpConf(context.Background(), DumpConfParams{Pref: "dhcp"})
	require.NoError(t, err)
	require.Equal(t, "67-68,4011", res.Prefs["dhcp.udp.port"].R)
	require.NotNil(t, res.Prefs["dhcp.pkt.ccc.option"].U)
	require.Equal(t, uint32(122), *res.Prefs["dhcp.pkt.ccc.option"].U)
	require.NotNil(t, res.Prefs["dhcp.novellserverstring"].B)
	require.Equal(t, 0, *res.Prefs["dhcp.novellserverstring"].B) // 指针区分 b:0 与缺省
	ev := res.Prefs["dhcp.pkt.ccc.protocol_version"].E
	require.Len(t, ev, 3)
	require.Equal(t, 3, ev[2].V)
	require.Equal(t, 1, ev[2].S) // 当前选中标记
	require.Equal(t, "RFC 3495", ev[2].D)
	require.JSONEq(t, `[["a","b"]]`, string(res.Prefs["dhcp.custom_dhcp_table"].T))
	require.Nil(t, res.Prefs["dhcp.udp.port"].U) // 区间项无 u

	res, err = c.DumpConf(context.Background(), DumpConfParams{Pref: "tcp.desegment_tcp_streams"})
	require.NoError(t, err)
	require.Len(t, res.Prefs, 1)
	require.Equal(t, 1, *res.Prefs["tcp.desegment_tcp_streams"].B)
}

func TestAPISetConfSetCommentBye(t *testing.T) {
	f := newFakeSharkd(t,
		expectParams(t, map[string]any{"name": "gui.console_open", "value": "never"}, `{"status":"OK"}`),
		expectParams(t, map[string]any{"frame": float64(1), "comment": "hello"}, `{"status":"OK"}`),
		expectParams(t, map[string]any{"name": "gui.console_open", "value": "never"}, `{"status":"NO"}`),
		envResult(`{"status":"OK"}`),
	)
	c := NewClient(f.ln.Addr().String())
	require.NoError(t, c.SetConf(context.Background(), SetConfParams{Name: "gui.console_open", Value: "never"}))
	require.NoError(t, c.SetComment(context.Background(), SetCommentParams{Frame: 1, Comment: "hello"}))
	require.ErrorContains(t, c.SetConf(context.Background(), SetConfParams{Name: "gui.console_open", Value: "never"}), "NO") // status 非 OK 报错
	require.NoError(t, c.Bye(context.Background()))
}
