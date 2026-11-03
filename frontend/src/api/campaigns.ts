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
};

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

export async function fetchAiConfig(): Promise<AiConfig | null> {
  try {
    return await request<AiConfig>("/api/config");
  } catch {
    return null;
  }
}
