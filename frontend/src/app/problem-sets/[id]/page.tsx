'use client';

import Link from 'next/link';
import GenerationConfigFields from '@/components/problem-sets/GenerationConfigFields';
import { defaultSetGenerationConfig, generationConfigForSet, isSetGenerationActive, SET_QUESTION_TYPES, setGenerationStatusLabel, setGenerationTotal, validateSetGenerationConfig } from '@/lib/problem-set-generation';
import { ViewTabs, ViewPanel } from '@/components/ui/ViewTabs';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowLeft, ArrowUp, ArrowDown, Download, Loader2, RefreshCw, Save, Sparkles, Trash2 } from 'lucide-react';

import {
  addProblemSetItem,
  startProblemSetGeneration,
  getProblemSetGeneration,
  cancelProblemSetGeneration,
  exportProblemSetURL,
  generateProblemSetPrompt,
  getProblemSet,
  listProblems,
  listQuizzes,
  removeProblemSetItem,
  reorderProblemSetItems,
  updateProblemSet,
} from '@/lib/api';
import type {
  Problem,
  ProblemSet,
  ProblemSetAddItemRequest,
  ProblemSetUpdateRequest,
  QuizProblem,
} from '@/lib/types';

export default function ProblemSetDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const [set, setSet] = useState<ProblemSet | null>(null);
  const [loading, setLoading] = useState(true);
  const [detailTab, setDetailTab] = useState('items');
  const initialViewChosen = useRef(false);
  const [working, setWorking] = useState(false);
  const [savingConfig, setSavingConfig] = useState(false);
  const [error, setError] = useState('');
  const [editTitle, setEditTitle] = useState('');
  const [editDescription, setEditDescription] = useState('');
  const [editSubject, setEditSubject] = useState('');
  const [editTags, setEditTags] = useState('');
  const [editStyle, setEditStyle] = useState('');
  const [editDifficulty, setEditDifficulty] = useState('');
  const [editGenerationConfig, setEditGenerationConfig] = useState(defaultSetGenerationConfig);
  const [stopRequested, setStopRequested] = useState(false);
  const [progressError, setProgressError] = useState('');
  const [editCooldown, setEditCooldown] = useState(2);
  const [generateOnSave, setGenerateOnSave] = useState(false);
  const [sourceType, setSourceType] = useState<'problem' | 'quiz'>('problem');
  const [sourceID, setSourceID] = useState('');
  const [score, setScore] = useState(100);
  const [section, setSection] = useState('');
  const [allowReuse, setAllowReuse] = useState(false);
  const [availableProblems, setAvailableProblems] = useState<Problem[]>([]);
  const [availableQuizzes, setAvailableQuizzes] = useState<QuizProblem[]>([]);
  const [sourceLoading, setSourceLoading] = useState(false);

  const load = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    setError('');
    try {
      const response = await getProblemSet(id);
      setSet(response.data ?? null);
      if (new URLSearchParams(window.location.search).has('generation_start_failed')) {
        setError('题集已保存，但自动启动失败。请检查模型配置后点击开始生成。');
        window.history.replaceState(null, '', window.location.pathname);
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '题集加载失败');
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    if (!set) return;
    setEditTitle(set.title);
    setEditDescription(set.description);
    setEditSubject(set.subject);
    setEditTags((set.tags ?? []).join('，'));
    setEditStyle(set.style_prompt);
    setEditDifficulty(set.difficulty_prompt);
    setEditGenerationConfig(generationConfigForSet(set));
    setEditCooldown(set.cooldown_sets);
  }, [set]);

  useEffect(() => {
    let cancelled = false;
    async function loadSources() {
      setSourceLoading(true);
      try {
        if (sourceType === 'problem') {
          const response = await listProblems({ status: 'published', page: 1, size: 100 });
          if (!cancelled) setAvailableProblems(response.data ?? []);
        } else {
          const response = await listQuizzes({ page: 1, size: 100 });
          if (!cancelled) setAvailableQuizzes((response.data ?? []).filter((quiz) => quiz.type !== 'programming'));
        }
      } catch {
        if (!cancelled) {
          if (sourceType === 'problem') setAvailableProblems([]);
          else setAvailableQuizzes([]);
        }
      } finally {
        if (!cancelled) setSourceLoading(false);
      }
    }
    void loadSources();
    return () => { cancelled = true; };
  }, [sourceType]);


  const generationActive = isSetGenerationActive(set?.generation?.status);
  useEffect(() => {
    if (!id || !generationActive) return;
    let cancelled = false;
    let inFlight = false;
    let loadedCount = -1;
    async function refreshProgress() {
      if (inFlight) return;
      inFlight = true;
      try {
        const state = await getProblemSetGeneration(id);
        const completed = state.data?.slots.filter((slot) => slot.status === 'succeeded').length ?? 0;
        if (completed !== loadedCount || !isSetGenerationActive(state.data?.status)) {
          const response = await getProblemSet(id);
          if (!cancelled && response.data) {
            setSet({ ...response.data, generation: state.data ?? undefined });
            loadedCount = completed;
          }
        } else if (!cancelled) {
          setSet((current) => current ? { ...current, generation: state.data ?? undefined } : current);
        }
        if (!cancelled) setProgressError('');
      } catch {
        if (!cancelled) setProgressError('进度暂时无法同步，后台任务会继续。正在自动重连。');
      } finally { inFlight = false; }
    }
    void refreshProgress();
    const timer = window.setInterval(() => { void refreshProgress(); }, 3000);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [id, generationActive]);

  const items = useMemo(() => set?.items ?? [], [set]);

  async function addItem() {
    if (!sourceID.trim()) { setError('请输入题目或客观题 UUID。'); return; }
    setWorking(true); setError('');
    const payload: ProblemSetAddItemRequest = {
      [sourceType === 'problem' ? 'problem_id' : 'quiz_id']: sourceID.trim(),
      score: Math.max(0, score),
      section: section.trim(),
    };
    try {
      const response = await addProblemSetItem(id, payload);
      setSet(response.data ?? null);
      setSourceID('');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '加入题集失败');
    } finally { setWorking(false); }
  }

  async function saveConfiguration(startGeneration = false) {
    if (!editTitle.trim()) { setError('题集名称不能为空。'); return; }
    const configError = validateSetGenerationConfig(editGenerationConfig);
    if (configError) { setError(configError); return; }
    const payload: ProblemSetUpdateRequest = {
      title: editTitle.trim(), description: editDescription.trim(), subject: editSubject.trim(),
      tags: editTags.split(/[,，\n]/).map((item) => item.trim()).filter(Boolean),
      style_prompt: editStyle.trim(), difficulty_prompt: editDifficulty.trim(),
      desired_item_count: setGenerationTotal(editGenerationConfig),
      generation_config: editGenerationConfig,
      cooldown_sets: Math.max(0, Math.min(50, editCooldown)),
      generate_prompt: startGeneration ? false : generateOnSave,
    };
    setSavingConfig(true); setError('');
    try {
      const response = await updateProblemSet(id, payload);
      setSet(response.data ?? null);
      if (startGeneration) {
        const started = await startProblemSetGeneration(id);
        setSet((current) => current ? { ...current, generation: started.data } : current);
        setStopRequested(false);
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存或启动失败，请检查模型配置后重试。');
    } finally { setSavingConfig(false); }
  }

  async function stopGeneration() {
    setWorking(true); setError('');
    try { await cancelProblemSetGeneration(id); setStopRequested(true); }
    catch (cause) { setError(cause instanceof Error ? cause.message : '停止失败，请重试。'); }
    finally { setWorking(false); }
  }

  async function resumeQueuedGeneration() {
    setWorking(true); setError('');
    try {
      const response = await startProblemSetGeneration(id);
      setSet((current) => current ? { ...current, generation: response.data } : current);
    } catch (cause) { setError(cause instanceof Error ? cause.message : '启动失败，请稍后重试。'); }
    finally { setWorking(false); }
  }

  async function moveItem(index: number, delta: number) {
    const ids = items.map((item) => item.id);
    const target = index + delta;
    if (target < 0 || target >= ids.length) return;
    [ids[index], ids[target]] = [ids[target], ids[index]];
    setWorking(true); setError('');
    try {
      const response = await reorderProblemSetItems(id, ids);
      setSet(response.data ?? null);
    } catch (cause) { setError(cause instanceof Error ? cause.message : '调整顺序失败'); }
    finally { setWorking(false); }
  }

  async function removeItem(itemID: string) {
    if (!window.confirm('确定从题集中移除这道题吗？')) return;
    setWorking(true); setError('');
    try {
      const response = await removeProblemSetItem(id, itemID);
      setSet(response.data ?? null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '移除题目失败');
    } finally { setWorking(false); }
  }

  async function regeneratePrompt() {
    setWorking(true); setError('');
    try {
      const response = await generateProblemSetPrompt(id);
      setSet(response.data ?? null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '提示词生成失败，请检查模型配置');
    } finally { setWorking(false); }
  }

  useEffect(() => {
    if (!set || initialViewChosen.current) return;
    initialViewChosen.current = true;
    if (isSetGenerationActive(set.generation?.status)) setDetailTab('generation');
  }, [set]);

  if (loading && !set) return <div className="forge-page"><p className="text-anvil-500">加载中…</p></div>;
  if (!set) return <div className="forge-page"><div className="rounded-lg bg-danger-50 p-4 text-danger-600">{error || '题集不存在'}</div></div>;

  const quality = set.quality;
  return (
    <div className="af-page af-set-content">
      <Link href="/problem-sets" className="inline-flex items-center gap-2 text-sm text-anvil-500 hover:text-forge-600"><ArrowLeft className="h-4 w-4" /> 返回题集</Link>
<div className="af-detail-header">
        <div>
          <h1 className="text-2xl font-bold">{set.title}</h1>
          <p className="mt-1 font-mono text-xs text-anvil-400">{set.code} · {({ contest: '比赛', mock_exam: '模拟考试', homework: '作业', curriculum: '课程' } as Record<string, string>)[set.kind] ?? set.kind} · {({ draft: '草稿', published: '已发布', archived: '已归档' } as Record<string, string>)[set.status] ?? set.status}</p>
          {set.description && <p className="mt-2 text-sm text-anvil-600 dark:text-anvil-300">{set.description}</p>}
        </div>
        <div className="flex flex-wrap gap-2">
          <button className="forge-btn-secondary" onClick={() => void load()} disabled={loading}><RefreshCw className="h-4 w-4" />刷新</button>
          <button className="forge-btn-secondary" onClick={() => void regeneratePrompt()} disabled={working || generationActive || savingConfig}><Sparkles className="h-4 w-4" />重新生成提示词</button>
          <a className={"forge-btn-primary" + (generationActive ? " pointer-events-none opacity-50" : "")} aria-disabled={generationActive} tabIndex={generationActive ? -1 : undefined} href={generationActive ? undefined : exportProblemSetURL(id, allowReuse)}><Download className="h-4 w-4" />下载题集 ZIP</a>
        </div>
      </div>
{error && <div className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600">{error}</div>}
      <ViewTabs id="set-content" value={detailTab} onChange={setDetailTab} items={[
        { id: 'items', label: '题目目录（' + items.length + '）' }, { id: 'generation', label: '生成进度' },
        { id: 'settings', label: '题集需求' }, { id: 'quality', label: '质量与去重' },
      ]} />
<ViewPanel id="set-content" name="items" active={detailTab}><div className="grid gap-4 lg:grid-cols-3">
        <section className="forge-card lg:col-span-2">
          <div className="flex items-center justify-between gap-2">
            <h2 className="forge-section-title">题目编排（{items.length} 道）</h2>
            <span className="text-sm text-anvil-500">总分 {set.total_score}</span>
          </div>
          <div className="mt-4 space-y-2">
            {items.length === 0 ? <p className="py-8 text-center text-sm text-anvil-400">还没有题目。可以开始自动生成，也可以从题库加入。</p> : items.map((item, index) => (
              <div key={item.id} className="flex items-center justify-between gap-3 rounded-lg border border-anvil-200 p-3 dark:border-anvil-700">
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-xs text-anvil-400"><span>#{item.position}</span><span>{item.problem_id ? '编程题' : '客观题'}</span><span>{item.score} 分</span></div>
                  <p className="truncate font-medium">{item.problem?.title ?? item.quiz?.title ?? item.problem_id ?? item.quiz_id}</p>
                  {item.knowledge_point_keys && <p className="truncate text-xs text-anvil-400">{item.knowledge_point_keys.filter((key) => !key.includes(':') || !key.match(/^[^:]+:[0-9a-f-]{36}$/)).slice(0, 5).join(' · ')}</p>}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <button className="text-anvil-500 disabled:opacity-30" onClick={() => void moveItem(index, -1)} disabled={index === 0 || working || generationActive || savingConfig} aria-label={'上移第 ' + (index + 1) + ' 题'}><ArrowUp className="h-4 w-4" /></button>
                  <button className="text-anvil-500 disabled:opacity-30" onClick={() => void moveItem(index, 1)} disabled={index === items.length - 1 || working || generationActive || savingConfig} aria-label={'下移第 ' + (index + 1) + ' 题'}><ArrowDown className="h-4 w-4" /></button>
                  <button className="text-danger-500 hover:text-danger-600" onClick={() => void removeItem(item.id)} disabled={working || generationActive || savingConfig} aria-label="移除"><Trash2 className="h-4 w-4" /></button>
                </div>
              </div>
            ))}
          </div>
        </section>

        <section className="forge-card space-y-3">
          <h2 className="forge-section-title">加入题目</h2>
          <select className="forge-input" value={sourceType} onChange={(event) => setSourceType(event.target.value as 'problem' | 'quiz')}>
            <option value="problem">已发布编程题</option>
            <option value="quiz">客观题</option>
          </select>
          <select
            className="forge-input"
            value={sourceID}
            onChange={(event) => setSourceID(event.target.value)}
            disabled={sourceLoading || generationActive}
          >
            <option value="">从题库选择（也可下方粘贴 UUID）</option>
            {sourceType === 'problem'
              ? availableProblems.map((problem) => (
                <option key={problem.id} value={problem.id}>
                  {problem.serial_number || problem.id.slice(0, 8)} · {problem.title}
                </option>
              ))
              : availableQuizzes.map((quiz) => (
                <option key={quiz.id} value={quiz.id}>{quiz.title}</option>
              ))}
          </select>
          <input className="forge-input" value={sourceID} onChange={(event) => setSourceID(event.target.value)} placeholder="或粘贴题目 UUID" />
          <div className="grid grid-cols-2 gap-2">
            <input className="forge-input" type="number" min={0} value={score} onChange={(event) => setScore(Number(event.target.value))} placeholder="分值" />
            <input className="forge-input" value={section} onChange={(event) => setSection(event.target.value)} placeholder="分区（可选）" />
          </div>
          <button className="forge-btn-primary w-full" onClick={() => void addItem()} disabled={working || generationActive || savingConfig}>
            {working ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}加入题集
          </button>
          <p className="text-xs text-anvil-500">编程题必须已经通过发布门禁并有完整测试数据；客观题保留选择、填空与判断的答案和解析，分值随题集清单保存。</p>
        </section>
      </div></ViewPanel>
<ViewPanel id="set-content" name="generation" active={detailTab}><section className="forge-card space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="forge-section-title">{set.generation_config?.assembly ? '补齐缺少的题目（可选）' : '自动生成整套题目'}</h2>
            <p className="mt-1 text-sm text-anvil-500">{set.generation ? setGenerationStatusLabel(set.generation.status) : set.generation_config?.assembly ? '本题集从已有题库组卷。缺题时可手工加入，或在确认需求后生成补齐。' : '填写下方需求并开始，系统会自动规划、生成、校验和入集。'}</p>
          </div>
          {generationActive ? <div className="flex gap-2">
            {set.generation?.status === 'queued' && <button className="forge-btn-secondary" disabled={working} onClick={() => void resumeQueuedGeneration()}>重新提交启动</button>}
            <button className="forge-btn-secondary" disabled={working || stopRequested} onClick={() => void stopGeneration()}>{stopRequested ? '正在停止…' : '停止生成'}</button>
          </div> : <button className="forge-btn-primary" disabled={working || savingConfig} onClick={() => { setDetailTab('generation'); void saveConfiguration(true); }}>
            {savingConfig ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}
            {set.generation && set.generation.status !== 'completed' ? '保存并重试未完成题目' : set.generation_config?.assembly ? '保存需求并生成补齐' : '保存并开始生成'}
          </button>}
        </div>
        <p className="text-xs text-anvil-500">成功题目逐题保存。可关闭页面后再查看；停止或失败后，重试只补齐未完成的题目。已有题目会计入题型配额，修改需求不会重写已入集的题目。</p>
        {progressError && <p className="text-sm text-warning-600">{progressError}</p>}
        {set.generation?.error && <p className="text-sm text-danger-600">{set.generation.error}</p>}
        {set.generation && <>
          <div className="flex items-center gap-3 text-sm">
            <progress className="h-2 flex-1 accent-forge-500" max={set.generation.slots.length || 1} value={set.generation.slots.filter((slot) => slot.status === 'succeeded').length} />
            <span>{set.generation.slots.filter((slot) => slot.status === 'succeeded').length} / {set.generation.slots.length} 已完成</span>
          </div>
          <div className="max-h-72 space-y-2 overflow-auto">
            {set.generation.slots.map((slot) => <div key={slot.position} className="rounded-lg border border-anvil-200 p-3 text-sm dark:border-anvil-700">
              <div className="flex flex-wrap justify-between gap-2"><span>#{slot.position} · {SET_QUESTION_TYPES.find((t) => t.type === slot.type)?.label} · {slot.title || '等待规划'}</span><span className={slot.status === 'failed' ? 'text-danger-600' : slot.status === 'succeeded' ? 'text-success-700' : 'text-anvil-500'}>{setGenerationStatusLabel(slot.status)}</span></div>
              {slot.error && slot.status === 'failed' && <p className="mt-1 text-xs text-danger-600">{slot.error}</p>}
            </div>)}
          </div>
        </>}
      </section><section className="forge-card">
        <div className="flex items-center justify-between gap-2"><h2 className="forge-section-title">内部出题提示词</h2><span className="text-xs text-anvil-400">不会展示给参赛者</span></div>
        {set.generated_prompt ? <pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap rounded-lg bg-anvil-50 p-4 text-sm leading-6 dark:bg-anvil-950">{set.generated_prompt}</pre> : <p className="mt-3 text-sm text-anvil-400">开始自动生成后会在这里保存整套命题规划，也可单独生成规划。</p>}
      </section></ViewPanel>
<ViewPanel id="set-content" name="settings" active={detailTab}><section className="forge-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="forge-section-title">题集配置</h2>
            <p className="mt-1 text-xs text-anvil-500">修改风格、难度或覆盖范围后，旧的内部提示词会失效。</p>
          </div>
          <button className="forge-btn-primary" onClick={() => void saveConfiguration()} disabled={savingConfig || working || generationActive}>
            {savingConfig ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}保存配置
          </button>
        </div>
        <fieldset disabled={generationActive || savingConfig || working} className="space-y-4 disabled:opacity-60">
        <GenerationConfigFields value={editGenerationConfig} onChange={setEditGenerationConfig} />
        <div className="grid gap-4 md:grid-cols-2">
          <label className="space-y-1 text-sm"><span>题集名称</span><input className="forge-input" value={editTitle} onChange={(event) => setEditTitle(event.target.value)} /></label>
          <label className="space-y-1 text-sm"><span>学科/方向</span><input className="forge-input" value={editSubject} onChange={(event) => setEditSubject(event.target.value)} /></label>
        </div>
        <label className="block space-y-1 text-sm"><span>说明</span><input className="forge-input" value={editDescription} onChange={(event) => setEditDescription(event.target.value)} placeholder="这场比赛希望解决什么训练目标？" /></label>
        <label className="block space-y-1 text-sm"><span>覆盖标签（逗号分隔）</span><input className="forge-input" value={editTags} onChange={(event) => setEditTags(event.target.value)} /></label>
        <div className="grid gap-4 md:grid-cols-2">
          <label className="space-y-1 text-sm"><span>风格描述</span><textarea className="forge-input min-h-24" value={editStyle} onChange={(event) => setEditStyle(event.target.value)} /></label>
          <label className="space-y-1 text-sm"><span>难度描述</span><textarea className="forge-input min-h-24" value={editDifficulty} onChange={(event) => setEditDifficulty(event.target.value)} /></label>
        </div>
        <div className="grid gap-4 md:grid-cols-3">
          <label className="space-y-1 text-sm"><span>回看台账场数</span><input className="forge-input" type="number" min={0} max={50} value={editCooldown} onChange={(event) => setEditCooldown(Number(event.target.value))} /></label>
          <label className="flex items-end gap-2 pb-2 text-sm"><input type="checkbox" checked={generateOnSave} onChange={(event) => setGenerateOnSave(event.target.checked)} />保存后重新生成提示词</label>
        </div>
        </fieldset>
      </section></ViewPanel>
<ViewPanel id="set-content" name="quality" active={detailTab}><section className="forge-card space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 className="forge-section-title">质量门禁与去重台账</h2>
          {quality && <span className={quality.ready_for_export ? 'forge-badge bg-success-100 text-success-700' : 'forge-badge bg-warning-100 text-warning-700'}>{quality.ready_for_export ? '可导出' : '需处理质量门禁'}</span>}
        </div>
        {quality ? <div className="grid gap-3 text-sm md:grid-cols-4">
          <div><span className="text-anvil-500">{set.desired_item_count > 0 ? '目标题数' : '建议题数'}</span><p className="font-semibold">{quality.recommended_item_count}（当前 {quality.item_count}）</p></div>
          <div><span className="text-anvil-500">知识点覆盖</span><p className="font-semibold">{quality.knowledge_point_count}</p></div>
          <div><span className="text-anvil-500">近期重复知识点</span><p className="font-semibold">{quality.reused_knowledge_points?.length ?? 0}</p></div>
          <div><span className="text-anvil-500">近期重复题目</span><p className="font-semibold">{quality.reused_items?.length ?? 0}</p></div>
        </div> : <p className="text-sm text-anvil-400">质量报告加载中…</p>}
        {quality?.blocking_issues?.map((issue) => <p key={issue} className="text-sm text-danger-600">阻塞：{issue}</p>)}
        {quality?.warnings?.map((warning) => <p key={warning} className="text-sm text-warning-600">提示：{warning}</p>)}
        {quality && ((quality.reused_knowledge_points?.length ?? 0) > 0 || (quality.reused_items?.length ?? 0) > 0) && <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={allowReuse} onChange={(event) => setAllowReuse(event.target.checked)} />本次导出明确允许复用（会记录在台账中）</label>}
      </section></ViewPanel>

    </div>
  );
}
