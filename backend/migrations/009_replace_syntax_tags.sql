-- Replace syntax-level tags with new classification:
-- 语法 (grammar basics) + 数据结构子类 (线性表、栈、队列、字符串、树、图、排序)

-- ============================================================
-- 1. Remove old syntax-level tags
-- ============================================================

DELETE FROM tag_categories WHERE level = 'syntax';

-- ============================================================
-- 2. Insert new syntax-level tags
-- ============================================================

INSERT INTO tag_categories (level, tag_name, display_name, description, sort_order, min_difficulty, max_difficulty) VALUES
('syntax', 'grammar',          '语法',   '变量、数据类型、输入输出、条件判断、循环、函数等基础语法',  1, 800, 1200),
('syntax', 'ds-linear-list',   '线性表', '数组、链表等线性数据结构的基本操作',                       2, 800, 1200),
('syntax', 'ds-stack',         '栈',     '栈的基本操作、表达式求值、括号匹配等',                    3, 800, 1200),
('syntax', 'ds-queue',         '队列',   '队列的基本操作、循环队列、优先队列基础',                   4, 800, 1200),
('syntax', 'ds-string',        '字符串', '字符串处理、匹配、操作等基础题',                          5, 800, 1200),
('syntax', 'ds-tree',          '树',     '二叉树的遍历、构建、基本性质',                            6, 800, 1200),
('syntax', 'ds-graph',         '图',     '图的存储、遍历（DFS/BFS）、基本概念',                     7, 800, 1200),
('syntax', 'ds-sorting',       '排序',   '冒泡、选择、插入、快排、归并等排序算法',                   8, 800, 1200);
