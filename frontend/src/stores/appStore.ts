// ============================================================================
// Qraft - Zustand Global App Store
// Slices: sidebar, notifications, tags, theme, testDataConfig, selectedLevel
// ============================================================================

import { create } from 'zustand';
import { listTags as apiListTags } from '../lib/api';
import { generateId } from '../lib/utils';
import type {
  Notification,
  NotificationType,
  ProblemLevel,
  QuizFilter,
  QuizSubject,
  TagCategory,
  TestDataConfig,
} from '../lib/types';

// ---------------------------------------------------------------------------
// Default test data config
// ---------------------------------------------------------------------------

const DEFAULT_TEST_DATA_CONFIG: TestDataConfig = {
  groups: [],
  custom_cases: [],
  adaptive_count: true,
  min_count: 10,
  max_count: 20,
  // Zero is the wire-level marker for model-selected adaptive sizing.
  total_count: 0,
  sample_count: 2,
  time_limit: 2000,
  memory_limit: 256,
  checker_type: 'exact',
};

// ---------------------------------------------------------------------------
// Slice: Sidebar
// ---------------------------------------------------------------------------

interface SidebarSlice {
  collapsed: boolean;
  toggle: () => void;
}

// ---------------------------------------------------------------------------
// Slice: Notifications
// ---------------------------------------------------------------------------

interface NotificationsSlice {
  items: Notification[];
  addNotification: (
    type: NotificationType,
    title: string,
    message?: string,
    duration?: number,
  ) => string;
  removeNotification: (id: string) => void;
  clearAll: () => void;
}

// ---------------------------------------------------------------------------
// Slice: Tags
// ---------------------------------------------------------------------------

interface TagsSlice {
  tagsByLevel: Record<ProblemLevel, TagCategory[]>;
  loading: boolean;
  fetchTags: () => Promise<void>;
}

// ---------------------------------------------------------------------------
// Slice: Theme
// ---------------------------------------------------------------------------

interface ThemeSlice {
  darkMode: boolean;
  toggleDarkMode: () => void;
}

// ---------------------------------------------------------------------------
// Slice: Selected Level
// ---------------------------------------------------------------------------

interface LevelSlice {
  selectedLevel: ProblemLevel;
  setLevel: (level: ProblemLevel) => void;
  selectedSubject: QuizSubject;
  setSubject: (s: QuizSubject) => void;
  quizListFilter: QuizFilter;
  setQuizListFilter: (filter: QuizFilter) => void;
}

// ---------------------------------------------------------------------------
// Slice: Test Data Config
// ---------------------------------------------------------------------------

interface TestDataConfigSlice {
  testDataConfig: TestDataConfig;
  updateTestDataConfig: (partial: Partial<TestDataConfig>) => void;
  resetTestDataConfig: () => void;
}

// ---------------------------------------------------------------------------
// Combined store type
// ---------------------------------------------------------------------------

type AppStore = SidebarSlice &
  NotificationsSlice &
  TagsSlice &
  ThemeSlice &
  LevelSlice &
  TestDataConfigSlice;

// ---------------------------------------------------------------------------
// Read persisted theme preference
// ---------------------------------------------------------------------------

function getInitialDarkMode(): boolean {
  if (typeof window === 'undefined') return false;
  const stored = localStorage.getItem('algoforge_dark_mode');
  if (stored !== null) return stored === 'true';
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function getInitialSubject(): QuizSubject {
  if (typeof window === 'undefined') return 'c_language';
  const stored = localStorage.getItem('algoforge_subject');
  if (stored === 'c_language' || stored === 'data_structure_algorithm') {
    return stored;
  }
  return 'c_language';
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

export const useAppStore = create<AppStore>((set, get) => ({
  // =========================================================================
  // Sidebar
  // =========================================================================

  collapsed: false,

  toggle: () =>
    set((state) => ({ collapsed: !state.collapsed })),

  // =========================================================================
  // Notifications
  // =========================================================================

  items: [],

  addNotification: (
    type: NotificationType,
    title: string,
    message?: string,
    duration?: number,
  ): string => {
    const id = generateId();
    const effectiveDuration = duration ?? 5000;

    const notification: Notification = {
      id,
      type,
      title,
      message,
      duration: effectiveDuration,
      timestamp: Date.now(),
    };

    set((state) => ({
      items: [...state.items, notification],
    }));

    // Auto-dismiss after duration (0 = persistent)
    if (effectiveDuration > 0) {
      setTimeout(() => {
        get().removeNotification(id);
      }, effectiveDuration);
    }

    return id;
  },

  removeNotification: (id: string) =>
    set((state) => ({
      items: state.items.filter((n) => n.id !== id),
    })),

  clearAll: () => set({ items: [] }),

  // =========================================================================
  // Tags
  // =========================================================================

  tagsByLevel: {
    syntax: [],
    algorithm: [],
    gplt_l1: [],
    gplt_l2: [],
    gplt_l3: [],
  },

  loading: false,

  fetchTags: async () => {
    // Skip if already loaded
    const current = get().tagsByLevel;
    if (current.syntax.length > 0 || current.algorithm.length > 0) return;

    set({ loading: true });

    try {
      const res = await apiListTags();
      const allCategories = res.data ?? [];

      const syntax = allCategories.filter((c) => c.level === 'syntax');
      const algorithm = allCategories.filter((c) => c.level === 'algorithm');
      const gplt_l1 = allCategories.filter((c) => c.level === 'gplt_l1');
      const gplt_l2 = allCategories.filter((c) => c.level === 'gplt_l2');
      const gplt_l3 = allCategories.filter((c) => c.level === 'gplt_l3');

      set({
        tagsByLevel: { syntax, algorithm, gplt_l1, gplt_l2, gplt_l3 },
        loading: false,
      });
    } catch {
      set({ loading: false });
    }
  },

  // =========================================================================
  // Theme
  // =========================================================================

  darkMode: getInitialDarkMode(),

  toggleDarkMode: () =>
    set((state) => {
      const next = !state.darkMode;

      // Persist preference
      if (typeof window !== 'undefined') {
        localStorage.setItem('algoforge_dark_mode', String(next));

        // Toggle the class on <html> for Tailwind dark mode
        if (next) {
          document.documentElement.classList.add('dark');
        } else {
          document.documentElement.classList.remove('dark');
        }
      }

      return { darkMode: next };
    }),

  // =========================================================================
  // Selected Level
  // =========================================================================

  selectedLevel: 'syntax' as ProblemLevel,

  setLevel: (level: ProblemLevel) => set({ selectedLevel: level }),

  selectedSubject: getInitialSubject(),

  setSubject: (s: QuizSubject) => {
    if (typeof window !== 'undefined') {
      localStorage.setItem('algoforge_subject', s);
    }
    set({ selectedSubject: s });
  },

  quizListFilter: {},

  setQuizListFilter: (filter: QuizFilter) => set({ quizListFilter: filter }),

  // =========================================================================
  // Test Data Config
  // =========================================================================

  testDataConfig: { ...DEFAULT_TEST_DATA_CONFIG },

  updateTestDataConfig: (partial: Partial<TestDataConfig>) =>
    set((state) => ({
      testDataConfig: { ...state.testDataConfig, ...partial },
    })),

  resetTestDataConfig: () =>
    set({ testDataConfig: { ...DEFAULT_TEST_DATA_CONFIG } }),
}));
