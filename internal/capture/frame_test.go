package capture

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestFrameContract 帧模型契约锚: 采集源产出 → 落盘消费的字段全量携带.
func TestFrameContract(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	f := Frame{
		IfaceID:  5,
		LinkType: 227,
		TS:       ts,
		Data:     []byte{1, 2, 3},
		Fields:   map[string]string{"proto": "can", "iface": "can0"},
	}
	assert.Equal(t, uint16(5), f.IfaceID)
	assert.EqualValues(t, 227, f.LinkType)
	assert.Equal(t, ts, f.TS)
	assert.Equal(t, []byte{1, 2, 3}, f.Data)
	assert.Equal(t, "can", f.Fields["proto"])
}
