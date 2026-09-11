-- Seed tag categories with all predefined tags
-- Syntax level: 10 tags
-- Algorithm level: 75 tags

-- ============================================================
-- 语法层级 (syntax) — 纯编程语法基础，共 10 个
-- ============================================================

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('syntax', 'variable',         '变量与数据类型',   '变量声明、类型转换、常量',                             1),
('syntax', 'io',               '输入输出',         'stdin/stdout、格式化输入输出',                        2),
('syntax', 'condition',        '条件判断',         'if-else、switch、三元运算符',                        3),
('syntax', 'loop',             '循环结构',         'for、while、do-while、循环嵌套、break/continue',     4),
('syntax', 'array-basic',      '数组基础',         '一维/二维数组的声明、遍历、简单操作',                    5),
('syntax', 'string-basic',     '字符串基础',       '字符串拼接、截取、遍历、ASCII操作',                     6),
('syntax', 'function-basic',   '函数基础',         '函数定义、参数传递、返回值、作用域',                     7),
('syntax', 'struct-basic',     '结构体基础',       '结构体/类的定义和使用',                               8),
('syntax', 'ptr-ref',          '指针与引用',       '指针基本操作、引用传递',                               9),
('syntax', 'simulation-basic', '简单模拟',         '按题意逐步模拟的纯实现题',                            10);

-- ============================================================
-- 算法层级 (algorithm) — 完整算法知识体系
-- ============================================================

-- ------------------------------------------------------------
-- 基础算法 (800-1400)
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'sorting',        '排序',             '各类排序算法及应用',                                 101),
('algorithm', 'binary-search',  '二分查找/二分答案', '二分搜索、二分答案',                                  102),
('algorithm', 'greedy',         '贪心',             '贪心策略',                                          103),
('algorithm', 'simulation',     '模拟',             '复杂模拟题',                                        104),
('algorithm', 'brute-force',    '暴力枚举',         '全搜索、枚举',                                      105),
('algorithm', 'prefix-sum',     '前缀和/差分',      '一维/二维前缀和、差分数组',                            106),
('algorithm', 'two-pointers',   '双指针/尺取法',    '对撞指针、滑动窗口',                                  107),
('algorithm', 'constructive',   '构造',             '构造算法',                                          108),
('algorithm', 'implementation', '实现',             '代码量大但算法简单的实现题',                           109),
('algorithm', 'stl-usage',      'STL/标准库应用',   'sort、map、set、priority_queue 等 STL 容器与算法的灵活运用', 110),
('algorithm', 'recursion',      '递归',             '递归思想、记忆化',                                   111);

-- ------------------------------------------------------------
-- 数学 (900-3000)
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'math',                '数学',       '基础数学推导',                                       201),
('algorithm', 'number-theory',       '数论',       '素数筛、GCD/LCM、模运算、欧拉函数、CRT、Lucas',       202),
('algorithm', 'combinatorics',       '组合数学',    '排列组合、容斥原理、Catalan数、Burnside',              203),
('algorithm', 'probability',         '概率/期望',   '概率DP、期望计算',                                   204),
('algorithm', 'linear-algebra',      '线性代数',    '矩阵快速幂、高斯消元、行列式',                         205),
('algorithm', 'fft-ntt',             'FFT/NTT',    '快速傅里叶变换、数论变换',                             206),
('algorithm', 'polynomial',          '多项式',      '多项式乘法/除法/求逆/多点求值',                        207),
('algorithm', 'generating-function', '生成函数',    'OGF/EGF',                                          208);

-- ------------------------------------------------------------
-- 动态规划 (1000-3500)
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'dp',                '动态规划-基础', '线性DP、区间DP',                                     301),
('algorithm', 'dp-knapsack',       '背包问题',      '01背包、完全背包、多重背包、分组背包',                   302),
('algorithm', 'dp-tree',           '树形DP',        '树上动态规划',                                       303),
('algorithm', 'dp-digit',          '数位DP',        '数位统计',                                          304),
('algorithm', 'dp-bitmask',        '状压DP',        '状态压缩动态规划',                                   305),
('algorithm', 'dp-probability',    '概率DP',        '概率/期望DP',                                       306),
('algorithm', 'dp-game',           '博弈DP',        'SG函数、博弈论DP',                                   307),
('algorithm', 'dp-optimization',   'DP优化',        '斜率优化、四边形不等式、WQS二分、分治优化',              308),
('algorithm', 'dp-on-dag',         'DAG上DP',       '有向无环图上DP',                                    309),
('algorithm', 'dp-plug',           '插头DP',        '轮廓线/插头DP',                                     310),
('algorithm', 'dp-broken-profile', '轮廓线DP',      '逐格转移',                                          311),
('algorithm', 'dp-sos',            '子集和DP(SOS)', 'Sum over Subsets DP',                               312);

-- ------------------------------------------------------------
-- 图论 (1200-3500)
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'graph-bfs-dfs',    '图搜索',          'BFS、DFS、连通性',                                  401),
('algorithm', 'shortest-path',    '最短路',          'Dijkstra、Bellman-Ford、Floyd、SPFA、Johnson',       402),
('algorithm', 'mst',              '最小生成树',       'Kruskal、Prim、Boruvka',                            403),
('algorithm', 'topological-sort', '拓扑排序',        'DAG排序',                                           404),
('algorithm', 'network-flow',     '网络流',          '最大流(Dinic)、最小割',                               405),
('algorithm', 'mcmf',             '费用流',          '最小费用最大流',                                     406),
('algorithm', 'bipartite',        '二分图',          '匹配(匈牙利/HK)、覆盖、独立集',                       407),
('algorithm', 'tarjan',           '强连通/割点/桥',   'Tarjan算法族、缩点',                                 408),
('algorithm', 'euler-path',       '欧拉路径/回路',    '欧拉图',                                            409),
('algorithm', '2-sat',            '2-SAT',           '布尔可满足性',                                      410),
('algorithm', 'virtual-tree',     '虚树',            '关键点建虚树',                                      411),
('algorithm', 'dominator-tree',   '支配树',          '支配关系',                                          412),
('algorithm', 'graph-coloring',   '图着色',          '色多项式、染色',                                     413);

-- ------------------------------------------------------------
-- 树 (1200-3200)
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'tree-basic',   '树基础',          '树的遍历、直径、重心',                                  501),
('algorithm', 'lca',          'LCA最近公共祖先',  '倍增LCA、Tarjan LCA、树上RMQ',                         502),
('algorithm', 'hld',          '树链剖分',        '重链剖分',                                             503),
('algorithm', 'centroid',     '点分治/边分治',    '树上分治',                                             504),
('algorithm', 'dsu-on-tree',  '树上启发式合并',   '小规模暴力合并',                                       505),
('algorithm', 'euler-tour',   '欧拉序/DFS序',    '树转序列',                                             506),
('algorithm', 'tree-diff',    '树上差分',        '路径/子树差分',                                         507);

-- ------------------------------------------------------------
-- 数据结构 (1200-3500)
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'stack-queue',    '栈/队列',         '单调栈、单调队列、优先队列',                            601),
('algorithm', 'segment-tree',   '线段树',          '普通线段树、懒标记',                                   602),
('algorithm', 'segtree-beats',  'Segment Tree Beats', '势能线段树',                                      603),
('algorithm', 'bit',            '树状数组/BIT',    '一维/二维BIT',                                        604),
('algorithm', 'sparse-table',   'ST表/RMQ',        '稀疏表',                                             605),
('algorithm', 'block',          '分块',            'sqrt分解',                                            606),
('algorithm', 'balanced-bst',   '平衡树',          'Treap、Splay、红黑树、FHQ-Treap',                     607),
('algorithm', 'persistent',     '可持久化数据结构', '可持久化线段树/Trie/平衡树',                            608),
('algorithm', 'dsu',            '并查集',          '带权并查集、可撤销并查集、按秩合并',                      609),
('algorithm', 'cdq',            'CDQ分治',         '离线分治',                                            610),
('algorithm', 'li-chao',        '李超线段树',       '凸壳技巧',                                           611),
('algorithm', 'link-cut-tree',  'LCT动态树',       'Link-Cut Tree',                                     612),
('algorithm', 'kd-tree',        'KD树',            'K维空间查询',                                        613);

-- ------------------------------------------------------------
-- 字符串 (1400-3500)
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'kmp',           'KMP',          '模式匹配',                                              701),
('algorithm', 'hashing',       '字符串哈希',    'Rolling Hash',                                          702),
('algorithm', 'trie',          'Trie/字典树',   '前缀树',                                                703),
('algorithm', 'ac-automaton',  'AC自动机',      '多模式匹配',                                             704),
('algorithm', 'suffix-array',  '后缀数组',      'SA+LCP',                                                705),
('algorithm', 'sam',           '后缀自动机',    'SAM',                                                   706),
('algorithm', 'manacher',      'Manacher',      '回文串',                                                707),
('algorithm', 'z-function',    'Z函数',         '扩展KMP',                                               708),
('algorithm', 'pam',           '回文自动机',    '回文树',                                                 709);

-- ------------------------------------------------------------
-- 几何 (1600-3500)
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'geometry-basic',    '基础计算几何',       '点线面、叉积、距离',                               801),
('algorithm', 'convex-hull',       '凸包',              'Graham/Andrew',                                   802),
('algorithm', 'half-plane',        '半平面交',           '半平面交集',                                      803),
('algorithm', 'rotating-calipers', '旋转卡壳',           '凸包直径等',                                      804),
('algorithm', 'delaunay',          'Delaunay三角剖分',   'Voronoi图',                                      805),
('algorithm', 'minkowski-sum',     '闵可夫斯基和',       '凸包合并',                                        806);

-- ------------------------------------------------------------
-- 其他高级算法
-- ------------------------------------------------------------

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order) VALUES
('algorithm', 'game-theory',    '博弈论',       'Nim、SG定理、组合博弈',                                   901),
('algorithm', 'interactive',    '交互题',       '在线交互',                                                902),
('algorithm', 'randomized',     '随机化算法',    '随机化技巧',                                              903),
('algorithm', 'parallel-bs',    '整体二分',      '离线整体二分',                                            904),
('algorithm', 'mo-algo',        '莫队算法',      '普通莫队、带修莫队、树上莫队、回滚莫队',                     905),
('algorithm', 'bitwise',        '位运算',        '位运算技巧、异或线性基',                                   906),
('algorithm', 'meet-in-middle', '折半搜索',      '双向BFS、折半枚举',                                       907),
('algorithm', 'matroid',        '拟阵',          '拟阵交集',                                               908),
('algorithm', 'offline',        '离线算法',      '离线处理、离线询问排序',                                    909),
('algorithm', 'online',         '在线算法',      '强制在线',                                                910);
