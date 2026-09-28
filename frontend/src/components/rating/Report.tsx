import SourceDifficulty from './SourceDifficulty';
import { OUTCOME_LABELS, groupLabel, helpLabel, stateLabel } from './labels';
import type { Assessment, Workspace } from '@/lib/rating-types';
import { BulletList, Message, Panel, dateLabel, estimateLabel } from './Primitives';
const KINDS: Record<string, string> = { intended: '预期路线', alternative: '替代路线', misleading: '候选误区' };
export function Overview({ workspace, assessment }: { workspace: Workspace; assessment?: Assessment }) {
 const report = assessment?.report;
 const cards = [
  { title: '目标难度', value: String(workspace.subject.target_difficulty), note: '保留出题时的目标' },
  { title: '初始参考区间', value: estimateLabel(report?.estimate), note: assessment?.stale ? '历史版本的估计' : 'CF 风格参考，非统计置信区间' },
  { title: '人类实测', value: workspace.human.total_reviewers ? workspace.human.effective_reviewers + ' 名有效评价者' : '尚无评价', note: '按版本、人群和作答条件统计' },
  { title: '正式 rating', value: workspace.official ? String(workspace.official.rating) : '暂定', note: workspace.official?.stale ? '旧版本，等待重新确认' : '由管理员确认' },
 ];
 return <div className="space-y-6"><div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">{cards.map(card => <section className="forge-card" key={card.title}><h2 className="text-sm text-[var(--dm)]">{card.title}</h2><p className="my-3 text-2xl font-semibold">{card.value}</p><p className="text-xs text-[var(--dm)]">{card.note}</p></section>)}</div>
  <SourceDifficulty reference={workspace.subject.source_reference ?? (!assessment?.stale ? report?.source_reference : undefined)} />
  {assessment?.stale && <Message>题目或验证材料已变化。本报告属于旧版本，不能用于当前正式评分；请重新评估。</Message>}
  <Panel title="证据概览" description="不同模型负责寻找和检验解法证据；不会靠人数投票决定 rating。">
   {!report ? <p className="text-sm text-[var(--dm)]">尚无评估报告。启动评估后，可以在其他页面继续工作，任务会由服务端持续执行。</p> : <>
    <p className="whitespace-pre-wrap text-sm">{report.summary}</p>
    <dl className="grid gap-4 text-sm sm:grid-cols-2"><div><dt className="text-[var(--dm)]">题目有效性</dt><dd>{stateLabel(report.validity) || '待确认'}</dd></div><div><dt className="text-[var(--dm)]">锚点覆盖</dt><dd>{report.anchors?.length ?? 0} 道参照题；{report.comparisons?.filter(x => x.relation !== 'incomparable').length ?? 0} 个可比较关系</dd></div></dl>
    <BulletList items={report.estimate.notes} />
    <h3 className="text-sm font-medium">保留的分歧与限制</h3><BulletList items={[...(report.disagreements ?? []), ...(report.limitations ?? [])]} />
    <details><summary className="cursor-pointer text-sm text-[var(--dm)]">查看参与模型与规则</summary><p className="mt-3 text-xs text-[var(--dm)]">规则 {report.rule_version}</p><ul className="mt-2 space-y-2 text-sm">{report.models?.map((item, index) => <li key={index}>{stateLabel(item.role)} · {item.model} · {item.summary}</li>)}</ul></details>
   </>}
  </Panel>
  <Panel title="评估运行记录"><ul className="divide-y divide-[var(--dl)]">{workspace.assessments.map(item => <li key={item.id} className="flex flex-wrap justify-between gap-2 py-3 text-sm"><span>{stateLabel(item.status)} · {stateLabel(item.phase) || '等待执行'}{item.stale ? ' · 旧版本' : ''}</span><time className="text-[var(--dm)]">{dateLabel(item.created_at)}</time>{item.error && <p className="w-full text-danger-600">{item.error}</p>}</li>)}</ul>{!workspace.assessments.length && <p className="text-sm text-[var(--dm)]">尚未启动评估。</p>}</Panel>
 </div>;
}
export function Paths({ assessment }: { assessment?: Assessment }) {
 const report = assessment?.report;
 return <div className="space-y-6"><Message>“已知解法共有”不等于绝对必要。“未找到替代解”不等于不可绕过；AC 也仅说明通过了当前测试。</Message>
  {!report?.paths?.length ? <Panel title="解法与 KC"><p className="text-sm text-[var(--dm)]">评估完成后，将在这里呈现按解法组织的知识组件与绕过关系。</p></Panel> : report.paths.map(path => <Panel key={path.id} title={path.name} description={(KINDS[path.kind] ?? path.kind) + ' · 验证状态：' + stateLabel(path.validation)}>
   <p className="whitespace-pre-wrap text-sm">{path.summary}</p><p className="text-sm">复杂度：{path.complexity}</p>
   <div className="flex flex-wrap gap-2">{path.kc_ids?.map(id => <span className="forge-badge" key={id}>{report.kcs?.find(kc => kc.id === id)?.name ?? id}</span>)}</div>
   {Boolean(path.bypasses?.length) && <p className="text-sm">候选绕过：{path.bypasses.join('、')}</p>}
   <p className="text-sm text-[var(--dm)]">{path.human_observations > 0 ? '已核实的人类路线观察：' + path.human_observations + ' 条' : '尚未关联已核实的人类路线观察；自报解法见人类反馈。'}</p>
   <details><summary className="cursor-pointer text-sm text-[var(--dm)]">论证、实现与证据</summary><p className="my-3 whitespace-pre-wrap text-sm">{path.proof}</p>{path.code && <pre className="max-h-96 overflow-auto rounded-lg bg-[var(--ds)] p-4 text-xs">{path.code}</pre>}<ul className="mt-3 space-y-2 text-sm">{path.evidence?.map(item => <li key={item.id}>{stateLabel(item.status)} · {item.summary}</li>)}</ul></details>
  </Panel>)}
  {Boolean(report?.kcs?.length) && <Panel title="知识组件定义" description="候选定义不会自动加入经过审核的知识目录。"><ul className="divide-y divide-[var(--dl)]">{report?.kcs.map(kc => <li key={kc.id} className="space-y-1 py-3 text-sm"><strong>{kc.name}</strong><span className="ml-2 text-xs text-[var(--dm)]">{kc.id} · {stateLabel(kc.status)}</span><p>{kc.definition}</p><p className="text-[var(--dm)]">适用条件：{kc.conditions}</p></li>)}</ul></Panel>}
 </div>;
}
export function Verification({ assessment }: { assessment?: Assessment }) {
 const report = assessment?.report;
 return <div className="space-y-6"><Message>反例可以否定错误解法，但不能证明人类容易被误导。数据漏洞和题目错误应先修复，不能用降低 rating 掩盖。</Message>
  <Panel title="验证与误区证据">{!report?.evidence?.length ? <p className="text-sm text-[var(--dm)]">目前没有可展示的验证证据。</p> : <ul className="divide-y divide-[var(--dl)]">{report.evidence.map(item => <li className="space-y-2 py-4 text-sm" key={item.id}><strong>{stateLabel(item.kind)} · {stateLabel(item.status)}</strong><p className="whitespace-pre-wrap">{item.summary}</p>{item.artifact_ref && <p className="break-all text-xs text-[var(--dm)]">证据引用：{item.artifact_ref}</p>}{item.details != null && <details><summary className="cursor-pointer text-[var(--dm)]">查看执行细节</summary><pre className="mt-2 max-h-80 overflow-auto rounded-lg bg-[var(--ds)] p-3 text-xs">{JSON.stringify(item.details, null, 2)}</pre></details>}</li>)}</ul>}</Panel>
  <Panel title="锚点比较">{!report?.comparisons?.length ? <p className="text-sm text-[var(--dm)]">参照不足，未形成可核查的比较关系。</p> : <ul className="divide-y divide-[var(--dl)]">{report.comparisons.map((comparison, index) => <li className="space-y-1 py-3 text-sm" key={index}><strong>{report.anchors.find(x => x.id === comparison.anchor_id)?.title ?? comparison.anchor_id} · {comparison.anchor_rating}</strong><p>{({ easier: '本题更容易', similar: '难度相近', harder: '本题更难', incomparable: '无法比较' } as Record<string, string>)[comparison.relation] ?? comparison.relation}：{comparison.reason}</p></li>)}</ul>}</Panel>
 </div>;
}
export function HumanFeedback({ workspace }: { workspace: Workspace }) {
 const human = workspace.human;
 return <div className="space-y-6"><Panel title="按条件观察人类作答" description="自报完成和外部链接均不会自动视为已验证通过；未知能力时不强行映射成 CF 分数。">
  <div className="grid gap-4 text-sm sm:grid-cols-3">{[['去重评价身份', human.total_reviewers], ['有效评价者', human.effective_reviewers], ['窗口内独立完成', human.independent_solved], ['完整窗口仍未完成', human.window_failures], ['未定或观察不足', human.censored], ['获得帮助', human.assisted]].map(([label, count]) => <div key={label}><p className="text-[var(--dm)]">{label}</p><strong className="mt-1 block text-xl">{count}</strong></div>)}</div><BulletList items={human.limitations} />
  {human.groups?.length > 0 && <div className="overflow-auto"><table className="w-full text-left text-sm"><thead><tr className="border-b border-[var(--dl)]"><th className="py-2">人群与条件</th><th>人数</th><th>独立完成</th><th>窗口失败</th><th>未定</th></tr></thead><tbody>{human.groups.map(group => <tr key={group.key} className="border-b border-[var(--dl)]"><td className="py-2">{groupLabel(group.key)}</td><td>{group.count}</td><td>{group.independent_solved}</td><td>{group.window_failures}</td><td>{group.censored}</td></tr>)}</tbody></table></div>}
 </Panel>
 <Panel title="当前版本反馈" description="重复提交保留修订历史，统计只计同一评价人的当前记录。">
  {!workspace.feedback.length ? <p className="text-sm text-[var(--dm)]">目前没有人类反馈。可以先发出专属评价邀请。</p> : <ul className="divide-y divide-[var(--dl)]">{workspace.feedback.map(item => <li className="space-y-2 py-4 text-sm" key={item.id}><p><strong>{item.reviewer_id.slice(0, 8)}</strong> · 修订 {item.revision} · {OUTCOME_LABELS[item.outcome] ?? item.outcome} · 有效耗时 {item.elapsed_minutes} 分钟 / 独立尝试 {item.independent_minutes} 分钟</p><p className="text-[var(--dm)]">{item.seen_before ? '见过本题' : '首次接触'} · {item.assistance.length ? '帮助：' + item.assistance.map(helpLabel).join('、') : '无帮助'} · 观察窗口 {item.window_minutes} 分钟 · {item.context === 'contest' ? '比赛' : '练习'}</p><p>首次路线：{item.first_route || '未填写'}</p><p>卡点：{item.blockers || '未填写'}</p><p>最终路线：{item.final_route || '未填写'}</p><p className="text-xs text-[var(--dm)]">结果为{item.result_source === 'external_link' ? '外部链接，尚未核验' : '本人自报'}{item.cf_rating !== undefined ? ' · 自报 CF ' + item.cf_rating : ' · 未提供能力信息'}</p></li>)}</ul>}
 </Panel></div>;
}
