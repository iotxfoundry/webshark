// useDrawerFetch.ts - 抽屉组件取数共享 hook(打开即取, 依赖变化重取):
// alive 防卸载后 setState(重置放异步头, 避免同步 setState-in-effect 级联渲染)
import { useEffect, useState } from "react";
import { errMsg } from "../api/client";

export function useDrawerFetch<T>(fetcher: () => Promise<T>, deps: readonly unknown[]): {
  data: T | null;
  err: string;
} {
  const [data, setData] = useState<T | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    let alive = true;
    const load = async () => {
      setData(null);
      setErr("");
      try {
        const d = await fetcher();
        if (alive) setData(d);
      } catch (e) {
        if (alive) setErr(errMsg(e));
      }
    };
    void load();
    return () => { alive = false; };
    // deps 由调用方显式给定(取数键), 与被抽取前的 effect 依赖完全一致
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  return { data, err };
}
