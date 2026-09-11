'use client';

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import {
  CheckCircle2,
  Cpu,
  ExternalLink,
  KeyRound,
  Loader2,
  Plug,
  Save,
  ShieldCheck,
} from 'lucide-react';

import { PageHeader } from '@/components/ui/Workspace';
import ApiHealthBanner from '@/components/ApiHealthBanner';
import {
  deployLocalEmbeddingModel,
  getEmbeddingRuntimeSettings,
  getPermanentLLMSettings,
  resetPermanentLLMSetting,
  testLocalEmbeddingEndpoint,
  updatePermanentLLMSetting,
} from '@/lib/api';
import type {
  EmbeddingRuntimeSettings,
  LLMOverridePurpose,
  LLMProviderPurpose,
  LocalEmbeddingEndpointConfig,
  LocalEmbeddingTestResult,
  LLMReasoningEffort,
  PersistentLLMProviderSetting,
  PersistentLLMProviderSettings,
} from '@/lib/types';

interface ProviderFormState {
  base_url: string;
  model: string;
  reasoning_effort: LLMReasoningEffort;
  api_key: string;
}

interface EmbeddingFormState {
  base_url: string;
  model: string;
  api_key: string;
}

interface FormErrors {
  base_url?: string;
  model?: string;
  api_key?: string;
}

const EMPTY_PROVIDER_FORM: ProviderFormState = {
  base_url: '',
  model: '',
  reasoning_effort: '',
  api_key: '',
};

const REASONING_EFFORT_OPTIONS: Array<{
  value: LLMReasoningEffort;
  label: string;
}> = [
  { value: '', label: '不指定（不发送）' },
  { value: 'none', label: 'none · 不启用' },
  { value: 'minimal', label: 'minimal · 最低' },
  { value: 'low', label: 'low · 低' },
  { value: 'medium', label: 'medium · 中' },
  { value: 'high', label: 'high · 高' },
  { value: 'xhigh', label: 'xhigh · 很高' },
  { value: 'max', label: 'max · 最大' },
];

const EMPTY_EMBEDDING_FORM: EmbeddingFormState = {
  base_url: '',
  model: '',
  api_key: '',
};

const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f]/;

function httpURLValidationError(raw: string): string | undefined {
  const value = raw.trim();
  if (!value) return 'Base URL 不能为空。';
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return 'Base URL 仅支持 HTTP 或 HTTPS。';
    }
    if (parsed.username || parsed.password) {
      return '请勿把凭据写入 Base URL。';
    }
  } catch {
    return '请输入完整有效的 Base URL。';
  }
  return undefined;
}

function apiKeyValidationError(
  value: string,
  required: boolean,
): string | undefined {
  const trimmed = value.trim();
  if (!trimmed) return required ? '请输入 API Key。' : undefined;
  if (trimmed.length > 4096) return 'API Key 不能超过 4096 个字符。';
  if (CONTROL_CHARACTERS.test(trimmed)) return 'API Key 不能包含控制字符。';
  return undefined;
}

function validateProviderForm(
  form: ProviderFormState,
  reusableKey: boolean,
): FormErrors {
  return {
    base_url: httpURLValidationError(form.base_url),
    model: form.model.trim() ? undefined : '模型名称不能为空。',
    api_key: apiKeyValidationError(form.api_key, !reusableKey),
  };
}

function endpointCanOmitAPIKey(raw: string): boolean {
  try {
    const hostname = new URL(raw.trim()).hostname.toLowerCase();
    if (hostname === 'localhost' || hostname === 'host.docker.internal') {
      return true;
    }
    if (
      hostname === '::1' ||
      hostname.startsWith('fc') ||
      hostname.startsWith('fd') ||
      hostname.startsWith('fe8') ||
      hostname.startsWith('fe9') ||
      hostname.startsWith('fea') ||
      hostname.startsWith('feb')
    ) {
      return true;
    }
    const octets = hostname.split('.').map(Number);
    if (
      octets.length !== 4 ||
      octets.some(
        (part) => !Number.isInteger(part) || part < 0 || part > 255,
      )
    ) {
      return false;
    }
    return (
      octets[0] === 10 ||
      octets[0] === 127 ||
      (octets[0] === 169 && octets[1] === 254) ||
      (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31) ||
      (octets[0] === 192 && octets[1] === 168)
    );
  } catch {
    return false;
  }
}

function validateEmbeddingForm(
  form: EmbeddingFormState,
  reusableKey: boolean,
): FormErrors {
  return {
    base_url: httpURLValidationError(form.base_url),
    model: form.model.trim() ? undefined : '模型名称不能为空。',
    api_key: apiKeyValidationError(
      form.api_key,
      !reusableKey && !endpointCanOmitAPIKey(form.base_url),
    ),
  };
}

function hasErrors(errors: FormErrors): boolean {
  return Object.values(errors).some(Boolean);
}

function safeErrorMessage(
  cause: unknown,
  fallback: string,
  secrets: string[] = [],
): string {
  let message = cause instanceof Error ? cause.message : fallback;
  for (const secret of secrets) {
    const value = secret.trim();
    if (value) message = message.split(value).join('[已隐藏]');
  }
  return message || fallback;
}

function normalizeURL(value: string): string {
  return value.trim().replace(/\/+$/, '');
}

function endpointIdentity(baseURL: string, model: string): string {
  return `${normalizeURL(baseURL)}\n${model.trim()}`;
}

function formFromSetting(
  setting?: PersistentLLMProviderSetting,
): ProviderFormState {
  if (!setting) return { ...EMPTY_PROVIDER_FORM };
  return {
    base_url: setting.base_url,
    model: setting.model,
    reasoning_effort: setting.reasoning_effort ?? '',
    api_key: '',
  };
}

function formsFromSettings(
  settings: PersistentLLMProviderSettings,
): Record<LLMProviderPurpose, ProviderFormState> {
  return {
    statement: formFromSetting(settings.statement),
    verification: formFromSetting(settings.verification),
    review: formFromSetting(settings.review),
  };
}

function overridesFromSettings(
  settings: PersistentLLMProviderSettings,
): Record<LLMOverridePurpose, boolean> {
  return {
    verification: Boolean(settings.verification.override_configured),
    review: Boolean(settings.review.override_configured),
  };
}

function ValidationMessage({ message }: { message?: string }) {
  if (!message) return null;
  return (
    <span className="block text-xs text-danger-500 dark:text-danger-400">
      {message}
    </span>
  );
}

interface ProviderCardProps {
  purpose: LLMProviderPurpose;
  title: string;
  description: string;
  setting?: PersistentLLMProviderSetting;
  form: ProviderFormState;
  enabled: boolean;
  follows?: string;
  reusableKey: boolean;
  saving: boolean;
  onChange: (next: ProviderFormState) => void;
  onSave: () => void;
  onToggle?: (enabled: boolean) => void;
}

function ProviderCard({
  purpose,
  title,
  description,
  setting,
  form,
  enabled,
  follows,
  reusableKey,
  saving,
  onChange,
  onSave,
  onToggle,
}: ProviderCardProps) {
  const Icon =
    purpose === 'statement'
      ? KeyRound
      : purpose === 'verification'
        ? ShieldCheck
        : CheckCircle2;
  const errors = validateProviderForm(form, reusableKey);

  return (
    <section className="af-model-editor">
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 items-start gap-3">
          <div className="rounded-lg bg-anvil-50 p-2 dark:bg-anvil-800">
            <Icon className="h-5 w-5 text-[var(--da)]" />
          </div>
          <div>
            <h2 className="text-lg font-semibold text-anvil-900 dark:text-anvil-100">
              {title}
            </h2>
            <p className="mt-1 text-sm text-anvil-500 dark:text-anvil-400">
              {description}
            </p>
          </div>
        </div>

        {onToggle ? (
          <label className="flex cursor-pointer items-center gap-2 text-sm font-medium text-anvil-700 dark:text-anvil-300">
            <input
              type="checkbox"
              checked={enabled}
              disabled={saving}
              onChange={(event) => onToggle(event.target.checked)}
              className="h-4 w-4 rounded border-anvil-300"
            />
            独立配置
          </label>
        ) : (
          <span
            className={`rounded-full px-2.5 py-1 text-xs font-medium ${
              setting?.api_key_configured
                ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
                : 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300'
            }`}
          >
            {setting?.api_key_configured ? '密钥已配置' : '需要 API Key'}
          </span>
        )}
      </div>

      {!enabled ? (
        <div className="rounded-lg border border-anvil-200 bg-anvil-50 p-4 text-sm dark:border-anvil-700 dark:bg-anvil-800/50">
          <p className="font-medium text-anvil-800 dark:text-anvil-100">
            当前跟随 {follows}
          </p>
          <p className="mt-2 break-all text-anvil-500 dark:text-anvil-400">
            {setting?.base_url || '尚未加载'}
            {setting?.model ? ` · ${setting.model}` : ''}
          </p>
          <p className="mt-2 text-anvil-500 dark:text-anvil-400">
            推理强度：{setting?.reasoning_effort || '未指定（不发送）'}
          </p>
          <p className="mt-2 text-xs text-anvil-500 dark:text-anvil-400">
            API Key 由服务端按继承链解析，不会复制到浏览器。
          </p>
        </div>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4">
            <label className="space-y-1.5">
              <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
                Base URL
              </span>
              <input
                className="forge-input font-mono"
                value={form.base_url}
                onChange={(event) =>
                  onChange({ ...form, base_url: event.target.value })
                }
                placeholder="https://api.example.com"
                inputMode="url"
                autoComplete="off"
                aria-invalid={Boolean(errors.base_url)}
              />
              <ValidationMessage message={errors.base_url} />
            </label>

            <label className="space-y-1.5">
              <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
                模型
              </span>
              <input
                className="forge-input"
                value={form.model}
                onChange={(event) =>
                  onChange({ ...form, model: event.target.value })
                }
                placeholder="gpt-5.6"
                autoComplete="off"
                aria-invalid={Boolean(errors.model)}
              />
              <ValidationMessage message={errors.model} />
            </label>

            <label className="space-y-1.5">
              <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
                推理强度
              </span>
              <select
                className="forge-input"
                value={form.reasoning_effort}
                onChange={(event) =>
                  onChange({
                    ...form,
                    reasoning_effort: event.target.value as LLMReasoningEffort,
                  })
                }
              >
                {REASONING_EFFORT_OPTIONS.map((option) => (
                  <option key={option.value || 'unset'} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
              <span className="block text-xs text-anvil-500 dark:text-anvil-400">
                仅 OpenAI Responses/Chat 协议发送；其他协议不会静默改写。
              </span>
            </label>

            <label className="space-y-1.5">
              <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
                API Key
              </span>
              <input
                className="forge-input"
                type="password"
                value={form.api_key}
                onChange={(event) =>
                  onChange({ ...form, api_key: event.target.value })
                }
                placeholder={
                  reusableKey
                    ? '留空保留当前密钥'
                    : '输入新密钥；不会再次显示'
                }
                autoComplete="new-password"
                maxLength={4096}
                aria-invalid={Boolean(errors.api_key)}
              />
              <ValidationMessage message={errors.api_key} />
            </label>
          </div>

          <div className="mt-5 flex flex-wrap items-center justify-between gap-3 border-t border-anvil-100 pt-4 dark:border-anvil-700">
            <span className="text-xs text-anvil-500 dark:text-anvil-400">
              Provider 与协议由服务端按本次 Base URL 和模型自动解析。
            </span>
            <button
              type="button"
              className="forge-btn-primary"
              onClick={onSave}
              disabled={saving || hasErrors(errors)}
            >
              {saving ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Save className="h-4 w-4" />
              )}
              保存
            </button>
          </div>
        </>
      )}
    </section>
  );
}

export default function SettingsPage() {
  const [settings, setSettings] = useState<PersistentLLMProviderSettings>();
  const [section, setSection] = useState<LLMProviderPurpose | 'embedding'>('statement');
  const [forms, setForms] = useState<Record<LLMProviderPurpose, ProviderFormState>>({
    statement: { ...EMPTY_PROVIDER_FORM },
    verification: { ...EMPTY_PROVIDER_FORM },
    review: { ...EMPTY_PROVIDER_FORM },
  });
  const [overrideEnabled, setOverrideEnabled] = useState<
    Record<LLMOverridePurpose, boolean>
  >({ verification: false, review: false });
  const [embeddingForm, setEmbeddingForm] = useState<EmbeddingFormState>({
    ...EMPTY_EMBEDDING_FORM,
  });
  const [embeddingRuntime, setEmbeddingRuntime] =
    useState<EmbeddingRuntimeSettings>();
  const [embeddingTest, setEmbeddingTest] = useState<LocalEmbeddingTestResult>();
  const [savedEmbeddingIdentity, setSavedEmbeddingIdentity] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState<LLMProviderPurpose>();
  const [embeddingAction, setEmbeddingAction] = useState<'test' | 'save'>();
  const [error, setError] = useState<string>();
  const [embeddingError, setEmbeddingError] = useState<string>();
  const [success, setSuccess] = useState<string>();

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    setEmbeddingError(undefined);

    const [llmResult, embeddingResult] = await Promise.allSettled([
      getPermanentLLMSettings(),
      getEmbeddingRuntimeSettings(),
    ]);

    if (llmResult.status === 'fulfilled' && llmResult.value.data) {
      const data = llmResult.value.data;
      setSettings(data);
      setForms(formsFromSettings(data));
      setOverrideEnabled(overridesFromSettings(data));
    } else {
      const cause =
        llmResult.status === 'rejected'
          ? llmResult.reason
          : new Error('服务器没有返回模型配置');
      setError(safeErrorMessage(cause, '加载模型配置失败'));
    }

    if (embeddingResult.status === 'fulfilled' && embeddingResult.value.data) {
      const data = embeddingResult.value.data;
      setEmbeddingRuntime(data);
      setEmbeddingForm({
        base_url: data.base_url,
        model: data.model,
        api_key: '',
      });
      if (data.api_key_configured) {
        setSavedEmbeddingIdentity(endpointIdentity(data.base_url, data.model));
      }
    } else {
      const cause =
        embeddingResult.status === 'rejected'
          ? embeddingResult.reason
          : new Error('服务器没有返回 embedding 配置');
      setEmbeddingError(safeErrorMessage(cause, '加载 embedding 配置失败'));
    }

    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  function reusableProviderKey(purpose: LLMProviderPurpose): boolean {
    const setting = settings?.[purpose];
    if (!setting?.api_key_configured) return false;
    const ownsConfiguredKey =
      purpose === 'statement' || Boolean(setting.override_configured);
    return (
      ownsConfiguredKey &&
      normalizeURL(forms[purpose].base_url) === normalizeURL(setting.base_url)
    );
  }

  async function saveProvider(purpose: LLMProviderPurpose) {
    const form = forms[purpose];
    if (hasErrors(validateProviderForm(form, reusableProviderKey(purpose)))) {
      return;
    }
    setSaving(purpose);
    setError(undefined);
    setSuccess(undefined);
    try {
      const response = await updatePermanentLLMSetting(purpose, {
        base_url: form.base_url.trim(),
        model: form.model.trim(),
        reasoning_effort: form.reasoning_effort,
        api_key: form.api_key.trim() || undefined,
      });
      if (!response.data) throw new Error('服务器没有返回保存结果');
      try {
        const refreshed = await getPermanentLLMSettings();
        if (!refreshed.data) throw new Error('服务器没有返回刷新后的模型配置');
        setSettings(refreshed.data);
        setForms(formsFromSettings(refreshed.data));
        setOverrideEnabled(overridesFromSettings(refreshed.data));
      } catch (refreshCause) {
        setSettings((current) =>
          current ? { ...current, [purpose]: response.data! } : current,
        );
        setForms((current) => ({
          ...current,
          [purpose]: formFromSetting(response.data!),
        }));
        if (purpose !== 'statement') {
          setOverrideEnabled((current) => ({ ...current, [purpose]: true }));
        }
        setError(
          `配置已保存，但继承链刷新失败，请重新加载页面：${safeErrorMessage(
            refreshCause,
            '刷新失败',
          )}`,
        );
      }
      const labels: Record<LLMProviderPurpose, string> = {
        statement: '生成（G）',
        verification: '验算（V）',
        review: '评审（R）',
      };
      setSuccess(`${labels[purpose]}配置已保存。`);
    } catch (cause) {
      setError(
        safeErrorMessage(cause, '保存模型配置失败', [form.api_key]),
      );
    } finally {
      setSaving(undefined);
    }
  }

  async function changeOverride(
    purpose: LLMOverridePurpose,
    enabled: boolean,
  ) {
    setError(undefined);
    setSuccess(undefined);
    if (enabled) {
      setForms((current) => ({
        ...current,
        [purpose]: formFromSetting(settings?.[purpose]),
      }));
      setOverrideEnabled((current) => ({ ...current, [purpose]: true }));
      return;
    }

    setSaving(purpose);
    try {
      const response = await resetPermanentLLMSetting(purpose);
      if (!response.data) throw new Error('服务器没有返回重置结果');
      try {
        const refreshed = await getPermanentLLMSettings();
        if (!refreshed.data) throw new Error('服务器没有返回刷新后的模型配置');
        setSettings(refreshed.data);
        setForms(formsFromSettings(refreshed.data));
        setOverrideEnabled(overridesFromSettings(refreshed.data));
      } catch (refreshCause) {
        setSettings((current) =>
          current ? { ...current, [purpose]: response.data! } : current,
        );
        setForms((current) => ({
          ...current,
          [purpose]: formFromSetting(response.data!),
        }));
        setOverrideEnabled((current) => ({ ...current, [purpose]: false }));
        setError(
          `覆盖已清除，但继承链刷新失败，当前显示可能已过期，请重新加载页面：${safeErrorMessage(
            refreshCause,
            '刷新失败',
          )}`,
        );
        return;
      }
      setSuccess(
        purpose === 'verification'
          ? '验算（V）已恢复跟随生成（G）。'
          : '评审（R）已恢复跟随验算（V）。',
      );
    } catch (cause) {
      setError(safeErrorMessage(cause, '恢复跟随配置失败'));
    } finally {
      setSaving(undefined);
    }
  }

  function updateForm(purpose: LLMProviderPurpose, next: ProviderFormState) {
    setForms((current) => ({ ...current, [purpose]: next }));
  }

  function updateEmbeddingForm(next: EmbeddingFormState) {
    const changed =
      next.base_url !== embeddingForm.base_url ||
      next.model !== embeddingForm.model ||
      next.api_key !== embeddingForm.api_key;
    setEmbeddingForm(next);
    if (changed) setEmbeddingTest(undefined);
  }

  function embeddingPayload(): LocalEmbeddingEndpointConfig {
    return {
      base_url: embeddingForm.base_url.trim(),
      model: embeddingForm.model.trim(),
      api_key: embeddingForm.api_key.trim() || undefined,
    };
  }

  const currentEmbeddingIdentity = endpointIdentity(
    embeddingForm.base_url,
    embeddingForm.model,
  );
  const runtimeMatchesEmbedding =
    embeddingRuntime !== undefined &&
    endpointIdentity(embeddingRuntime.base_url, embeddingRuntime.model) ===
      currentEmbeddingIdentity;
  const testMatchesEmbedding =
    embeddingTest !== undefined &&
    endpointIdentity(embeddingTest.base_url, embeddingTest.model) ===
      currentEmbeddingIdentity;
  const embeddingKeyConfigured =
    savedEmbeddingIdentity === currentEmbeddingIdentity ||
    (runtimeMatchesEmbedding && Boolean(embeddingRuntime.api_key_configured));
  const embeddingErrors = validateEmbeddingForm(
    embeddingForm,
    embeddingKeyConfigured,
  );

  async function testEmbedding() {
    if (hasErrors(embeddingErrors)) return;
    setEmbeddingAction('test');
    setEmbeddingError(undefined);
    setSuccess(undefined);
    try {
      const response = await testLocalEmbeddingEndpoint(embeddingPayload());
      if (!response.data) throw new Error('服务器没有返回连接测试结果');
      setEmbeddingTest(response.data);
      setSuccess('Embedding 连接测试通过。');
    } catch (cause) {
      setEmbeddingError(
        safeErrorMessage(cause, 'Embedding 连接测试失败', [embeddingForm.api_key]),
      );
    } finally {
      setEmbeddingAction(undefined);
    }
  }

  async function saveEmbedding() {
    if (hasErrors(embeddingErrors)) return;
    setEmbeddingAction('save');
    setEmbeddingError(undefined);
    setSuccess(undefined);
    try {
      const response = await deployLocalEmbeddingModel({
        endpoint: embeddingPayload(),
        activate: false,
      });
      if (!response.data) throw new Error('服务器没有返回 embedding 保存结果');
      setEmbeddingTest(response.data.test);
      if (response.data.runtime_settings_saved) {
        setSavedEmbeddingIdentity(currentEmbeddingIdentity);
      }
      setEmbeddingForm((current) => ({ ...current, api_key: '' }));
      setSuccess(
        response.data.requires_runtime_restart
          ? 'Embedding 配置已保存；重启 runtime 后生效。'
          : 'Embedding 配置已保存。',
      );
    } catch (cause) {
      setEmbeddingError(
        safeErrorMessage(cause, '保存 embedding 配置失败', [embeddingForm.api_key]),
      );
    } finally {
      setEmbeddingAction(undefined);
    }
  }

  const sections = [
    { id: 'statement' as const, label: '生成模型', hint: settings?.statement.api_key_configured ? '已配置' : '开始配置', Icon: KeyRound },
    { id: 'verification' as const, label: '独立验算', hint: overrideEnabled.verification ? '独立模型' : '跟随生成', Icon: ShieldCheck },
    { id: 'review' as const, label: '质量评审', hint: overrideEnabled.review ? '独立模型' : '跟随验算', Icon: CheckCircle2 },
    { id: 'embedding' as const, label: '题库去重', hint: embeddingKeyConfigured ? '已配置' : '连接模型', Icon: Cpu },
  ];
  return <div className="af-page af-settings-page">
    <PageHeader eyebrow="工作区设置" title="模型设置" description="配置生成、验算与评审模型。只需服务地址、模型名称和密钥。" />
    <ApiHealthBanner />
    {error && <div role="alert" className="af-error-banner">{error}</div>}
    {success && <div role="status" className="af-success-banner"><CheckCircle2 size={16} />{success}</div>}
    <div className="af-settings-layout">
      <aside className="af-settings-navigation">
        <div role="tablist" aria-label="模型用途" aria-orientation="vertical" className="af-settings-tabs" onKeyDown={event => {
          if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
          event.preventDefault(); const index = sections.findIndex(item => item.id === section);
          const next = event.key === 'Home' ? 0 : event.key === 'End' ? sections.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + sections.length) % sections.length;
          setSection(sections[next].id); document.getElementById('model-tab-' + sections[next].id)?.focus();
        }}>
          {sections.map(({ id, label, hint, Icon }) => <button type="button" role="tab" id={'model-tab-' + id} key={id} aria-selected={section === id} tabIndex={section === id ? 0 : -1} aria-controls={'model-panel-' + id} onClick={() => setSection(id)}>
            <Icon size={18} /><span><strong>{label}</strong><small>{hint}</small></span>
          </button>)}
        </div>
        <div className="af-settings-note"><ShieldCheck size={18} /><p>密钥加密保存在服务端。留空保存时，会继续使用原密钥。</p></div>
      </aside>
      <div className="af-settings-content">
        {loading ? <div className="af-panel af-loading"><Loader2 size={22} className="animate-spin" />正在读取配置…</div> : <>
          <div className="af-panel af-settings-panel" role="tabpanel" id="model-panel-statement" aria-labelledby="model-tab-statement" hidden={section !== 'statement'} tabIndex={0}><ProviderCard
                purpose="statement"
                title="生成模型"
                description="用来构思题目、撰写题面，并生成解法和测试数据。"
                setting={settings?.statement}
                form={forms.statement}
                enabled
                reusableKey={reusableProviderKey('statement')}
                saving={saving === 'statement'}
                onChange={(next) => updateForm('statement', next)}
                onSave={() => void saveProvider('statement')}
              /></div>
          <div className="af-panel af-settings-panel" role="tabpanel" id="model-panel-verification" aria-labelledby="model-tab-verification" hidden={section !== 'verification'} tabIndex={0}><ProviderCard
                purpose="verification"
                title="独立验算"
                description="验算题面、解法与结果。"
                setting={settings?.verification}
                form={forms.verification}
                enabled={overrideEnabled.verification}
                follows="生成（G）"
                reusableKey={reusableProviderKey('verification')}
                saving={saving === 'verification'}
                onChange={(next) => updateForm('verification', next)}
                onSave={() => void saveProvider('verification')}
                onToggle={(enabled) =>
                  void changeOverride('verification', enabled)
                }
              /></div>
          <div className="af-panel af-settings-panel" role="tabpanel" id="model-panel-review" aria-labelledby="model-tab-review" hidden={section !== 'review'} tabIndex={0}><ProviderCard
                purpose="review"
                title="质量评审"
                description="质量评审。"
                setting={settings?.review}
                form={forms.review}
                enabled={overrideEnabled.review}
                follows="验算（V）"
                reusableKey={reusableProviderKey('review')}
                saving={saving === 'review'}
                onChange={(next) => updateForm('review', next)}
                onSave={() => void saveProvider('review')}
                onToggle={(enabled) => void changeOverride('review', enabled)}
              /></div>
          <div className="af-panel af-settings-panel" role="tabpanel" id="model-panel-embedding" aria-labelledby="model-tab-embedding" hidden={section !== 'embedding'} tabIndex={0}><section className="af-model-editor">
            <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
              <div className="flex min-w-0 items-start gap-3">
                <div className="rounded-lg bg-anvil-50 p-2 dark:bg-anvil-800">
                  <Cpu className="h-5 w-5 text-sky-600 dark:text-sky-400" />
                </div>
                <div>
                  <h2 className="text-lg font-semibold text-anvil-900 dark:text-anvil-100">
                    题库去重模型
                  </h2>
                  <p className="mt-1 text-sm text-anvil-500 dark:text-anvil-400">
                    用于题目近邻去重；Provider 与向量维度自动验证。
                  </p>
                </div>
              </div>
              <span
                className={`rounded-full px-2.5 py-1 text-xs font-medium ${
                  embeddingKeyConfigured
                    ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
                    : 'bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300'
                }`}
              >
                {embeddingKeyConfigured ? '密钥已配置' : '未配置持久密钥'}
              </span>
            </div>

            {embeddingError && (
              <div className="mb-4 rounded-lg border border-danger-400 bg-danger-50 p-4 text-sm text-danger-600 dark:border-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
                {embeddingError}
              </div>
            )}

            <div className="grid grid-cols-1 gap-4">
              <label className="space-y-1.5">
                <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
                  Base URL
                </span>
                <input
                  className="forge-input font-mono"
                  value={embeddingForm.base_url}
                  onChange={(event) =>
                    updateEmbeddingForm({
                      ...embeddingForm,
                      base_url: event.target.value,
                    })
                  }
                  placeholder="http://127.0.0.1:8000/v1"
                  inputMode="url"
                  autoComplete="off"
                  aria-invalid={Boolean(embeddingErrors.base_url)}
                />
                <ValidationMessage message={embeddingErrors.base_url} />
              </label>

              <label className="space-y-1.5">
                <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
                  模型
                </span>
                <input
                  className="forge-input"
                  value={embeddingForm.model}
                  onChange={(event) =>
                    updateEmbeddingForm({
                      ...embeddingForm,
                      model: event.target.value,
                    })
                  }
                  placeholder="text-embedding-3-large"
                  autoComplete="off"
                  aria-invalid={Boolean(embeddingErrors.model)}
                />
                <ValidationMessage message={embeddingErrors.model} />
              </label>

              <label className="space-y-1.5">
                <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
                  API Key
                </span>
                <input
                  className="forge-input"
                  type="password"
                  value={embeddingForm.api_key}
                  onChange={(event) =>
                    updateEmbeddingForm({
                      ...embeddingForm,
                      api_key: event.target.value,
                    })
                  }
                  placeholder={
                    embeddingKeyConfigured
                      ? '留空使用已保存密钥'
                      : endpointCanOmitAPIKey(embeddingForm.base_url)
                        ? '本地免鉴权端点可留空'
                        : '输入 API Key；不会再次显示'
                  }
                  autoComplete="new-password"
                  maxLength={4096}
                  aria-invalid={Boolean(embeddingErrors.api_key)}
                />
                <ValidationMessage message={embeddingErrors.api_key} />
              </label>
            </div>

            <p className="mt-4 text-xs text-anvil-500 dark:text-anvil-400">
              {testMatchesEmbedding && embeddingTest
                ? `已验证 ${embeddingTest.provider_id} · ${embeddingTest.dimensions} 维 · ${embeddingTest.latency_ms} ms`
                : runtimeMatchesEmbedding && embeddingRuntime
                  ? `当前运行时 ${embeddingRuntime.provider_id} · ${embeddingRuntime.dimensions} 维`
                  : '连接测试会自动验证 Provider 与向量维度。'}
            </p>

            <div className="mt-5 flex flex-wrap items-center justify-between gap-3 border-t border-anvil-100 pt-4 dark:border-anvil-700">
              <Link href="/embedding" className="forge-btn-secondary">
                <ExternalLink className="h-4 w-4" />
                高级设置
              </Link>
              <div className="flex flex-wrap gap-3">
                <button
                  type="button"
                  className="forge-btn-secondary"
                  onClick={() => void testEmbedding()}
                  disabled={
                    Boolean(embeddingAction) || hasErrors(embeddingErrors)
                  }
                >
                  {embeddingAction === 'test' ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Plug className="h-4 w-4" />
                  )}
                  测试连接
                </button>
                <button
                  type="button"
                  className="forge-btn-primary"
                  onClick={() => void saveEmbedding()}
                  disabled={
                    Boolean(embeddingAction) || hasErrors(embeddingErrors)
                  }
                >
                  {embeddingAction === 'save' ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Save className="h-4 w-4" />
                  )}
                  保存
                </button>
              </div>
            </div>
          </section></div>
        </>}
        <div className="af-settings-explanation"><h2>模型如何协作</h2><div><span>构思与生成</span><span aria-hidden="true">→</span><span>独立验算</span><span aria-hidden="true">→</span><span>质量评审</span></div><p>验算默认跟随生成，评审默认跟随验算；开启独立配置后，各自使用所选模型。题库去重单独连接向量模型。</p></div>
      </div>
    </div>
  </div>;
}
