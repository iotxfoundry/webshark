// useCaptureStream.ts - 抓包数据面 hook(推送模型): 服务端后台追帧驱动, 前端零轮询.
// 下行消费: hello 握手确认(续推游标在服务端, 聚合器保留) / rows 微批 → 跨分片聚合器追加渲染
// (ref 去重 + no 自增) / rotated 信息性(分片标签) / reset 清空聚合器按服务端全量重推对齐;
// 断线重连(同 handle)服务端按游标续推无重无漏, 聚合器跨重连保留.
// 上行: 仅 _ctl 流控制(抓包暂停恢复 + 筛选进出, 与数据同连接 TCP 保序); 未连接时仅存态,
// 连接就绪后重断言(重连不丢暂停/筛选态). autoscroll 是纯前端显示开关, 不经上行.
// 交互查询(frame/check/complete/follow/expert)全部走 HTTP(api/capture.ts), live WS 无
// JSON-RPC 通道.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ctlCaptureMsg, ctlFilterMsg } from "../api/captureStream";
import {
  checkCaptureFilter, completeCaptureFilter, fetchCaptureFrame,
  followCaptureStream, tapCaptureExpert,
} from "../api/capture";
import type { SharkdExpertResponse, SharkdFollowResult, SharkdFrameDetail } from "../api/sharkdTypes";
import { openSharkdTransport, type SharkdConn } from "./sharkdTransport";
import { createCaptureAggregator, type CaptureAggregator, type CaptureViewRow } from "./captureAggregator";
import { getTokenVersion, onTokenChange, awaitingFirstToken } from "../api/token";
import { errMsg } from "../api/client";

export function useCaptureStream(initialFilter?: string) {
  const [conn, setConn] = useState<SharkdConn>("connecting");
  const [rows, setRows] = useState<CaptureViewRow[]>([]);
  /** 当前生效筛选(空串 = 普通实时流); 初值承接 URL/localStorage 还原, open 后断言链自动上行 */
  const [filter, setFilter] = useState(initialFilter ?? "");
  const [error, setError] = useState("");

  const agg = useMemo<CaptureAggregator>(() => createCaptureAggregator(), []);
  const connRef = useRef(conn);
  useEffect(() => { connRef.current = conn; }, [conn]);
  const filterRef = useRef(initialFilter ?? "");
  useEffect(() => { filterRef.current = filter; }, [filter]);
  /** 连接就绪后由 transport 注入的上行通道(_ctl 专用) */
  const sendRef = useRef<((line: string) => void) | null>(null);

  /** 令牌版本: token 变化(宿主注入/401 清除)计入建连依赖 → 旧 WS 关闭, 以新令牌重建 */
  const [tokenVersion, setTokenVersion] = useState(getTokenVersion());
  useEffect(() => onTokenChange(() => setTokenVersion(getTokenVersion())), []);

  // 传输生命周期: 挂载建连(同 handle), 卸载清理; 断线退避重连在 transport 内.
  // embedded 门控: 宿主首令牌未到不建流(免挂载即无令牌 subscribe 401); 令牌到达即
  // tokenVersion 变更唤醒本 effect 重跑, 无需定时器
  useEffect(() => {
    if (awaitingFirstToken()) return;
    const close = openSharkdTransport({
      onReady: (send) => { sendRef.current = send; },
      onConn: (s) => {
        setConn(s);
        // 连接就绪即重断言筛选态(重连后服务端筛选态随流重建; 空筛选无需断言)
        if (s === "open" && filterRef.current !== "") {
          try {
            sendRef.current?.(ctlFilterMsg(filterRef.current));
          } catch { /* 刚 open 即发送失败: 忽略, 下次重连再断言 */ }
        }
      },
      onEvent: (e) => {
        switch (e.type) {
          case "hello":
            break; // 握手确认: 聚合器保留, 服务端按游标续推
          case "rows":
            agg.append(e.rows, e.shard);
            setRows(agg.view());
            break;
          case "rotated":
            break; // 信息性: 行数据随 rows 微批走, 分片归属在行内 ref, 前端无需动作
          case "reset":
            agg.reset();
            setRows(agg.view());
            break;
          case "error":
            // 控制指令未生效(筛选失败等): 上横幅; 已提交筛选时撤乐观「筛选中」标识
            setError(e.reason);
            if (filterRef.current !== "") {
              filterRef.current = "";
              setFilter("");
            }
            break;
        }
      },
    });
    return close;
  }, [agg, tokenVersion]);

  /** _ctl 上行通用: 未连接忽略(重连后按当前态重断言); 发送失败上错误横幅 */
  const sendCtl = useCallback((line: string) => {
    if (connRef.current !== "open" || sendRef.current === null) return;
    try {
      sendRef.current(line);
    } catch (e) {
      setError(errMsg(e));
    }
  }, []);

  /** 抓包暂停/恢复: WS 上行 _ctl */
  const setCapturePaused = useCallback((v: boolean) => {
    sendCtl(ctlCaptureMsg(v ? "pause" : "resume"));
  }, [sendCtl]);

  /** 筛选提交/清除: WS 上行 _ctl{op:"filter"}(空串退出); 未连接仅存态(重连后 onConn 重断言) */
  const applyFilter = useCallback((f: string) => {
    filterRef.current = f;
    setFilter(f);
    sendCtl(ctlFilterMsg(f));
  }, [sendCtl]);

  /** frame 详情(点行触发): 按行隐藏查询键 ref(分片名+原始帧号)走 HTTP 按复合键路由 */
  const fetchFrame = useCallback(async (ref: { shard: string; num: number }): Promise<SharkdFrameDetail> => {
    return await fetchCaptureFrame(ref.shard, ref.num);
  }, []);

  /** follow: 流跟随对话视图(协议短名大写 + 树节点 f 串 filter), HTTP */
  const fetchFollow = useCallback((type: string, filter: string): Promise<SharkdFollowResult> => {
    return followCaptureStream(type, filter);
  }, []);

  /** tap expert 汇总, HTTP */
  const fetchExpert = useCallback((): Promise<SharkdExpertResponse> => {
    return tapCaptureExpert();
  }, []);

  /** check: display filter 校验(400 envelope 错误透传 sharkd 文案, OK/warning 返回), HTTP */
  const checkFilter = useCallback(async (f: string) => {
    if (f.trim() === "") return { status: "OK" } as { status: string; warning?: string };
    return await checkCaptureFilter(f);
  }, []);

  /** complete: 补全(空前缀返回空), HTTP */
  const complete = useCallback(async (prefix: string) => {
    if (prefix.trim() === "") return [];
    return await completeCaptureFilter(prefix);
  }, []);

  return {
    conn, rows, filter, error,
    setCapturePaused, applyFilter, fetchFrame, fetchFollow, fetchExpert, checkFilter, complete,
  };
}

