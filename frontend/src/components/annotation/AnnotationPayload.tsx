import MarkdownRenderer from '@/components/MarkdownRenderer';
import { cn } from '@/lib/utils';

type SafePayload =
  | string
  | number
  | boolean
  | SafePayload[]
  | { [key: string]: SafePayload };

const FORBIDDEN_KEY_PARTS = [
  'provider',
  'model',
  'source',
  'sourceuri',
  'sourcerevision',
  'creator',
  'ancestry',
  'score',
  'systemscore',
  'peerlabel',
];

const FIELD_LABELS: Record<string, string> = {
  title: '标题',
  prompt: '题目',
  question: '问题',
  statement: '题面',
  content: '内容',
  markdown: '内容',
  instructions: '说明',
  description: '说明',
  candidate: '候选内容',
  candidate_a: '候选 A',
  candidate_b: '候选 B',
  left: '内容 A',
  right: '内容 B',
  item_a: '内容 A',
  item_b: '内容 B',
  reference: '参考内容',
  answer: '答案',
  explanation: '解析',
  solution: '解法',
  code: '代码',
  input: '输入',
  output: '输出',
  tags: '标签',
  proposed_tags: '待核标签',
  claims: '待核条款',
};

function normalizeKey(key: string): string {
  return key.toLowerCase().replace(/[^a-z0-9]/g, '');
}

function isForbiddenKey(key: string): boolean {
  const normalized = normalizeKey(key);
  return FORBIDDEN_KEY_PARTS.some((part) => normalized.includes(part));
}

export function sanitizeAnnotationPayload(
  value: unknown,
  depth = 0,
): SafePayload | undefined {
  if (depth > 10 || value === null || value === undefined) return undefined;

  if (typeof value === 'string') {
    return value.trim() ? value : undefined;
  }
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : undefined;
  }
  if (typeof value === 'boolean') return value;

  if (Array.isArray(value)) {
    const entries = value
      .map((entry) => sanitizeAnnotationPayload(entry, depth + 1))
      .filter((entry): entry is SafePayload => entry !== undefined);
    return entries.length > 0 ? entries : undefined;
  }

  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([key]) => !isForbiddenKey(key))
      .flatMap(([key, entry]) => {
        const sanitized = sanitizeAnnotationPayload(entry, depth + 1);
        return sanitized === undefined ? [] : [[key, sanitized] as const];
      });

    return entries.length > 0 ? Object.fromEntries(entries) : undefined;
  }

  return undefined;
}

function fieldLabel(key: string): string {
  const normalized = key.toLowerCase();
  if (FIELD_LABELS[normalized]) return FIELD_LABELS[normalized];

  return key
    .replace(/[_-]+/g, ' ')
    .replace(/\b[a-z]/g, (letter) => letter.toUpperCase());
}

function isRecord(value: SafePayload): value is Record<string, SafePayload> {
  return typeof value === 'object' && !Array.isArray(value);
}

function PrimitiveValue({ value }: { value: string | number | boolean }) {
  if (typeof value === 'string') {
    return <MarkdownRenderer content={value} />;
  }
  if (typeof value === 'boolean') {
    return (
      <span className="text-sm font-medium text-anvil-800">
        {value ? '是' : '否'}
      </span>
    );
  }
  return <span className="font-mono text-sm text-anvil-800">{value}</span>;
}

function PayloadValue({
  value,
  depth,
}: {
  value: SafePayload;
  depth: number;
}) {
  if (
    typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'boolean'
  ) {
    return <PrimitiveValue value={value} />;
  }

  if (Array.isArray(value)) {
    const primitives = value.every((entry) => !isRecord(entry) && !Array.isArray(entry));
    if (primitives) {
      return (
        <ul className="space-y-2">
          {value.map((entry, index) => (
            <li key={index} className="flex gap-3 text-anvil-800">
              <span aria-hidden="true" className="mt-2 h-1.5 w-1.5 flex-none rounded-full bg-anvil-400" />
              <div className="min-w-0 flex-1">
                <PayloadValue value={entry} depth={depth + 1} />
              </div>
            </li>
          ))}
        </ul>
      );
    }

    return (
      <div className="divide-y divide-anvil-200">
        {value.map((entry, index) => (
          <div key={index} className="py-4 first:pt-0 last:pb-0">
            <PayloadValue value={entry} depth={depth + 1} />
          </div>
        ))}
      </div>
    );
  }

  const entries = Object.entries(value);
  const comparison =
    depth === 0 &&
    entries.length === 2 &&
    entries.every(([key]) =>
      /^(candidate|item|content)?_?[ab]$|^(left|right)$/.test(key.toLowerCase()),
    );

  return (
    <div
      className={cn(
        'grid gap-5',
        comparison && 'lg:grid-cols-2 lg:gap-0 lg:divide-x lg:divide-anvil-200',
      )}
    >
      {entries.map(([key, entry]) => (
        <section
          key={key}
          className={cn(
            'min-w-0',
            comparison && 'lg:px-6 lg:first:pl-0 lg:last:pr-0',
            !comparison && depth > 0 && 'border-l-2 border-anvil-200 pl-4',
          )}
        >
          <h2 className="mb-2 text-xs font-semibold uppercase text-anvil-500">
            {fieldLabel(key)}
          </h2>
          <PayloadValue value={entry} depth={depth + 1} />
        </section>
      ))}
    </div>
  );
}

export default function AnnotationPayload({ payload }: { payload: unknown }) {
  const safePayload = sanitizeAnnotationPayload(payload);
  if (safePayload === undefined) return null;

  return (
    <div className="annotation-payload min-w-0">
      <PayloadValue value={safePayload} depth={0} />
    </div>
  );
}
