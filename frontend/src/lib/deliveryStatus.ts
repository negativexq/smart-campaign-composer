import { Ban, CheckCircle2, CircleHelp, Clock, RotateCw, Send, XCircle, type LucideIcon } from "lucide-vue-next";

export const DELIVERY_STATUSES = ["QUEUED", "SENDING", "SENT", "RETRYING", "FAILED", "SKIPPED", "UNKNOWN"] as const;

export type DeliveryStatus = (typeof DELIVERY_STATUSES)[number];
export type StatusTone = "neutral" | "primary" | "success" | "warning" | "error";

interface StatusMeta {
  label: string;
  description: string;
  tone: StatusTone;
  icon: LucideIcon;
}

export const deliveryStatusMeta: Record<DeliveryStatus, StatusMeta> = {
  QUEUED: {
    label: "Queued",
    description: "Waiting for its turn to send.",
    tone: "neutral",
    icon: Clock,
  },
  SENDING: {
    label: "Sending",
    description: "A send attempt is in progress.",
    tone: "primary",
    icon: Send,
  },
  SENT: {
    label: "Accepted",
    description: "The mock provider accepted the message. This does not confirm delivery to a person.",
    tone: "success",
    icon: CheckCircle2,
  },
  RETRYING: {
    label: "Retrying",
    description: "A temporary error occurred. Another attempt is scheduled.",
    tone: "warning",
    icon: RotateCw,
  },
  FAILED: {
    label: "Failed",
    description: "The provider rejected the message or attempts ran out.",
    tone: "error",
    icon: XCircle,
  },
  SKIPPED: {
    label: "Skipped",
    description: "Not sent, for example because consent was withdrawn.",
    tone: "neutral",
    icon: Ban,
  },
  UNKNOWN: {
    label: "Outcome unknown",
    description: "Attempts ended on a timeout, so the provider may or may not have accepted the message.",
    tone: "warning",
    icon: CircleHelp,
  },
};
