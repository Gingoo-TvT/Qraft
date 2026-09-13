// Question search belongs to content workflows, not configuration or utility pages.
const CONTENT_PATHS = new Set([
 '/', '/search', '/problems', '/problems/new', '/quizzes', '/quizzes/new',
 '/problem-sets', '/problem-sets/new', '/problem-sets/assemble',
]);
const RESERVED_SEGMENTS: Record<string, Set<string>> = {
 problems: new Set(['new', 'import', 'hydro', 'gplt', 'quarantine']),
 quizzes: new Set(['new', 'import']),
 'problem-sets': new Set(['new', 'assemble']),
};

export function hasQuestionSearch(pathname: string): boolean {
 const path = pathname.replace(/\/+$/, '') || '/';
 if (CONTENT_PATHS.has(path)) return true;
 const detail = /^\/(problems|quizzes|problem-sets)\/([^/]+)(\/edit)?$/.exec(path);
 if (!detail || RESERVED_SEGMENTS[detail[1]].has(detail[2])) return false;
 return !detail[3] || detail[1] === 'problems';
}

export function questionSearchHref(pathname: string, currentQuery: string, text: string): string {
 const params = new URLSearchParams(pathname === '/search' ? currentQuery : '');
 const query = text.trim();
 if (query) params.set('q', query);
 else params.delete('q');
 params.delete('page');
 return '/search' + (params.size ? '?' + params.toString() : '');
}
