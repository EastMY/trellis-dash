import { ArrowRightOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button, Empty, Skeleton, Timeline, Typography } from "antd";
import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { groupActivityEvents } from "../lib/activity";
import { eventLabel, fullDate, relativeDate } from "../lib/format";
import type { ActivityEvent } from "../types";
import { activityEventIcon } from "./ActivityEventIcon";

interface SidebarActivityProps {
  items: ActivityEvent[];
  loading?: boolean;
  error?: unknown;
  onRetry: () => void;
  onViewAll: () => void;
}

const TIMELINE_BOTTOM_RESERVE = 6;

/**
 * 根据真实渲染后的条目底边，计算当前容器能完整容纳多少条活动。
 * 容器尚未完成布局时返回全部条目，避免测试环境或隐藏侧栏被误判为空。
 */
export function countVisibleTimelineItems(availableHeight: number, itemBottoms: number[]) {
  if (itemBottoms.length === 0) return 0;
  if (!Number.isFinite(availableHeight) || availableHeight <= 0) return itemBottoms.length;

  const limit = Math.max(0, availableHeight - TIMELINE_BOTTOM_RESERVE);
  const fitting = itemBottoms.findIndex((bottom) => bottom > limit);
  const count = fitting === -1 ? itemBottoms.length : fitting;
  // 正常桌面布局至少有一条活动的空间；保底值也避免高度过渡期间列表闪空。
  return Math.max(1, count);
}

function ActivityTimelineContent({ event, count }: { event: ActivityEvent; count: number }) {
  const label = `${eventLabel(event.type)}${count > 1 ? ` × ${count}` : ""}`;
  const detail = [event.taskKey, event.source].filter(Boolean).join(" · ");

  return (
    <div className="sider-activity-copy">
      <div className="sider-activity-line">
        <Typography.Text strong ellipsis title={label}>{label}</Typography.Text>
        <time dateTime={event.createdAt} title={fullDate(event.createdAt)}>
          {relativeDate(event.createdAt)}
        </time>
      </div>
      <Typography.Text type="secondary" ellipsis title={detail}>{detail}</Typography.Text>
    </div>
  );
}

export function SidebarActivity({
  items,
  loading = false,
  error,
  onRetry,
  onViewAll,
}: SidebarActivityProps) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const measureRef = useRef<HTMLDivElement>(null);
  const groups = useMemo(() => groupActivityEvents(items), [items]);
  const timelineItems = useMemo(() => groups.map(({ event, count }) => ({
    key: event.id,
    icon: activityEventIcon(event),
    color: event.type.includes("error") || event.type.includes("fail") ? "red" : "gray",
    content: <ActivityTimelineContent event={event} count={count} />,
  })), [groups]);
  const [visibleCount, setVisibleCount] = useState(timelineItems.length);

  useLayoutEffect(() => {
    const body = bodyRef.current;
    const measure = measureRef.current;
    if (!body || !measure || timelineItems.length === 0) return undefined;

    const updateVisibleCount = () => {
      const bodyTop = body.getBoundingClientRect().top;
      const itemBottoms = Array.from(measure.querySelectorAll<HTMLElement>(".ant-timeline-item")).map((item) => {
        const content = item.querySelector<HTMLElement>(".ant-timeline-item-content");
        const head = item.querySelector<HTMLElement>(".ant-timeline-item-head");
        return Math.max(
          content?.getBoundingClientRect().bottom ?? bodyTop,
          head?.getBoundingClientRect().bottom ?? bodyTop,
        ) - bodyTop;
      });
      const nextCount = countVisibleTimelineItems(body.clientHeight, itemBottoms);
      setVisibleCount((current) => current === nextCount ? current : nextCount);
    };

    updateVisibleCount();
    const resizeObserver = typeof ResizeObserver === "undefined"
      ? undefined
      : new ResizeObserver(updateVisibleCount);
    // 容器高度和隐藏测量时间轴的高度都可能因窗口、字体或文本换行而变化。
    resizeObserver?.observe(body);
    resizeObserver?.observe(measure);
    window.addEventListener("resize", updateVisibleCount);

    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener("resize", updateVisibleCount);
    };
  }, [timelineItems]);

  return (
    <section className="sider-activity" aria-labelledby="sider-activity-title">
      <div className="sider-activity-heading">
        <div>
          <span className="sider-activity-kicker">PROJECT FEED</span>
          <Typography.Title id="sider-activity-title" level={5}>最近活动</Typography.Title>
        </div>
        <Button type="link" size="small" onClick={onViewAll}>
          查看全部 <ArrowRightOutlined />
        </Button>
      </div>

      {loading ? (
        <div className="sider-activity-skeleton" aria-label="正在加载最近活动">
          <Skeleton active title={false} paragraph={{ rows: 4 }} />
        </div>
      ) : error ? (
        <div className="sider-activity-state" role="alert">
          <Typography.Text type="secondary">最近活动加载失败</Typography.Text>
          <Button type="link" size="small" icon={<ReloadOutlined />} onClick={onRetry}>重试</Button>
        </div>
      ) : timelineItems.length === 0 ? (
        <div className="sider-activity-state">
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无活动记录" />
        </div>
      ) : (
        <div className="sider-activity-body" ref={bodyRef} role="region" aria-label="最近活动时间轴">
          <Timeline className="sider-activity-timeline" items={timelineItems.slice(0, visibleCount)} />
          {/* 隐藏副本只参与真实尺寸测量，不进入读屏与交互顺序。 */}
          <div className="sider-activity-measure" ref={measureRef} aria-hidden="true">
            <Timeline className="sider-activity-timeline" items={timelineItems} />
          </div>
        </div>
      )}
    </section>
  );
}
