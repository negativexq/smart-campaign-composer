import type { StatusTone } from "./deliveryStatus";

const META: Record<string, { label: string; tone: StatusTone }> = {
  DRAFT: { label: "Draft", tone: "neutral" },
  GENERATING: { label: "Generating", tone: "primary" },
  READY_FOR_REVIEW: { label: "Ready for review", tone: "primary" },
  GENERATION_FAILED: { label: "Generation failed", tone: "error" },
  APPROVED: { label: "Approved", tone: "primary" },
  SCHEDULED: { label: "Scheduled", tone: "primary" },
  DISPATCHING: { label: "Sending", tone: "primary" },
  PAUSED: { label: "Paused", tone: "warning" },
  COMPLETED: { label: "Completed", tone: "success" },
  COMPLETED_WITH_FAILURES: { label: "Completed with failures", tone: "warning" },
  CANCELLED: { label: "Cancelled", tone: "neutral" },
};

export function campaignStatusMeta(status: string) {
  return META[status] ?? { label: status, tone: "neutral" as StatusTone };
}

export const isDraftStatus = (status: string) => ["DRAFT", "READY_FOR_REVIEW", "GENERATION_FAILED"].includes(status);

export const isFinishedStatus = (status: string) => ["COMPLETED", "COMPLETED_WITH_FAILURES"].includes(status);

type RowState = { status: string; schedule?: { repeats: boolean; next_run_at: string | null } | null; last_run?: { number: number } | null };

/**
 * The status a list row shows. A repeating campaign between runs is not finished, so
 * it says it is waiting; one sending says which run.
 */
export function rowStatusMeta(row: RowState): { label: string; tone: StatusTone } {
  const repeats = Boolean(row.schedule?.repeats);
  if (repeats && row.status === "SCHEDULED" && row.last_run) return { label: "Waiting for next run", tone: "primary" };
  if (repeats && row.status === "DISPATCHING" && row.last_run) return { label: `Sending run #${row.last_run.number}`, tone: "primary" };
  return campaignStatusMeta(row.status);
}
