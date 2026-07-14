import { Empty, Timeline, Typography } from "antd";
import { useLayoutEffect, useRef, useState } from "react";
import { groupActivityEvents } from "../lib/activity";
import { eventLabel, fullDate, relativeDate } from "../lib/format";
import type { ActivityEvent } from "../types";
import { activityEventIcon } from "./ActivityEventIcon";

interface ActivityListProps {
  items: ActivityEvent[];
  layout?: "default" | "snake";
}

type ActivityGroup = ReturnType<typeof groupActivityEvents>[number];

const MIN_SNAKE_HEIGHT = 420;
const IDEAL_ROW_HEIGHT = 104;
const COMPACT_BREAKPOINT = 720;

function cssPixels(value: string) {
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function ActivityItemContent({ event, count }: { event: ActivityEvent; count: number }) {
  return (
    <div className="activity-item">
      <div className={`activity-item-heading${event.taskKey ? " activity-item-heading--task" : ""}`}>
        <Typography.Text strong>
          {eventLabel(event.type)}{count > 1 ? ` × ${count}` : ""}
        </Typography.Text>
        {event.taskKey && <Typography.Text className="activity-task-key" code>{event.taskKey}</Typography.Text>}
      </div>
      <Typography.Text type="secondary" title={fullDate(event.createdAt)}>
        {relativeDate(event.createdAt)}，来源：{event.source}
      </Typography.Text>
    </div>
  );
}

function buildSnakePath(columnCount: number, columnWidth: number, height: number, compact: boolean) {
  const axisX = 20;
  const edgeY = 18;
  if (compact) return `M ${axisX} ${edgeY} V ${Math.max(edgeY, height - edgeY)}`;

  const bottomY = height - edgeY;
  const radius = 20;
  let path = `M ${axisX} ${edgeY}`;

  for (let column = 0; column < columnCount; column += 1) {
    const x = column * columnWidth + axisX;
    const nextX = (column + 1) * columnWidth + axisX;
    const isLast = column === columnCount - 1;

    if (column % 2 === 0) {
      path += isLast
        ? ` V ${bottomY}`
        : ` V ${bottomY - radius} Q ${x} ${bottomY} ${x + radius} ${bottomY} H ${nextX - radius} Q ${nextX} ${bottomY} ${nextX} ${bottomY - radius}`;
    } else {
      path += isLast
        ? ` V ${edgeY}`
        : ` V ${edgeY + radius} Q ${x} ${edgeY} ${x + radius} ${edgeY} H ${nextX - radius} Q ${nextX} ${edgeY} ${nextX} ${edgeY + radius}`;
    }
  }

  return path;
}

function SnakeActivityList({ groups }: { groups: ActivityGroup[] }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [metrics, setMetrics] = useState({ width: 960, height: 640 });

  useLayoutEffect(() => {
    const element = scrollRef.current;
    if (!element) return undefined;

    const updateMetrics = () => {
      const rect = element.getBoundingClientRect();
      const viewportHeight = window.visualViewport?.height ?? window.innerHeight;
      const width = Math.max(320, Math.floor(element.clientWidth || rect.width || window.innerWidth));
      const page = element.closest<HTMLElement>(".activity-page");
      const panel = element.closest<HTMLElement>(".activity-full");
      const pageBottom = page ? cssPixels(window.getComputedStyle(page).paddingBottom) : 0;
      const panelStyle = panel ? window.getComputedStyle(panel) : undefined;
      const panelBottom = panelStyle
        ? cssPixels(panelStyle.paddingBottom) + cssPixels(panelStyle.borderBottomWidth)
        : 0;
      // 同时预留页面留白、面板内边距与横向滚动条空间，避免画布把页面向下撑出视口。
      const bottomReserve = pageBottom + panelBottom + 12;
      const height = Math.max(MIN_SNAKE_HEIGHT, Math.floor(viewportHeight - rect.top - bottomReserve));
      setMetrics((current) => current.width === width && current.height === height ? current : { width, height });
    };

    updateMetrics();
    const resizeObserver = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(updateMetrics);
    resizeObserver?.observe(element);
    window.addEventListener("resize", updateMetrics);
    window.visualViewport?.addEventListener("resize", updateMetrics);

    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener("resize", updateMetrics);
      window.visualViewport?.removeEventListener("resize", updateMetrics);
    };
  }, []);

  const compact = metrics.width <= COMPACT_BREAKPOINT;
  // 桌面端按当前可用高度计算容量；窄屏保留单列，交给页面自然纵向滚动。
  const rowsPerColumn = compact
    ? groups.length
    : Math.max(3, Math.floor((metrics.height - 64) / IDEAL_ROW_HEIGHT));
  const columnCount = compact ? 1 : Math.ceil(groups.length / rowsPerColumn);
  const visibleColumns = Math.max(1, Math.min(3, columnCount));
  const columnWidth = compact
    ? metrics.width
    : Math.max(300, Math.min(480, Math.floor(metrics.width / visibleColumns)));
  const canvasHeight = compact ? Math.max(180, groups.length * 82 + 32) : metrics.height;
  const canvasWidth = compact ? metrics.width : Math.max(metrics.width, columnCount * columnWidth);
  const path = buildSnakePath(columnCount, columnWidth, canvasHeight, compact);

  return (
    <div
      className={`activity-snake-scroll${compact ? " activity-snake-scroll--compact" : ""}`}
      ref={scrollRef}
      role="region"
      aria-label={compact ? "活动记录时间轴" : "活动记录蛇形时间轴，可横向滚动"}
      tabIndex={0}
    >
      <div
        className="activity-snake-canvas"
        data-columns={columnCount}
        data-rows-per-column={rowsPerColumn}
        style={{ width: canvasWidth, height: canvasHeight }}
      >
        <svg
          className="activity-snake-path"
          viewBox={`0 0 ${canvasWidth} ${canvasHeight}`}
          preserveAspectRatio="none"
          aria-hidden="true"
        >
          <path d={path} vectorEffect="non-scaling-stroke" />
        </svg>
        <ol
          className="activity-snake-grid"
          aria-label="活动记录时间轴"
          style={{
            gridTemplateColumns: `repeat(${columnCount}, ${columnWidth}px)`,
            gridTemplateRows: `repeat(${rowsPerColumn}, minmax(0, 1fr))`,
          }}
        >
          {groups.map(({ event, count }, index) => {
            const column = compact ? 0 : Math.floor(index / rowsPerColumn);
            const offset = compact ? index : index % rowsPerColumn;
            const row = compact || column % 2 === 0 ? offset : rowsPerColumn - offset - 1;
            const isError = event.type.includes("error") || event.type.includes("fail");

            return (
              <li
                className="activity-snake-entry"
                data-direction={compact || column % 2 === 0 ? "down" : "up"}
                key={event.id}
                style={{ gridColumn: column + 1, gridRow: row + 1 }}
              >
                <span className={`activity-snake-node${isError ? " activity-snake-node--error" : ""}`} aria-hidden="true">
                  {activityEventIcon(event)}
                </span>
                <article className="activity-snake-card">
                  <ActivityItemContent event={event} count={count} />
                </article>
              </li>
            );
          })}
        </ol>
      </div>
    </div>
  );
}

export function ActivityList({ items, layout = "default" }: ActivityListProps) {
  if (!items.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无活动记录" />;
  const groups = groupActivityEvents(items);

  // 完整活动页使用连续往返的蛇形路径，其余紧凑场景继续沿用 Ant Design 时间线。
  if (layout === "snake") return <SnakeActivityList groups={groups} />;

  return (
    <Timeline
      className="activity-list"
      items={groups.map(({ event, count }) => ({
        key: event.id,
        icon: activityEventIcon(event),
        color: event.type.includes("error") || event.type.includes("fail") ? "red" : "gray",
        content: <ActivityItemContent event={event} count={count} />,
      }))}
    />
  );
}
