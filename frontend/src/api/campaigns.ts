export type Condition = { field: string; label: string; value: string };
export type Assumption = { field: string; note: string };
export type Audience = { total: number; sms: number; email: number };

export type Campaign = {
  id: string;
  brief: string;
  status: string;
  version: number;
  proposal?: { assumptions: Assumption[]; unsupported_conditions: string[] };
  conditions: Condition[];
  audience: Audience;
  audience_as_of?: string;
  blockers: string[];
  created_at: string;
  updated_at: string;
};

export type CampaignSummary = {
  id: string;
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
  brief: string;
  type: string;
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
  ) {
    super(message);
  }
}

const UNREACHABLE = "We could not reach the server. Check your connection and try again.";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, init);
  } catch {
    throw new ApiError(UNREACHABLE, 0);
  }
  const body = await response.json().catch(() => null);
  if (!response.ok) {
    throw new ApiError(body?.error ?? UNREACHABLE, response.status);
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

export async function createCampaign(brief: string): Promise<string> {
  return (await post<{ id: string }>("/api/campaigns", { brief })).id;
}

export function proposeSegment(id: string, input: { brief?: string; refinement?: string } = {}): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/segment`, input);
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
