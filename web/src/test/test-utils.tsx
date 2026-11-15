import { render, type RenderOptions } from "@testing-library/react";
import type { ReactElement } from "react";

/** webshark 单页无路由/无全局 Provider 依赖, 直接透传 render */
export function renderWithProviders(
  ui: ReactElement,
  options?: RenderOptions,
) {
  return render(ui, options);
}

export * from "@testing-library/react";
