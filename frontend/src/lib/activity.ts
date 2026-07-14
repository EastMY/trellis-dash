import type { ActivityEvent } from "../types";

export interface ActivityGroup {
  event: ActivityEvent;
  count: number;
}

function activityTimestamp(event: ActivityEvent): number {
  const timestamp = Date.parse(event.createdAt);
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

/**
 * 将活动按最新时间排序，并合并相邻的 Git 状态更新。
 *
 * 原始事件仍保留在服务端；这里只生成稳定的只读展示分组，分页加载后也能
 * 重新计算跨页边界上的连续分组。
 */
export function groupActivityEvents(items: ActivityEvent[]): ActivityGroup[] {
  const sorted = [...items].sort((left, right) => {
    const timeDifference = activityTimestamp(right) - activityTimestamp(left);
    return timeDifference || right.id - left.id;
  });

  const groups: ActivityGroup[] = [];
  for (const event of sorted) {
    const previous = groups.at(-1);
    const canMerge = event.type === "git.updated"
      && previous?.event.type === "git.updated"
      && previous.event.projectId === event.projectId;

    if (canMerge) {
      previous.count += 1;
      continue;
    }
    groups.push({ event, count: 1 });
  }
  return groups;
}
