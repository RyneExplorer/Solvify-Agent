import type { ExecutionPlan, StreamEvent } from '../types/chat'

// 计划快照和任务增量都返回新对象，历史消息不会被下一次流式更新修改。
export function applyExecutionPlanEvent(plan: ExecutionPlan | null, event: StreamEvent): ExecutionPlan | null {
  if (event.type === 'plan' && event.plan) {
    const samePlan = plan?.id === event.plan.id
    if (samePlan && (event.plan.version ?? 0) < (plan?.version ?? 0)) return plan
    return {
      ...event.plan,
      tasks: (event.plan.tasks ?? []).map(task => ({
        ...task,
        added_version: (samePlan ? plan?.tasks.find(previous => previous.id === task.id)?.added_version : undefined)
          ?? task.added_version ?? event.plan?.version,
        depends_on: [...(task.depends_on ?? [])],
      })),
    }
  }
  if (!plan || (event.plan_id && event.plan_id !== plan.id)
    || (event.plan_version ?? 0) < (plan.version ?? 0)) return plan
  if (!event.task && !event.phase && !event.replan_reason) return plan
  const metadata = {
    version: event.plan_version ?? plan.version,
    phase: event.phase ?? plan.phase,
    replan_reason: event.replan_reason ?? plan.replan_reason,
    round: event.round ?? plan.round,
  }
  if (!event.task) return { ...plan, ...metadata }
  const index = plan.tasks.findIndex(task => task.id === event.task?.id)
  const tasks = [...plan.tasks]
  if (index < 0) tasks.push({ ...event.task, added_version: event.task.added_version ?? metadata.version, depends_on: [...(event.task.depends_on ?? [])] })
  else tasks[index] = { ...tasks[index], ...event.task, added_version: tasks[index].added_version, depends_on: [...(event.task.depends_on ?? tasks[index].depends_on ?? [])] }
  return { ...plan, ...metadata, tasks }
}
