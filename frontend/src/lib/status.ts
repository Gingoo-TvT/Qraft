// ============================================================================
// Qraft - Workflow Status Helpers
// ============================================================================

export const TEMPORAL_STATUS_MAP: Record<string, string> = {
  Running: 'running',
  Completed: 'approved',
  Failed: 'failed',
  Canceled: 'cancelled',
  TimedOut: 'failed',
  Terminated: 'cancelled',
  WORKFLOW_EXECUTION_STATUS_RUNNING: 'running',
  WORKFLOW_EXECUTION_STATUS_COMPLETED: 'approved',
  WORKFLOW_EXECUTION_STATUS_FAILED: 'failed',
  WORKFLOW_EXECUTION_STATUS_CANCELED: 'cancelled',
  WORKFLOW_EXECUTION_STATUS_TIMED_OUT: 'failed',
  WORKFLOW_EXECUTION_STATUS_TERMINATED: 'cancelled',
};

export function normalizeStatus(status: string): string {
  return TEMPORAL_STATUS_MAP[status] ?? status.toLowerCase();
}

export const TERMINAL_STATUSES = new Set([
  'approved',
  'rejected_quarantined',
  'failed',
  'cancelled',
]);

export function isTerminal(rawOrFriendly: string): boolean {
  return TERMINAL_STATUSES.has(normalizeStatus(rawOrFriendly));
}
