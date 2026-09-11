import { isTerminal, normalizeStatus } from '@/lib/status';

export function useNormalizedStatus(status: string): {
  friendly: string;
  terminal: boolean;
} {
  const friendly = normalizeStatus(status);
  return {
    friendly,
    terminal: isTerminal(friendly),
  };
}
