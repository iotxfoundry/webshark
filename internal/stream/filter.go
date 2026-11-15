// filter.go - 筛选模式会话状态挂点与生效流程：筛选不是独立管线，而是同一追帧
// 管道的参数（rows 帧 mode:"filter" + frames 请求携 filter）——进入/切换 =
// reset 清视图 + 位置归零（worker 下轮自环窗起点 mtime 升序逐片走查，活动分片
// 按增长重 load 续推）；退出 = 位置跳最新片回普通流。全流单一 ref 空间（分片名
// +帧号），行 ref 恒有效（详情走分片缓存实例），无派生文件与生命周期体系。
package stream

import (
	"errors"

	"github.com/iotxfoundry/webshark/internal/sharkd"
)

// enqueueError 下发错误提示帧（筛选校验失败等 UI 提示场景）。
func (s *Session) enqueueError(reason string) {
	s.Enqueue(ErrorFrame(reason))
}

// applyFilter 处理待生效筛选请求（worker tick 取走）：同值=no-op 不重走查；
// 空=退出（位置跳最新片回普通流）；其余=语法校验（轻查询实例）→ reset 清视图
// + 位置归零（worker 下轮自环窗起点带 filter 逐片走查）。非法 filter：发
// error 帧（reason 含校验原文），保持原状态不变。
func (m *Manager) applyFilter(s *Session, f string, pos *streamPos, b *batch) {
	if f == s.Filter() { // 同 filter 重提：no-op 不重走查
		return
	}
	if f == "" { // 退出：回普通流，位置跳最新分片（已收 filter 行 ref 恒有效，视图保留）
		s.setFilter("")
		b.discard()
		b.mode = "live"
		*pos = streamPos{shard: m.latestShard()}
		return
	}
	if _, err := m.Light().Call("check", map[string]string{"filter": f}); err != nil {
		var se *sharkd.Error
		if errors.As(err, &se) {
			s.enqueueError("filter 校验失败: " + se.Message)
			return
		}
		s.enqueueError("filter 校验不可用(sharkd 不可达), 已保持当前模式")
		return
	}
	s.setFilter(f)
	s.realign() // 清视图：reset 帧 + 清队列，前端清空后收走查结果
	b.discard()
	*pos = streamPos{} // 位置归零：下轮走查自环窗起点带 filter 逐片推进
}
