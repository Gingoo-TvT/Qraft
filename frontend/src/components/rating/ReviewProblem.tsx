import MarkdownRenderer from '@/components/MarkdownRenderer';
import type { ReviewTask } from '@/lib/rating-types';
import { Panel, dateLabel } from './Primitives';
export default function ReviewProblem({ task }: { task: ReviewTask }) {
 return <Panel title={task.title} description={'约定观察窗口：' + task.window_minutes + ' 分钟 · ' + (task.context === 'contest' ? '比赛' : '练习') + ' · 邀请截止 ' + dateLabel(task.expires_at)}>
  <p className="text-sm text-[var(--dm)]">时间限制 {task.time_limit} ms · 内存限制 {task.memory_limit} MB。观察窗口是本次评价约定，不是页面计时器；请记录真正用于作答的时间。</p>
  <MarkdownRenderer content={task.statement} />
  {(task.samples ?? []).map((sample, index) => <div className="grid gap-3 sm:grid-cols-2" key={index}><div><h3 className="text-sm font-medium">样例输入 {index + 1}</h3><pre className="mt-2 overflow-auto rounded-lg bg-[var(--ds)] p-3 text-sm">{sample.input}</pre></div><div><h3 className="text-sm font-medium">样例输出 {index + 1}</h3><pre className="mt-2 overflow-auto rounded-lg bg-[var(--ds)] p-3 text-sm">{sample.output}</pre></div></div>)}
 </Panel>;
}
