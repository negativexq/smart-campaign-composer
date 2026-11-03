import { EventStreamParser } from "../lib/sse";
import type { DeliveryQuery } from "../lib/report";
import { currentUser } from "../lib/session";

export type Condition = { field: string; label: string; value: string };
export type Assumption = { field: string; note: string };
export type Audience = { total: number; sms: number; email: number };

export type MessageField = "sms" | "email_subject" | "email_body";
export type Messages = Record<MessageField, string>;
export type ContentIssue = { field: string; reason: string; message: string };
export type SampleRecipient = { id: number; label: string; first_name: string };

export type ScheduleSpec = {
  kind: "once" | "interval" | "weekly";
  at?: string;
  every_hours?: number;
  starts_at?: string;
  days?: string[];
  time?: string;
  timezone?: string;
  ends_at?: string;
};

export type CampaignSchedule = {
  created_by?: string | null;
  spec: ScheduleSpec;
  description: string;
  next_run_at: string | null;
  last_run_at: string | null;
  runs: number;
};

export type Progress = {
  total: number;
  processed: number;
  attempts: number;
  by_status: Record<string, number>;
  by_channel: Record<string, Record<string, number>>;
  responses: Record<string, number>;
};

export type Delivery = {
  id: string;
  user_id: number;
  first_name: string;
  channel: string;
  status: string;
  attempts: number;
  response_code: number | null;
  reason: string;
  provider_message_id: string;
  next_attempt_at: string;
  sent_at: string | null;
  updated_at: string;
  opened_at?: string | null;
};

export type DeliveryAttempt = {
  attempt: number;
  response_code: number | null;
  outcome: string;
  error: string;
  occurred_at: string;
};

export type Campaign = {
  created_by?: string | null;
  id: string;
  name: string;
  name_source: "brief" | "ai" | "user";
  brief: string;
  status: string;
  channels: string[];
  version: number;
  proposal?: { assumptions: Assumption[]; unsupported_conditions: string[] };
  conditions: Condition[];
  audience: Audience;
  audience_as_of?: string;
  blockers: string[];
  content?: Messages & { edited: MessageField[] };
  content_limits: { sms_name_length: number; email_name_length: number };
  content_warnings: ContentIssue[];
  approved_at?: string;
  schedule?: CampaignSchedule;
  created_at: string;
  updated_at: string;
};

export type CampaignSummary = {
  id: string;
  name: string;
  brief: string;
  status: string;
  channels: string[];
  deliveries_total: number;
  deliveries_processed: number;
  created_at: string;
  updated_at: string;
};

export type ActivityEntry = {
  campaign_id: string;
  campaign_name: string;
  type: string;
  actor_type: string;
  actor: string | null;
  details: Record<string, unknown>;
  occurred_at: string;
};

export type Overview = {
  counts: { total: number; drafts: number; active: number; completed: number; failed: number };
  recent_campaigns: CampaignSummary[];
  recent_activity: ActivityEntry[];
};

export type CampaignGroup = "" | "drafts" | "active" | "completed" | "failed";

export type AiConfig = { llm_provider: string; llm_model: string };

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly issues: ContentIssue[] = [],
  ) {
    super(message);
  }
}

const STREAM_LOST = "The connection dropped before the messages were finished. Try again.";
const UNREACHABLE = "We could not reach the server. Check your connection and try again.";

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, init);
  } catch {
    throw new ApiError(UNREACHABLE, 0);
  }
  const body = await response.json().catch(() => null);
  if (response.status === 401) currentUser.value = null;
  if (!response.ok) {
    throw new ApiError(body?.error ?? UNREACHABLE, response.status, body?.issues);
  }
  return body as T;
}

function post<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export async function createCampaign(brief: string, channels: string[]): Promise<string> {
  return (await post<{ id: string }>("/api/campaigns", { brief, channels })).id;
}

export function proposeSegment(id: string, input: { brief?: string; refinement?: string } = {}): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/segment`, input);
}

export function renameCampaign(id: string, name: string): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
}

export function getCampaign(id: string): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}`);
}

export function getOverview(): Promise<Overview> {
  return request<Overview>("/api/overview");
}

export async function listCampaigns(filter: { group: CampaignGroup; query: string }): Promise<CampaignSummary[]> {
  const params = new URLSearchParams();
  if (filter.group) params.set("group", filter.group);
  if (filter.query.trim()) params.set("q", filter.query.trim());
  const suffix = params.size ? `?${params}` : "";
  return (await request<{ campaigns: CampaignSummary[] }>(`/api/campaigns${suffix}`)).campaigns;
}

export async function getActivity(id: string): Promise<ActivityEntry[]> {
  return (await request<{ activity: ActivityEntry[] }>(`/api/campaigns/${encodeURIComponent(id)}/activity`)).activity;
}

export function saveContent(id: string, messages: Messages): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/content`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(messages),
  });
}

export async function getSampleRecipients(id: string): Promise<SampleRecipient[]> {
  return (await request<{ recipients: SampleRecipient[] }>(`/api/campaigns/${encodeURIComponent(id)}/sample-recipients`)).recipients;
}

export function approveCampaign(id: string): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/approve`, {});
}

export function dispatchCampaign(id: string): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/dispatch`, {});
}

export function scheduleCampaign(id: string, spec: ScheduleSpec): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/schedule`, spec);
}

export function cancelSchedule(id: string): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/schedule`, { method: "DELETE" });
}

export function getProgress(id: string): Promise<Progress> {
  return request<Progress>(`/api/campaigns/${encodeURIComponent(id)}/progress`);
}

export async function getDeliveries(id: string, filter: Partial<DeliveryQuery> & { limit: number }): Promise<Delivery[]> {
  const params = new URLSearchParams({ limit: String(filter.limit) });
  for (const [key, value] of Object.entries(filter)) {
    if (value && key !== "limit") params.set(key, String(value));
  }
  return (await request<{ deliveries: Delivery[] }>(`/api/campaigns/${encodeURIComponent(id)}/deliveries?${params}`)).deliveries;
}

export type TimelineBucket = {
  at: string;
  accepted: number;
  rate_limited: number;
  server_error: number;
  rejected: number;
  timeout: number;
};

export type Report = {
  reasons: { reason: string; deliveries: number }[];
  problems: { outcome: string; deliveries: number; attempts: number }[];
  timeline: { bucket_seconds: number; buckets: TimelineBucket[] };
  engagement: { channel: string; accepted: number; opened: number }[];
};

export async function getReport(id: string): Promise<Report> {
  return request<Report>(`/api/campaigns/${encodeURIComponent(id)}/report`);
}

export async function getDeliveryAttempts(id: string, delivery: string): Promise<DeliveryAttempt[]> {
  const path = `/api/campaigns/${encodeURIComponent(id)}/deliveries/${encodeURIComponent(delivery)}/attempts`;
  return (await request<{ attempts: DeliveryAttempt[] }>(path)).attempts;
}

export type LiveState = {
  status: string;
  progress: Progress;
};

export type LiveHandlers = {
  onUpdate: (state: LiveState) => void;
  onEnd: () => void;
  onConnection: (connected: boolean) => void;
};

// Follows a campaign until the server says it can no longer change. The browser
// reconnects by itself, and the first event after that is the current state.
export function watchCampaign(id: string, handlers: LiveHandlers): () => void {
  const source = new EventSource(`/api/campaigns/${encodeURIComponent(id)}/events`);
  source.onopen = () => handlers.onConnection(true);
  source.onerror = () => handlers.onConnection(false);
  source.addEventListener("update", (event) => handlers.onUpdate(JSON.parse((event as MessageEvent<string>).data) as LiveState));
  source.addEventListener("end", () => {
    source.close();
    handlers.onEnd();
  });
  return () => source.close();
}

export type GenerationHandlers = {
  onDelta: (field: MessageField, text: string) => void;
  onReset: () => void;
};

// Streams a generation and resolves with the validated campaign. The streamed
// text is only a preview: what counts is the campaign in the final event.
export async function generateContent(
  id: string,
  input: { instruction?: string },
  handlers: GenerationHandlers,
  signal: AbortSignal,
): Promise<Campaign> {
  let response: Response;
  try {
    response = await fetch(`/api/campaigns/${encodeURIComponent(id)}/generate`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
      signal,
    });
  } catch (failure) {
    if (signal.aborted) throw failure;
    throw new ApiError(UNREACHABLE, 0);
  }
  if (!response.ok || !response.body) {
    const body = await response.json().catch(() => null);
    throw new ApiError(body?.error ?? UNREACHABLE, response.status);
  }

  const parser = new EventStreamParser();
  const decoder = new TextDecoder();
  const reader = response.body.getReader();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) throw new ApiError(STREAM_LOST, 0);
      for (const { event, data } of parser.push(decoder.decode(value, { stream: true }))) {
        const payload = JSON.parse(data);
        if (event === "delta") handlers.onDelta(payload.field, payload.text);
        if (event === "reset") handlers.onReset();
        if (event === "done") return payload as Campaign;
        if (event === "error") throw new ApiError(payload.error, 0);
      }
    }
  } catch (failure) {
    if (signal.aborted || failure instanceof ApiError) throw failure;
    throw new ApiError(STREAM_LOST, 0);
  }
}

export function errorMessage(failure: unknown): string {
  return failure instanceof ApiError ? failure.message : "Something went wrong. Try again.";
}

export async function fetchAiConfig(): Promise<AiConfig | null> {
  try {
    return await request<AiConfig>("/api/config");
  } catch {
    return null;
  }
}
