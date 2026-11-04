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
