import { lazy, type ComponentType } from 'react';

export const pages: Record<string, ComponentType> = {
 '/problems': lazy(() => import('@/app/problems/page')),
 '/problems/new': lazy(() => import('@/app/problems/new/page')),
 '/problems/quarantine': lazy(() => import('@/app/problems/quarantine/page')),
 '/problems/gplt': lazy(() => import('@/app/problems/gplt/page')),
 '/problems/hydro/import': lazy(() => import('@/app/problems/hydro/import/page')),
 '/quizzes': lazy(() => import('@/app/quizzes/page')),
 '/quizzes/new': lazy(() => import('@/app/quizzes/new/page')),
 '/quizzes/import': lazy(() => import('@/app/quizzes/import/page')),
 '/problem-sets': lazy(() => import('@/app/problem-sets/page')),
 '/problem-sets/new': lazy(() => import('@/app/problem-sets/new/page')),
 '/problem-sets/assemble': lazy(() => import('@/app/problem-sets/assemble/page')),
 '/workflows': lazy(() => import('@/app/workflows/page')),
 '/settings': lazy(() => import('@/app/settings/page')),
 '/embedding': lazy(() => import('@/app/embedding/page')),
 '/integration': lazy(() => import('@/app/integration/page')),
 '/knowledge-points': lazy(() => import('@/app/knowledge-points/page')),
 '/testdata-config': lazy(() => import('@/app/testdata-config/page')),
};
const problem = lazy(() => import('@/app/problems/[id]/page'));
const edit = lazy(() => import('@/app/problems/[id]/edit/page'));
const quiz = lazy(() => import('@/app/quizzes/[id]/page'));
const set = lazy(() => import('@/app/problem-sets/[id]/page'));
const workflow = lazy(() => import('@/app/workflows/[id]/page'));
export function routePage(path: string): ComponentType | null {
 if (pages[path]) return pages[path];
 if (/^\/problems\/[^/]+\/edit$/.test(path)) return edit;
 if (/^\/problems\/[^/]+$/.test(path)) return problem;
 if (/^\/quizzes\/[^/]+$/.test(path)) return quiz;
 if (/^\/problem-sets\/[^/]+$/.test(path)) return set;
 if (/^\/workflows\/[^/]+$/.test(path)) return workflow;
 return null;
}
