-- Add difficulty range columns to tag_categories for difficulty-aware filtering
-- This enforces that tags are only available for problems within their difficulty range

ALTER TABLE tag_categories ADD COLUMN IF NOT EXISTS min_difficulty INT NOT NULL DEFAULT 800;
ALTER TABLE tag_categories ADD COLUMN IF NOT EXISTS max_difficulty INT NOT NULL DEFAULT 3500;

-- ============================================================
-- Syntax level tags: all 800-1200
-- ============================================================

UPDATE tag_categories SET min_difficulty = 800, max_difficulty = 1200 WHERE level = 'syntax';

-- ============================================================
-- Algorithm level tags: difficulty ranges from seed comments
-- ============================================================

-- 基础算法 (800-1400)
UPDATE tag_categories SET min_difficulty = 800, max_difficulty = 1400 WHERE tag_name IN (
  'sorting', 'binary-search', 'greedy', 'simulation', 'brute-force',
  'prefix-sum', 'two-pointers', 'constructive', 'implementation',
  'stl-usage', 'recursion'
);

-- 数学 (900-3000)
UPDATE tag_categories SET min_difficulty = 900, max_difficulty = 3000 WHERE tag_name IN (
  'math', 'number-theory', 'combinatorics', 'probability'
);
-- 高级数学 (1600-3500)
UPDATE tag_categories SET min_difficulty = 1600, max_difficulty = 3500 WHERE tag_name IN (
  'linear-algebra', 'fft-ntt', 'polynomial', 'generating-function'
);

-- 动态规划-基础 (1000-2200)
UPDATE tag_categories SET min_difficulty = 1000, max_difficulty = 2200 WHERE tag_name = 'dp';
-- 背包 (1000-2000)
UPDATE tag_categories SET min_difficulty = 1000, max_difficulty = 2000 WHERE tag_name = 'dp-knapsack';
-- 树形DP (1200-2800)
UPDATE tag_categories SET min_difficulty = 1200, max_difficulty = 2800 WHERE tag_name = 'dp-tree';
-- 高级DP (1600-3500)
UPDATE tag_categories SET min_difficulty = 1600, max_difficulty = 3500 WHERE tag_name IN (
  'dp-digit', 'dp-bitmask', 'dp-probability', 'dp-game',
  'dp-optimization', 'dp-on-dag', 'dp-plug', 'dp-broken-profile', 'dp-sos'
);

-- 图论: 基础 (1200-2200)
UPDATE tag_categories SET min_difficulty = 1200, max_difficulty = 2200 WHERE tag_name IN (
  'graph-bfs-dfs', 'shortest-path', 'mst', 'topological-sort'
);
-- 图论: 高级 (1600-3500)
UPDATE tag_categories SET min_difficulty = 1600, max_difficulty = 3500 WHERE tag_name IN (
  'network-flow', 'mcmf', 'bipartite', 'tarjan', 'euler-path',
  '2-sat', 'virtual-tree', 'dominator-tree', 'graph-coloring'
);

-- 树: 基础 (1200-2400)
UPDATE tag_categories SET min_difficulty = 1200, max_difficulty = 2400 WHERE tag_name IN (
  'tree-basic', 'lca', 'euler-tour', 'tree-diff'
);
-- 树: 高级 (1600-3200)
UPDATE tag_categories SET min_difficulty = 1600, max_difficulty = 3200 WHERE tag_name IN (
  'hld', 'centroid', 'dsu-on-tree'
);

-- 数据结构: 基础 (1200-2400)
UPDATE tag_categories SET min_difficulty = 1200, max_difficulty = 2400 WHERE tag_name IN (
  'stack-queue', 'segment-tree', 'bit', 'sparse-table', 'dsu'
);
-- 数据结构: 高级 (1600-3500)
UPDATE tag_categories SET min_difficulty = 1600, max_difficulty = 3500 WHERE tag_name IN (
  'segtree-beats', 'block', 'balanced-bst', 'persistent',
  'cdq', 'li-chao', 'link-cut-tree', 'kd-tree'
);

-- 字符串: 基础 (1400-2600)
UPDATE tag_categories SET min_difficulty = 1400, max_difficulty = 2600 WHERE tag_name IN (
  'kmp', 'hashing', 'trie', 'manacher', 'z-function'
);
-- 字符串: 高级 (1800-3500)
UPDATE tag_categories SET min_difficulty = 1800, max_difficulty = 3500 WHERE tag_name IN (
  'ac-automaton', 'suffix-array', 'sam', 'pam'
);

-- 几何 (1600-3500)
UPDATE tag_categories SET min_difficulty = 1600, max_difficulty = 3500 WHERE tag_name IN (
  'geometry-basic', 'convex-hull', 'half-plane',
  'rotating-calipers', 'delaunay', 'minkowski-sum'
);

-- 高级算法 (1200-3500, with subcategory ranges)
UPDATE tag_categories SET min_difficulty = 1200, max_difficulty = 2800 WHERE tag_name IN (
  'game-theory', 'bitwise', 'meet-in-middle', 'offline', 'online'
);
UPDATE tag_categories SET min_difficulty = 1400, max_difficulty = 3200 WHERE tag_name IN (
  'interactive', 'randomized', 'mo-algo'
);
UPDATE tag_categories SET min_difficulty = 1800, max_difficulty = 3500 WHERE tag_name IN (
  'parallel-bs', 'matroid'
);

-- Create index for efficient difficulty-range queries
CREATE INDEX IF NOT EXISTS idx_tag_categories_difficulty_range ON tag_categories (level, min_difficulty, max_difficulty);
