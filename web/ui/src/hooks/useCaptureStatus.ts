// useCaptureStatus.ts - sharkd HTTP 探活 + spool 分片数(工具栏状态徽章):
// 5s 轮询 /api/status(401 由 readJSON 管线兜底, 其余失败静默 → 离线兜底文案);
// 隐藏标签页暂停轮询(每次 poll 都会拨 sharkd + 列目录), 恢复可见/聚焦立即补拉一次;
// embedded 宿主首令牌未到不启动轮询(与 useCaptureStream 同门控), 令牌到达经订阅唤醒.
import { useEffect, useState } from "react";
import { fetchCaptureStatus, type CaptureStatus } from "../api/capture";
import { awaitingFirstToken, getTokenVersion, onTokenChange } from "../api/token";

export function useCaptureStatus() {
  const [status, setStatus] = useState<CaptureStatus | null>(null);
  /** 令牌版本: embedded 门控解除(宿主注入)即唤醒轮询; 后续变化重起轮询=立即补拉一次, 无害 */
  const [tokenVersion, setTokenVersion] = useState(getTokenVersion());
  useEffect(() => onTokenChange(() => setTokenVersion(getTokenVersion())), []);

  useEffect(() => {
    if (awaitingFirstToken()) return;
    let alive = true;
    const poll = () => {
      if (document.hidden) return;
      fetchCaptureStatus()
        .then((s) => { if (alive) setStatus(s); })
        .catch(() => { if (alive) setStatus({ connected: false, files: [], reason: "网络不可达" }); });
    };
    poll();
    const t = window.setInterval(poll, 5000);
    document.addEventListener("visibilitychange", poll);
    window.addEventListener("focus", poll);
    return () => {
      alive = false;
      window.clearInterval(t);
      document.removeEventListener("visibilitychange", poll);
      window.removeEventListener("focus", poll);
    };
  }, [tokenVersion]);

  return { status };
}
