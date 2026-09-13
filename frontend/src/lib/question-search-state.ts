import type { QuestionSearchFilter, QuestionSearchItem } from './types';

function integer(value: string | null, min: number, max: number): number | undefined {
  if (!value || !/^\d+$/.test(value)) return undefined;
  const number = Number(value);
  return Number.isSafeInteger(number) && number >= min && number <= max ? number : undefined;
}

export function parseQuestionSearch(params: URLSearchParams): QuestionSearchFilter {
  const type = params.get('type');
  const difficulty = params.get('quiz_difficulty');
  const quizDifficulty = difficulty === 'easy' || difficulty === 'medium' || difficulty === 'hard'
    ? difficulty : undefined;
  return {
    q: params.get('q')?.trim() || undefined,
    type: type === 'programming' || type === 'choice' || type === 'fill_blank' || type === 'judge' ? type : undefined,
    tag: params.get('tag')?.trim() || undefined,
    knowledge_point: params.get('knowledge_point')?.trim() || undefined,
    min_difficulty: quizDifficulty ? undefined : integer(params.get('min_difficulty'), 800, 3500),
    max_difficulty: quizDifficulty ? undefined : integer(params.get('max_difficulty'), 800, 3500),
    quiz_difficulty: quizDifficulty,
    page: integer(params.get('page'), 1, 1000000) ?? 1,
    size: integer(params.get('size'), 1, 100) ?? 20,
  };
}

export function questionResultsHref(filter: QuestionSearchFilter): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filter)) {
    if (value !== undefined && value !== '' && !(key === 'page' && value === 1)) {
      params.set(key, String(value));
    }
  }
  const query = params.toString();
  return query ? '/search?' + query : '/search';
}

export function questionDetailHref(item: Pick<QuestionSearchItem, 'source' | 'id'>): string {
  return '/' + (item.source === 'quiz' ? 'quizzes' : 'problems') + '/' + encodeURIComponent(item.id);
}
