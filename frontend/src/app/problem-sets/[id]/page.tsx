'use client';

import { downloadProblemSetTesting, parseExportTagCatalog } from '@/lib/problem-set-download';
import { useAuth } from '@/components/auth/AuthProvider';
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
  const { isAdmin, session } = useAuth();
  const params = useParams<{ id: string }>();
  const id = params.id;
  const [set, setSet] = useState<ProblemSet | null>(null);
  const canManage = isAdmin || Boolean(set?.owner_user_id && set.owner_user_id === session?.user?.id && set.visibility === 'private');
  const [loading, setLoading] = useState(true);
  const [detailTab, setDetailTab] = useState('items');
  const initialViewChosen = useRef(false);
  const [working, setWorking] = useState(false);
  const [savingConfig, setSavingConfig] = useState(false);
  const [error, setError] = useState('');
  const [downloading, setDownloading] = useState<'generic' | 'hydro' | null>(null);
  const [customNumbering, setCustomNumbering] = useState(false);
  const [numberPrefix, setNumberPrefix] = useState('');
  const [startIndex, setStartIndex] = useState(1);
  const [tagCatalog, setTagCatalog] = useState<{ activeTagsTree: unknown[] }>();
  const [tagFileName, setTagFileName] = useState('');
  const [tagCatalogError, setTagCatalogError] = useState('');
  const [readingTags, setReadingTags] = useState(false);
  const tagReadSequence = useRef(0);

  async function selectTagCatalog(file?: File) {
    const sequence = ++tagReadSequence.current;
    setTagCatalog(undefined); setTagFileName(''); setTagCatalogError('');
    if (!file) { setReadingTags(false); return; }
    setReadingTags(true);
    try {
      if (file.size > 4 * 1024 * 1024) throw new Error('标签 JSON 文件最多支持 4 MiB。');
      const parsed = parseExportTagCatalog(await file.text());
      if (sequence === tagReadSequence.current) { setTagCatalog(parsed); setTagFileName(file.name); }
    } catch (cause) {
      if (sequence === tagReadSequence.current) setTagCatalogError(cause instanceof Error ? cause.message : '无法读取标签 JSON');
    } finally { if (sequence === tagReadSequence.current) setReadingTags(false); }
  }

  async function downloadTesting(format: 'generic' | 'hydro') {
    if (!set || downloading) return;
    setDownloading(format); setError('');
    try { await downloadProblemSetTesting(id, format, set.code.replace(/[^a-zA-Z0-9_-]/g, '') + '-' + format + '.zip', format === 'generic' ? tagCatalog : undefined, customNumbering ? { prefix: numberPrefix, start: startIndex } : undefined); }
    catch (cause) { setError(cause instanceof Error ? cause.message : '下载失败'); }
    finally { setDownloading(null); }
  }
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
      if (!canManage) return;
      setSourceLoading(true);
      try {
        if (sourceType === 'problem') {
          const response = await listProblems({ page: 1, size: 100 });
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
  }, [sourceType, canManage]);


  const generationActive = isSetGenerationActive(set?.generation?.status);
  useEffect(() => {
    if (!id || !generationActive || !canManage) return;
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
  }, [id, generationActive, canManage]);

  const items = useMemo(() => set?.items ?? [], [set]);

  async function addItem() {
    if (!canManage) return;
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
    if (!canManage) return;
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
    if (!canManage) return;
    setWorking(true); setError('');
    try { await cancelProblemSetGeneration(id); setStopRequested(true); }
    catch (cause) { setError(cause instanceof Error ? cause.message : '停止失败，请重试。'); }
    finally { setWorking(false); }
  }

  async function resumeQueuedGeneration() {
    if (!canManage) return;
    setWorking(true); setError('');
    try {
      const response = await startProblemSetGeneration(id);
      setSet((current) => current ? { ...current, generation: response.data } : current);
    } catch (cause) { setError(cause instanceof Error ? cause.message : '启动失败，请稍后重试。'); }
    finally { setWorking(false); }
  }

  async function moveItem(index: number, delta: number) {
    if (!canManage) return;
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
    if (!canManage) return;
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
    if (!canManage) return;
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
    if (canManage && isSetGenerationActive(set.generation?.status)) setDetailTab('generation');
  }, [set, canManage]);

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
          <button className="forge-btn-secondary" onClick={() => void regeneratePrompt()} disabled={!canManage || working || generationActive || savingConfig}><Sparkles className="h-4 w-4" />重新生成提示词</button>
          <button className="forge-btn-primary" disabled={generationActive || !items.length || Boolean(downloading) || readingTags || Boolean(tagCatalogError)} onClick={() => void downloadTesting('generic')}>{downloading === 'generic' ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}通用 ZIP</button>
          <button className="forge-btn-secondary" disabled={generationActive || !items.length || Boolean(downloading) || items.some(item => !item.problem_id || Boolean(item.quiz_id))} onClick={() => void downloadTesting('hydro')}>{downloading === 'hydro' ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}Hydro ZIP</button>
        </div>
      </div>
<p className="text-sm text-[var(--dm)]">通用 ZIP 包含模板 Excel、datas 测试数据和题集清单，可保留全部题型；Hydro ZIP 可整包导入 Hydro 编程题库。两者供 OJ 测试，不改变题目的发布状态。{items.some(item => !item.problem_id || Boolean(item.quiz_id)) && ' 当前含客观题，请下载通用 ZIP；Hydro 不支持这些题型。'}</p>
<details className="rounded-lg border border-[var(--dl)] p-3 text-sm">
  <summary className="cursor-pointer font-medium">导出题目编号（可选）</summary>
  <div className="mt-3 space-y-3">
    <label className="flex items-center gap-2"><input type="checkbox" checked={customNumbering} disabled={Boolean(downloading)} onChange={event => setCustomNumbering(event.target.checked)} />自定义编号</label>
    {customNumbering && <div className="flex flex-wrap items-end gap-4">
      <label className="space-y-1">编号前缀<input className="forge-input" value={numberPrefix} maxLength={24} placeholder="例如 Atc" disabled={Boolean(downloading)} onChange={event => setNumberPrefix(event.target.value)} /></label>
      <label className="space-y-1">起始编号<input className="forge-input w-32" type="number" min={1} max={999999} value={startIndex} disabled={Boolean(downloading)} onChange={event => setStartIndex(Number(event.target.value))} /></label>
      <p>编程题编号示例：<code>{numberPrefix}P{String(startIndex).padStart(3, '0')}</code></p>
    </div>}
    <p className="text-[var(--dm)]">按题集顺序递增，至少保留三位数字；通用 ZIP 和 Hydro ZIP 均适用。仅改变这次下载的编号，题库原编号保留。导入同一 OJ 时请选择未占用的编号范围。</p>
  </div>
</details>
<details className="rounded-lg border border-[var(--dl)] p-3 text-sm">
  <summary className="cursor-pointer font-medium">通用 ZIP 标签映射{tagFileName ? '：已选择标签目录' : '（可选）'}</summary>
  <div className="mt-3 space-y-2">
    <p className="text-[var(--dm)]">选择目标 OJ 导出的标签 JSON。本次下载按有效 ID、完整路径或唯一名称匹配；同名和未匹配标签列在导出说明中。未选择目录时标签列留空，原标签仍保留。</p>
    <label className="block">标签目录 JSON<input className="mt-1 block w-full" type="file" accept=".json,application/json" disabled={Boolean(downloading)} onChange={event => void selectTagCatalog(event.target.files?.[0])} /></label>
    {readingTags && <p>正在读取标签目录…</p>}
    {tagFileName && <p>{tagFileName} · 仅用于当前页面的下载，不保存为全局配置</p>}
    {tagCatalogError && <p role="alert" className="text-danger-600">{tagCatalogError}</p>}
    <p className="text-[var(--dm)]">编程题按六档 rating 区间导出，原始分数保留在清单及说明中。语言编号未确认时不限制语言。</p>
  </div>
</details>
{!canManage && <p className="rounded-lg border border-[var(--dl)] p-3 text-sm text-[var(--dm)]">这是团队共享题集。你可以查看与导出；修改由管理员统一管理。</p>}
{error && <div className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600">{error}</div>}
      <ViewTabs id="set-content" value={detailTab} onChange={setDetailTab} items={[
        { id: 'items', label: '题目目录（' + items.length + '）' }, ...(canManage ? [{ id: 'generation', label: '生成进度' }] : []),
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
                  <Link className="af-link font-medium" href={(item.problem_id ? '/problems/' : '/quizzes/') + (item.problem_id ?? item.quiz_id)}>{item.problem?.title ?? item.quiz?.title ?? item.problem_id ?? item.quiz_id}</Link>
                  {item.section && <p className="mt-1 text-xs text-[var(--dm)]">{item.section}</p>}
                  {item.notes && <p className="mt-1 whitespace-pre-wrap text-sm text-[var(--dm)]">{item.notes}</p>}
                  {item.knowledge_point_keys && <p className="truncate text-xs text-anvil-400">{item.knowledge_point_keys.filter((key) => !key.includes(':') || !key.match(/^[^:]+:[0-9a-f-]{36}$/)).slice(0, 5).join(' · ')}</p>}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <button className="text-anvil-500 disabled:opacity-30" onClick={() => void moveItem(index, -1)} disabled={!canManage || index === 0 || working || generationActive || savingConfig} aria-label={'上移第 ' + (index + 1) + ' 题'}><ArrowUp className="h-4 w-4" /></button>
                  <button className="text-anvil-500 disabled:opacity-30" onClick={() => void moveItem(index, 1)} disabled={!canManage || index === items.length - 1 || working || generationActive || savingConfig} aria-label={'下移第 ' + (index + 1) + ' 题'}><ArrowDown className="h-4 w-4" /></button>
                  <button className="text-danger-500 hover:text-danger-600" onClick={() => void removeItem(item.id)} disabled={!canManage || working || generationActive || savingConfig} aria-label="移除"><Trash2 className="h-4 w-4" /></button>
                </div>
              </div>
            ))}
          </div>
        </section>

        <section style={{ display: canManage ? undefined : "none" }} className="forge-card space-y-3">
          <h2 className="forge-section-title">加入题目</h2>
          {!generationActive && <Link href={'/problems?set=' + id} className="forge-btn-primary w-full">打开题库，批量选题</Link>}
          <p className="af-hint">批量选题支持搜索、分页和重复题自动跳过。下方也可单题加入。</p>
          <select className="forge-input" value={sourceType} onChange={(event) => setSourceType(event.target.value as 'problem' | 'quiz')}>
            <option value="problem">编程题</option>
            <option value="quiz">客观题</option>
          </select>
          <select
            className="forge-input"
            value={sourceID}
            onChange={(event) => setSourceID(event.target.value)}
            disabled={sourceLoading || generationActive}
          >
            <option value="">最近 100 题；更多题目请使用批量选题</option>
            {sourceType === 'problem'
              ? availableProblems.filter(problem => !items.some(item => item.problem_id === problem.id)).map((problem) => (
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
          <button className="forge-btn-primary w-full" onClick={() => void addItem()} disabled={!canManage || working || generationActive || savingConfig}>
            {working ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}加入题集
          </button>
          <p className="text-xs text-anvil-500">草稿和待审题可以先编排，导出时再检查题面与测试数据。加入题集不会自动发布题目。</p>
        </section>
      </div></ViewPanel>
<ViewPanel id="set-content" name="generation" active={detailTab}><section className="forge-card space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="forge-section-title">{set.generation_config?.assembly ? '补齐缺少的题目（可选）' : '自动生成整套题目'}</h2>
            <p className="mt-1 text-sm text-anvil-500">{set.generation ? setGenerationStatusLabel(set.generation.status) : set.generation_config?.assembly ? '本题集从已有题库组卷。缺题时可手工加入，或在确认需求后生成补齐。' : '填写下方需求并开始，系统会自动规划、生成、校验和入集。'}</p>
          </div>
          {generationActive ? <div className="flex gap-2">
            {set.generation?.status === 'queued' && <button className="forge-btn-secondary" disabled={!canManage || working} onClick={() => void resumeQueuedGeneration()}>重新提交启动</button>}
            <button className="forge-btn-secondary" disabled={!canManage || working || stopRequested} onClick={() => void stopGeneration()}>{stopRequested ? '正在停止…' : '停止生成'}</button>
          </div> : <button className="forge-btn-primary" disabled={!canManage || working || savingConfig} onClick={() => { setDetailTab('generation'); void saveConfiguration(true); }}>
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
          <button className="forge-btn-primary" onClick={() => void saveConfiguration()} disabled={!canManage || savingConfig || working || generationActive}>
            {savingConfig ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}保存配置
          </button>
        </div>
        <fieldset disabled={!canManage || generationActive || savingConfig || working} className="space-y-4 disabled:opacity-60">
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
          <h2 className="forge-section-title">正式发布门禁与去重台账</h2>
          {quality && <span className={quality.ready_for_export ? 'forge-badge bg-success-100 text-success-700' : 'forge-badge bg-warning-100 text-warning-700'}>{quality.ready_for_export ? '满足正式发布要求' : '正式发布前需处理'}</span>}
        </div>
        {quality ? <div className="grid gap-3 text-sm md:grid-cols-4">
          <div><span className="text-anvil-500">{set.desired_item_count > 0 ? '目标题数' : '建议题数'}</span><p className="font-semibold">{quality.recommended_item_count}（当前 {quality.item_count}）</p></div>
          <div><span className="text-anvil-500">知识点覆盖</span><p className="font-semibold">{quality.knowledge_point_count}</p></div>
          <div><span className="text-anvil-500">近期重复知识点</span><p className="font-semibold">{quality.reused_knowledge_points?.length ?? 0}</p></div>
          <div><span className="text-anvil-500">近期重复题目</span><p className="font-semibold">{quality.reused_items?.length ?? 0}</p></div>
        </div> : <p className="text-sm text-anvil-400">质量报告加载中…</p>}
        {quality?.blocking_issues?.map((issue) => <p key={issue} className="text-sm text-danger-600">阻塞：{issue}</p>)}
        {quality?.warnings?.map((warning) => <p key={warning} className="text-sm text-warning-600">提示：{warning}</p>)}
        <p className="text-xs text-anvil-500">上方 ZIP 用于 OJ 测试，按实际数据验证结果提供下载；这里的正式发布要求不会自动改变题目的状态。</p>
      </section></ViewPanel>

    </div>
  );
}
