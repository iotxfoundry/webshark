// useCaptureStatus.ts - sharkd HTTP 探活 + spool 分片数(工具栏状态徽章):
// 5s 轮询 /api/status(401 由 readJSON 管线兜底, 其余失败静默 → 离线兜底文案);
// 隐藏标签页暂停轮询(每次 poll 都会拨 sharkd + 列目录), 恢复可见/聚焦立即补拉一次.
import { useEffect, useState } from "react";
import { fetchCaptureStatus, type CaptureStatus } from "../api/capture";

export function useCaptureStatus() {
  const [status, setStatus] = useState<CaptureStatus | null>(null);

  useEffect(() => {
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
  }, []);

  return { status };
}
