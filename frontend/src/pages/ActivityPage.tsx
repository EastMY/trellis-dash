import { HistoryOutlined } from "@ant-design/icons";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Button, Tag } from "antd";
import { useLayoutEffect } from "react";
import { api } from "../api/client";
import { ActivityList } from "../components/ActivityList";
import { useProjectContext } from "../components/AppShell";
import { PageHeader } from "../components/PageHeader";
import { ErrorState, PageSkeleton } from "../components/PageState";
import { groupActivityEvents } from "../lib/activity";

export function ActivityPage() {
  const { project } = useProjectContext();
  useLayoutEffect(() => {
    // 桌面端活动页由内部时间轴管理滚动，避免根页面出现无意义的纵向滚动条。
    document.documentElement.classList.add("activity-page-active");
    document.body.classList.add("activity-page-active");
    return () => {
      document.documentElement.classList.remove("activity-page-active");
      document.body.classList.remove("activity-page-active");
    };
  }, []);

  const query = useInfiniteQuery({
    queryKey: ["project", project.id, "activity"],
    queryFn: ({ pageParam }) => api.getActivity(project.id, 0, 200, pageParam),
    initialPageParam: 0,
    getNextPageParam: (page) => page.hasMore && page.firstId > 0 ? page.firstId : undefined,
  });
  if (query.isLoading) return <PageSkeleton rows={10} />;
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  // 各页统一交给活动分组逻辑做倒序排序，避免分页数组反转后旧记录跑到顶部。
  const events = (query.data?.pages ?? []).flatMap((page) => page.items);
  const displayCount = groupActivityEvents(events).length;
  return (
    <div className="page activity-page">
      <PageHeader
        title="活动记录"
        description="仅记录有意义的索引、任务、Session 与 Git 状态变化"
        meta={<Tag variant="filled" icon={<HistoryOutlined />}>{displayCount} 条</Tag>}
      />
      <section className="section-panel activity-full">
        <ActivityList items={events} layout="snake" />
        {query.hasNextPage && (
          <Button block loading={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
            加载更早记录
          </Button>
        )}
      </section>
    </div>
  );
}
