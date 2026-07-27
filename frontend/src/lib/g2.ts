import { extend } from "@antv/g2/esm/api/extend";
import { Runtime } from "@antv/g2/esm/api/runtime";
import { litelib } from "@antv/g2/esm/lib/lite";
import { Line } from "@antv/g2/esm/mark/line";
import { Point } from "@antv/g2/esm/mark/point";
import { Light } from "@antv/g2/esm/theme/light";
import { StackY } from "@antv/g2/esm/transform/stackY";

// Dashboard 只需要柱、线、点三种 mark；在 G2 lite 库上补齐线/点及堆叠变换，
// 避免加载地图、图网络等无关运行时。
export const Chart = extend(Runtime, {
  ...litelib(),
  "mark.line": Line,
  "mark.point": Point,
  "transform.stackY": StackY,
  // G2 Chart 默认使用 light；lite 库只带 classic，需显式补齐，否则画布会创建但无法绘制。
  "theme.light": Light,
});

export type { G2Spec } from "@antv/g2";
