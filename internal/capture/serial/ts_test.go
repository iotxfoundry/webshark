// ts_test.go 帧时刻换算回归：eBPF 单调 ts_ns → 墙钟（基准采样对齐）与负值钳制。
package serial

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTsBaseToWall 合成 ts_ns 换算：基准后 5s 的事件 → 基准墙钟 +5s；
// 基准前（负偏移）的事件钳到基准墙钟，零偏移即基准本身。
func TestTsBaseToWall(t *testing.T) {
	wall := time.Unix(1700000000, 123456789)
	b := tsBase{mono: 100_000_000_000, wall: wall} // 单调基准 100s
	assert.Equal(t, wall.Add(5*time.Second), b.toWall(105_000_000_000))
	assert.Equal(t, wall, b.toWall(99_999_999_999), "负偏移应钳到基准墙钟")
	assert.Equal(t, wall, b.toWall(100_000_000_000), "零偏移=基准本身")
}
