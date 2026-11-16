package sharkd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// envResult 生成按序应答的 handler：原样返回实测响应串（不经过 map 重编码，
// 保证解码路径与真实 sharkd 字节一致）。
func envResult(raw string) func(map[string]any) any {
	return func(req map[string]any) any {
		return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": json.RawMessage(raw)}
	}
}

func TestAPILoad(t *testing.T) {
	f := newFakeSharkd(t, envResult(`{"status":"OK"}`))
	c := NewClient(f.ln.Addr().String())
	res, err := c.Load(context.Background(), "/tmp/a.pcapng")
	require.NoError(t, err)
	require.Equal(t, "OK", res.Status)
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

func TestAPIFrames(t *testing.T) {
	rows := `[{"c":["1","23:57:30.0000010Z","10.0.0.1","10.0.0.2","ICMP","74","Echo (ping) request  id=0x0001, seq=1/256, ttl=64 (no response found!)"],"num":1,"bg":"fce0ff","fg":"12272e"},{"c":["2","23:57:30.0000020Z","10.0.0.2","10.0.0.1","ICMP","74","Echo (ping) reply  id=0x0001, seq=1/256, ttl=64 (no response found!)"],"num":2,"comments":["hello"],"m":"M","i":"I","ct":"CT"}]`
	f := newFakeSharkd(t, func(req map[string]any) any {
		require.Equal(t, map[string]any{"skip": float64(1), "limit": float64(2), "filter": "icmp"}, req["params"])
		return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": json.RawMessage(rows)}
	})
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
	require.Equal(t, int64(2), decoded[1].Num)
	require.Equal(t, []string{"hello"}, decoded[1].Comments)
	require.Equal(t, "M", decoded[1].M)
	require.Equal(t, "I", decoded[1].I)
	require.Equal(t, "CT", decoded[1].CT)
}

func TestAPIFrame(t *testing.T) {
	f := newFakeSharkd(t, envResult(`{"tree":[{"l":"Frame 1: 74 bytes on wire (592 bits), 74 bytes captured","h":[0,74],"n":[{"l":"Ethernet II","h":[0,14],"n":[{"l":"Destination: 10:00:00:00:00:02","h":[0,6],"e":2,"t":"str"}]}]},{"l":"Internet Control Message Protocol","h":[34,8],"f":"icmp","fn":"icmp.type","e":8,"t":"uint","ds":2,"fnum":1,"url":"https://example.com","s":"Warn","g":true}],"bytes":"AQIDBAUGBw==","fol":[],"followers":[],"bg":"fce0ff","fg":"12272e"}`))
	c := NewClient(f.ln.Addr().String())
	fr, err := c.Frame(context.Background(), FrameParams{Frame: 1, Proto: true, Bytes: true})
	require.NoError(t, err)
	require.Len(t, fr.Tree, 2)
	require.Contains(t, fr.Tree[0].L, "Frame 1")
	require.Equal(t, "icmp", fr.Tree[1].F)
	require.Equal(t, "icmp.type", fr.Tree[1].FN)
	require.Equal(t, int64(2), fr.Tree[0].N[0].N[0].E)
	require.Equal(t, int64(1), fr.Tree[1].FNum)
	require.True(t, fr.Tree[1].G)
	require.Equal(t, []byte{1, 2, 3, 4, 5, 6, 7}, fr.Bytes)
	require.Equal(t, "fce0ff", fr.Bg)
	require.Equal(t, json.RawMessage("[]"), fr.Fol)
}

func TestAPICheck(t *testing.T) {
	f := newFakeSharkd(t, envResult(`{"status":"OK"}`), envResult(`{"status":"OK","warning":"test warning"}`))
	c := NewClient(f.ln.Addr().String())
	res, err := c.Check(context.Background(), "icmp")
	require.NoError(t, err)
	require.Equal(t, "OK", res.Status)
	require.Empty(t, res.Warning)
	res, err = c.Check(context.Background(), "icmp &&")
	require.NoError(t, err)
	require.Equal(t, "test warning", res.Warning)
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
		envResult(`{"shost":"NONE","sport":"0","sbytes":0,"chost":"NONE","cport":"0","cbytes":0}`),
		envResult(`{"shost":"10.0.0.1","sport":"1234","sbytes":8,"chost":"10.0.0.2","cport":"5678","cbytes":8,"payloads":[{"n":1,"d":"aGVsbG8="}]}`),
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.Follow(context.Background(), FollowParams{Follow: "TCP", Filter: "tcp"})
	require.NoError(t, err)
	require.Equal(t, "NONE", res.SHost)
	require.Equal(t, "0", res.SPort) // 实测：端口是字符串
	require.Equal(t, "0", res.CPort)
	require.Equal(t, int64(0), res.SBytes)
	require.Empty(t, res.Payloads)
	res, err = c.Follow(context.Background(), FollowParams{Follow: "TCP"})
	require.NoError(t, err)
	require.Equal(t, "1234", res.SPort)
	require.Len(t, res.Payloads, 1)
	require.Equal(t, int64(1), res.Payloads[0].N)
	require.Equal(t, []byte("hello"), res.Payloads[0].D)
}

func TestAPITap(t *testing.T) {
	f := newFakeSharkd(t, envResult(`{"taps":[{"tap":"expert","type":"expert","details":[{"f":3,"s":"Warning","g":"Checksum","p":"ICMP","m":"Bad checksum ..."}]}]}`))
	c := NewClient(f.ln.Addr().String())
	res, err := c.Tap(context.Background(), TapParams{Tap: "expert"})
	require.NoError(t, err)
	require.Len(t, res.Taps, 1)
	require.Equal(t, "expert", res.Taps[0].Tap)
	require.Equal(t, "expert", res.Taps[0].Type)
	require.Equal(t, int64(3), res.Taps[0].Details[0].F)
	require.Equal(t, "Warning", res.Taps[0].Details[0].S)
	require.Equal(t, "Checksum", res.Taps[0].Details[0].G)
	require.Equal(t, "ICMP", res.Taps[0].Details[0].P)
	require.Contains(t, res.Taps[0].Details[0].M, "Bad checksum")
}

func TestAPIIntervals(t *testing.T) {
	f := newFakeSharkd(t, envResult(`{"intervals":[[0,2,148]],"last":0,"frames":2,"bytes":148}`))
	c := NewClient(f.ln.Addr().String())
	res, err := c.Intervals(context.Background())
	require.NoError(t, err)
	require.Equal(t, [][3]int64{{0, 2, 148}}, res.Intervals)
	require.Equal(t, int64(0), res.Last)
	require.Equal(t, int64(2), res.Frames)
	require.Equal(t, int64(148), res.Bytes)
}

func TestAPISetConfAndBye(t *testing.T) {
	f := newFakeSharkd(t,
		envResult(`{"status":"OK"}`),
		envResult(`{"status":"NO"}`),
		envResult(`{"status":"OK"}`),
	)
	c := NewClient(f.ln.Addr().String())
	require.NoError(t, c.SetConf(context.Background(), "gui.console_open", "never"))
	err := c.SetConf(context.Background(), "gui.console_open", "never")
	require.ErrorContains(t, err, "NO") // status 非 OK 报错
	require.NoError(t, c.Bye(context.Background()))
}
