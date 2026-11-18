package serial

import (
	"sync"
	"testing"
	"time"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// TestSerialSourceFeedFlushIdleRace readLoop 的 Feed 与 flushLoop 的 FlushIdle
// 并发跑同一 Framer（-race 压 1 秒）：两处全程持 s.mu，无 data race 且切帧回调
// 不重入（缓冲状态一致性）。不走真 eBPF，直测源内共享 Framer 的两条路径。
func TestSerialSourceFeedFlushIdleRace(t *testing.T) {
	s := NewSerialSource(nil, 115200, 0, capture.LinkTypeUser0, nil)
	var mu sync.Mutex
	pushed := 0
	push := func(capture.Frame) {
		mu.Lock()
		pushed++
		mu.Unlock()
	}
	s.mu.Lock()
	s.fr = make(map[string]*framerPair)
	s.mu.Unlock()

	stop := time.Now().Add(time.Second)
	var wg sync.WaitGroup
	// 模拟 readLoop 的 feed 路径；间隔取 2ms > t35(115200)=1.75ms，保证静默
	// 周期性切帧（成帧器只认字节时序，帧内容无协议含义）。
	wg.Go(func() {
		i := 0
		for time.Now().Before(stop) {
			s.feed("ttyUSB0", capture.RX, []byte{byte(i), 0x40, 0x05, 0x16}, time.Now(), push)
			i++
			time.Sleep(2 * time.Millisecond)
		}
	})
	// 模拟 flushLoop 路径（周期 FlushIdle 全部 Framer）。
	wg.Go(func() {
		for time.Now().Before(stop) {
			now := time.Now()
			s.mu.Lock()
			for _, p := range s.fr {
				p.tx.FlushIdle(now)
				p.rx.FlushIdle(now)
			}
			s.mu.Unlock()
			time.Sleep(time.Millisecond)
		}
	})
	wg.Wait()
	// 压测期应有帧产出且无 panic/race 到此（状态一致性由 -race 把关）。
	mu.Lock()
	defer mu.Unlock()
	if pushed == 0 {
		t.Fatal("并发压测期间应有帧产出")
	}
}
