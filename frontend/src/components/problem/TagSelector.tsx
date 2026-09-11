'use client';

import { useState, useEffect, useCallback, useRef } from 'react';
import { Search, X, Loader2 } from 'lucide-react';
import { listTagsByLevel } from '@/lib/api';
import { cn } from '@/lib/utils';
import type { ProblemLevel, TagCategory } from '@/lib/types';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface TagSelectorProps {
  level: ProblemLevel;
  difficulty?: number;
  selectedTags: string[];
  onChange: (tags: string[]) => void;
}

// ---------------------------------------------------------------------------
// TagSelector
// ---------------------------------------------------------------------------

export default function TagSelector({
  level,
  difficulty,
  selectedTags,
  onChange,
}: TagSelectorProps) {
  const [tags, setTags] = useState<TagCategory[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // ---- Fetch tags when level/difficulty changes ----
  const fetchTags = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await listTagsByLevel(level, difficulty);
      setTags(res.data ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load tags');
      setTags([]);
    } finally {
      setLoading(false);
    }
  }, [level, difficulty]);

  useEffect(() => {
    onChange([]);
    setSearch('');
    fetchTags();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [level, difficulty, fetchTags]);

  // ---- Close dropdown on outside click ----
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  // ---- Filtered tags (flat list) ----
  const normalizedSearch = search.toLowerCase().trim();

  const filteredTags = normalizedSearch
    ? tags.filter(
        (tag) =>
          tag.display_name.toLowerCase().includes(normalizedSearch) ||
          tag.tag_name.toLowerCase().includes(normalizedSearch),
      )
    : tags;

  // ---- Tag helpers ----
  function toggleTag(tagName: string) {
    if (selectedTags.includes(tagName)) {
      onChange(selectedTags.filter((t) => t !== tagName));
    } else {
      onChange([...selectedTags, tagName]);
    }
  }

  function removeTag(tagName: string) {
    onChange(selectedTags.filter((t) => t !== tagName));
  }

  // Resolve display name for a selected tag name
  function resolveDisplayName(tagName: string): string {
    const tag = tags.find((t) => t.tag_name === tagName);
    return tag?.display_name ?? tagName;
  }

  return (
    <div ref={containerRef} className="relative">
      {/* Selected tag badges */}
      <div
        className={cn(
          'flex min-h-[42px] flex-wrap items-center gap-1.5 rounded-lg border px-3 py-2 transition-colors',
          open
            ? 'border-forge-400 ring-2 ring-forge-200 dark:border-forge-600 dark:ring-forge-900'
            : 'border-anvil-200 dark:border-anvil-700',
          'cursor-text bg-white dark:bg-anvil-900',
        )}
        onClick={() => setOpen(true)}
      >
        {selectedTags.map((tagName) => (
          <span
            key={tagName}
            className="inline-flex items-center gap-1 rounded-md bg-forge-100 px-2 py-0.5 text-xs font-medium text-forge-700 dark:bg-forge-900/40 dark:text-forge-300"
          >
            {resolveDisplayName(tagName)}
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                removeTag(tagName);
              }}
              className="rounded-full p-0.5 hover:bg-forge-200 dark:hover:bg-forge-800"
            >
              <X className="h-3 w-3" />
            </button>
          </span>
        ))}
        {selectedTags.length === 0 && (
          <span className="text-sm text-anvil-400 dark:text-anvil-500">
            Select tags...
          </span>
        )}
      </div>

      {/* Dropdown panel */}
      {open && (
        <div className="absolute left-0 top-full z-50 mt-1 w-full animate-fade-in rounded-lg border border-anvil-200 bg-white shadow-lg dark:border-anvil-700 dark:bg-anvil-900">
          {/* Search input */}
          <div className="relative border-b border-anvil-100 p-2 dark:border-anvil-700">
            <Search className="pointer-events-none absolute left-4 top-1/2 h-4 w-4 -translate-y-1/2 text-anvil-400" />
            <input
              type="text"
              placeholder="Search tags..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="h-8 w-full rounded-md border-none bg-anvil-50 pl-8 pr-3 text-sm text-anvil-900 placeholder-anvil-400 focus:outline-none focus:ring-0 dark:bg-anvil-800 dark:text-anvil-100 dark:placeholder-anvil-500"
              autoFocus
            />
          </div>

          {/* Tag list */}
          <div className="max-h-64 overflow-y-auto p-2">
            {loading && (
              <div className="flex items-center justify-center py-6 text-anvil-400">
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                <span className="text-sm">Loading tags...</span>
              </div>
            )}

            {error && (
              <div className="px-3 py-4 text-center text-sm text-danger-500">
                {error}
                <button
                  type="button"
                  className="ml-2 text-forge-600 underline hover:text-forge-700"
                  onClick={fetchTags}
                >
                  Retry
                </button>
              </div>
            )}

            {!loading && !error && filteredTags.length === 0 && (
              <p className="py-4 text-center text-sm text-anvil-400">
                No tags found.
              </p>
            )}

            {!loading && !error && filteredTags.length > 0 && (
              <div className="flex flex-wrap gap-1 px-1">
                {filteredTags.map((tag) => {
                  const selected = selectedTags.includes(tag.tag_name);
                  return (
                    <button
                      type="button"
                      key={tag.id}
                      onClick={() => toggleTag(tag.tag_name)}
                      className={cn(
                        'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                        selected
                          ? 'bg-forge-600 text-white dark:bg-forge-500'
                          : 'bg-anvil-100 text-anvil-700 hover:bg-forge-100 hover:text-forge-700 dark:bg-anvil-800 dark:text-anvil-300 dark:hover:bg-forge-900/40 dark:hover:text-forge-300',
                      )}
                    >
                      {tag.display_name}
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
