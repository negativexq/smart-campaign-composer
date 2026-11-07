import type { ChannelAlert } from "../lib/channelAlerts";
import { EventStreamParser } from "../lib/sse";
import type { DeliveryQuery } from "../lib/report";
import { currentUser } from "../lib/session";

export type Evidence = { field: string; quote: string };
export type Condition = { field: string; label: string; value: string };
export type AudienceBreakdown = { everyone: number; steps: { field: string; remaining: number }[] };
export type Assumption = { field: string; note: string };
export type Audience = { total: number; sms: number; email: number };

export type MessageField = "sms" | "email_subject" | "email_preheader" | "email_headline" | "email_body" | "email_cta_label" | "email_cta_url" | "email_template";
export type Messages = Record<MessageField, string>;
export type ContentIssue = { field: string; reason: string; message: string };
export type SampleRecipient = { id: number; label: string; first_name: string; language?: string };
export type LanguageAudience = { language: string; total: number; sms: number; email: number; sms_name_length: number; email_name_length: number };
export type MessageVersion = Messages & { edited: MessageField[]; language?: string; variant?: string; email_design?: EmailDesign | null };
/** One version of a test: its messages and their other languages. */
export type VariantContent = MessageVersion & { translations?: MessageVersion[] };

export type ScheduleSpec = {
  kind: "once" | "interval" | "weekly";
  at?: string;
  every_hours?: number;
  starts_at?: string;
  days?: string[];
  time?: string;
  timezone?: string;
  ends_at?: string;
  /** Whom later runs may message again: a repeating send's own, or a one-time send after earlier ones. */
  reach?: ScheduleReach;
  reach_after_days?: number;
};

export type ScheduleReach = "new" | "after" | "everyone";

export interface BriefPart {
  text: string;
  kind: "audience" | "message" | "other";
  used_as?: string[] | null;
}

export type CampaignSchedule = {
  created_by?: string | null;
  spec: ScheduleSpec;
  description: string;
  next_run_at: string | null;
  last_run_at: string | null;
  runs: number;
  /** Cancelled while it still had sends to come. */
  stopped?: boolean;
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
  variant?: string;
  attempts: number;
  calls?: number;
  response_code: number | null;
  reason: string;
  provider_message_id: string;
  next_attempt_at: string;
  sent_at: string | null;
  updated_at: string;
  delivered_at?: string | null;
  bounced_at?: string | null;
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
  email_template?: string;
  email_design?: EmailDesign | null;
  brief: string;
  status: string;
  channels: string[];
  tone?: string;
  offer_percent?: number | null;
  not_messaged_within_days?: number | null;
  version: number;
  proposal?: { saved_audience?: string; brief_parts?: BriefPart[] | null; unsure?: string[] | null; not_applied?: string[] | null; not_audience?: string[] | null; assumptions: Assumption[]; evidence?: Evidence[] | null; unsupported_conditions: string[] };
  conditions: Condition[];
  audience: Audience;
  audience_as_of?: string;
  audience_snapshot?: { counts: Audience; breakdown: AudienceBreakdown };
  provider?: string;
  model?: string;
  blockers: string[];
  content?: VariantContent & { variant_b?: VariantContent };
  ab_test?: boolean;
  variant_a_percent?: number;
  variant_audience?: { variant: string; language: string; total: number; sms: number; email: number }[];
  languages?: LanguageAudience[] | null;
  excluded_languages?: string[];
  content_limits: { sms_name_length: number; email_name_length: number };
  content_warnings: ContentIssue[];
  approved_at?: string;
  schedule?: CampaignSchedule;
  tags?: string[];
  /** When the campaign was archived; nothing sends from the archive. */
  archived_at?: string | null;
  created_at: string;
  updated_at: string;
  /** Names the stored messages; a save sends it back so it cannot undo a newer change. */
  content_revision?: string;
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
  /** The sending rule, when the campaign has one. */
  schedule?: { description: string; repeats: boolean; next_run_at: string | null; stopped?: boolean } | null;
  /** The latest send, so a repeating campaign shows its last result. */
  last_run?: RunSummary | null;
  tags?: string[];
  archived_at?: string | null;
};

/** One send of a campaign and how its messages ended. */
export type RunSummary = {
  number: number;
  started_at: string;
  finished_at: string | null;
  total: number;
  processed: number;
  sent: number;
  failed: number;
  skipped: number;
  unknown: number;
};

/** A send planned for later. */
export type UpcomingSend = { campaign_id: string; name: string; at: string; repeats: boolean };

export type ActivityEntry = {
  id?: number;
  campaign_id: string;
  campaign_name: string;
  type: string;
  actor_type: string;
  actor: string | null;
  details: Record<string, unknown>;
  occurred_at: string;
};

export type NotificationEntry = ActivityEntry & { id: number };

export type Overview = {
  counts: { total: number; drafts: number; active: number; completed: number; failed: number };
  recent_campaigns: CampaignSummary[];
  recent_activity: ActivityEntry[];
  channels?: ChannelUse[];
};

export type ChannelUse = {
  channel: string;
  provider: string;
  last_accepted_at: string | null;
  calls_last_hour: number;
  not_accepted_last_hour: number;
  busiest_second_last_hour: number;
};

export type CampaignGroup = "" | "drafts" | "review" | "active" | "repeating" | "sending" | "paused" | "completed" | "failed" | "archived";

export type CampaignList = {
  campaigns: CampaignSummary[];
  total: number;
  /** How many campaigns each group holds with the same search and channel; "all" holds every one. */
  groups?: Partial<Record<CampaignGroup | "all", number>>;
  upcoming?: UpcomingSend[];
};

export type AiConfig = { llm_provider: string; llm_model: string };
export type AppSettings = AiConfig & { rate_limit_per_second: number; max_attempts: number; customers: number };

export function getSettings(): Promise<AppSettings> {
  return request<AppSettings>("/api/settings");
}

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

export type CampaignOptions = { tone?: string; offer_percent?: number; not_messaged_within_days?: number; ab_test?: boolean; variant_a_percent?: number; email_template?: string; saved_audience_id?: string };

export async function createCampaign(brief: string, channels: string[], options: CampaignOptions = {}): Promise<string> {
  return (await post<{ id: string }>("/api/campaigns", { brief, channels, ...options })).id;
}

export function proposeSegment(id: string, input: { brief?: string; refinement?: string } = {}): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/segment`, input);
}

export function setExcludedLanguages(id: string, excluded: string[]): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/languages`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ excluded }),
  });
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

export type BlockType = "logo" | "image" | "heading" | "text" | "button" | "divider";
export type EmailBlock = {
  id: string;
  type: BlockType;
  slot?: "headline" | "body" | "cta";
  text?: string;
  label?: string;
  url?: string;
  asset_id?: string;
  alt?: string;
  width?: number;
  link?: string;
  align?: string;
  style?: string;
};
export type EmailDesign = { id: string; name: string; accent: string; page: string; button: string; corners: number; font: string; footer?: string; blocks: EmailBlock[] };
export type SavedEmailDesign = EmailDesign & { version: string; campaigns: number; updated_at: string };

export async function getEmailDesigns(): Promise<SavedEmailDesign[]> {
  return (await request<{ designs: SavedEmailDesign[] }>("/api/email-designs")).designs ?? [];
}

// A saved design also says how it is used, and the API refuses fields it does not expect.
function designOnly(design: EmailDesign): EmailDesign {
  const { id, name, accent, page, button, corners, font, footer, blocks } = design;
  return { id, name, accent, page, button, corners, font, footer, blocks };
}

export function saveEmailDesign(id: string, design: EmailDesign): Promise<SavedEmailDesign> {
  if (!id) return post<SavedEmailDesign>("/api/email-designs", designOnly(design));
  return request<SavedEmailDesign>(`/api/email-designs/${encodeURIComponent(id)}`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(designOnly(design)) });
}

export async function deleteEmailDesign(id: string): Promise<void> {
  await request<void>(`/api/email-designs/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export type Asset = { id: string; name: string; mime: string; bytes: number; width: number; height: number; in_use: boolean; url: string; created_at: string };

export async function getAssets(): Promise<Asset[]> {
  return (await request<{ assets: Asset[] }>("/api/assets")).assets ?? [];
}

export function uploadAsset(file: File): Promise<Asset> {
  const form = new FormData();
  form.append("file", file);
  return request<Asset>("/api/assets", { method: "POST", body: form });
}

export async function deleteAsset(id: string): Promise<void> {
  await request<void>(`/api/assets/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export type Brand = { name: string; logo_asset_id: string; accent: string; page: string; button: string; font: string; footer: string };

export function getBrand(): Promise<Brand> {
  return request<Brand>("/api/brand");
}

export function saveBrand(brand: Brand): Promise<Brand> {
  return request<Brand>("/api/brand", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(brand) });
}

export type EmailTemplateUse = { id: string; version: string; campaigns: number };

export async function getEmailTemplates(): Promise<EmailTemplateUse[]> {
  return (await request<{ templates: EmailTemplateUse[] }>("/api/email-templates")).templates;
}

export type MonitorEntry = {
  id: string;
  campaign_id: string;
  campaign_name: string;
  user_id: number;
  first_name: string;
  channel: string;
  status: string;
  calls: number;
  response_code: number | null;
  reason: string;
  next_attempt_at: string;
  updated_at: string;
};
export type DeliveryMonitor = { counts: Record<string, number>; channels: Record<string, number>; reasons: { reason: string; count: number }[]; total: number; deliveries: MonitorEntry[]; paused: { id: string; name: string }[] };
export type MonitorQuery = { status: string; channel: string; outcome: string; reason: string; within_hours: string };

export function getDeliveryMonitor(query: MonitorQuery, limit: number, offset = 0): Promise<DeliveryMonitor> {
  const params = new URLSearchParams({ limit: String(limit) });
  for (const [key, value] of Object.entries(query)) if (value) params.set(key, value);
  if (offset) params.set("offset", String(offset));
  return request(`/api/delivery-monitor?${params}`);
}

export type SavedAudience = {
  id: string;
  name: string;
  conditions: Condition[];
  audience: Audience;
  counted_at: string;
  source_campaign_id: string | null;
  source_campaign_name: string | null;
  created_by: string | null;
  created_at: string;
};

export async function getAudiences(): Promise<SavedAudience[]> {
  return (await request<{ audiences: SavedAudience[] }>("/api/audiences")).audiences;
}

export function saveAudience(campaignId: string, name: string): Promise<SavedAudience> {
  return post<SavedAudience>("/api/audiences", { campaign_id: campaignId, name });
}

export function recountAudience(id: string): Promise<SavedAudience> {
  return post<SavedAudience>(`/api/audiences/${encodeURIComponent(id)}/recount`, {});
}

export async function deleteAudience(id: string): Promise<void> {
  await request<void>(`/api/audiences/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export type ChannelTotals = {
  channel: string;
  planned: number;
  skipped: number;
  accepted: number;
  failed: number;
  unknown: number;
  on_the_way: number;
  delivered: number;
  bounced: number;
  waiting: number;
  opened: number;
};
export type CallDay = { day: string; accepted: number; not_accepted: number };
export type CampaignResult = { id: string; name: string; status: string; accepted: number; failed: number; unknown: number };
export type Analytics = { days: number; channels: ChannelTotals[]; calls: CallDay[]; campaigns: CampaignResult[] };

export function getAnalytics(days: string): Promise<Analytics> {
  return request(`/api/analytics?days=${encodeURIComponent(days)}`);
}

export async function getChannels(): Promise<ChannelUse[]> {
  return (await request<{ channels: ChannelUse[] }>("/api/channels")).channels;
}

export function getOverview(): Promise<Overview> {
  return request<Overview>("/api/overview");
}

export type CampaignListFilter = { group: CampaignGroup; query: string; channel?: string; createdWithinDays?: string; sort?: string; tag?: string };

export function listCampaigns(filter: CampaignListFilter, offset = 0): Promise<CampaignList> {
  const params = new URLSearchParams();
  if (filter.group) params.set("group", filter.group);
  if (filter.query.trim()) params.set("q", filter.query.trim());
  if (filter.channel) params.set("channel", filter.channel);
  if (filter.createdWithinDays) params.set("created_within_days", filter.createdWithinDays);
  if (filter.sort) params.set("sort", filter.sort);
  if (filter.tag) params.set("tag", filter.tag);
  if (offset) params.set("offset", String(offset));
  const suffix = params.size ? `?${params}` : "";
  return request(`/api/campaigns${suffix}`);
}

export type TagUse = { tag: string; campaigns: number };

/** Tags on campaigns that are not archived, with how many carry each. */
export async function getTags(): Promise<TagUse[]> {
  return (await request<{ tags: TagUse[] }>("/api/campaign-tags")).tags ?? [];
}

export function setTags(id: string, tags: string[]): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/tags`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tags }),
  });
}

export function archiveCampaign(id: string): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/archive`, { method: "POST" });
}

export function unarchiveCampaign(id: string): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/archive`, { method: "DELETE" });
}

export async function deleteCampaign(id: string): Promise<void> {
  await request<void>(`/api/campaigns/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function getActivity(id: string): Promise<ActivityEntry[]> {
  return (await request<{ activity: ActivityEntry[] }>(`/api/campaigns/${encodeURIComponent(id)}/activity`)).activity;
}

const EMAIL_FIELDS: MessageField[] = ["email_subject", "email_preheader", "email_headline", "email_body", "email_cta_label", "email_cta_url", "email_template"];

export type EmailPreviewResult = { subject: string; html: string; text: string };

export function previewEmail(message: Partial<Messages> & { email_design?: EmailDesign | null }, firstName: string, language = "", editing = false): Promise<EmailPreviewResult> {
  // Stored content also says who edited what, which the template has no use for.
  const fields = EMAIL_FIELDS.map((field) => [field, message[field] ?? ""]);
  return post<EmailPreviewResult>("/api/email-preview", {
    ...Object.fromEntries(fields),
    ...(message.email_design ? { email_design: designOnly(message.email_design) } : {}),
    ...(language ? { language } : {}),
    ...(editing ? { editing } : {}),
    first_name: firstName,
  });
}

/** Saves an edit made to the messages at revision; a newer change is answered with 409 rather than undone. */
export function saveContent(id: string, messages: Messages & { language?: string; variant?: string; revision?: string }): Promise<Campaign> {
  return request<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/content`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(messages),
  });
}

export function getAudienceBreakdown(id: string): Promise<AudienceBreakdown> {
  return request<AudienceBreakdown>(`/api/campaigns/${encodeURIComponent(id)}/audience/breakdown`);
}

export async function getSampleRecipients(id: string): Promise<SampleRecipient[]> {
  return (await request<{ recipients: SampleRecipient[] }>(`/api/campaigns/${encodeURIComponent(id)}/sample-recipients`)).recipients;
}

export function approveCampaign(id: string): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/approve`, {});
}

export function resumeCampaign(id: string): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/resume`, {});
}

export async function getNotifications(): Promise<NotificationEntry[]> {
  return (await request<{ notifications: NotificationEntry[] }>("/api/notifications")).notifications;
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

/** run limits the counts to one send of the campaign; none counts every send. */
export function getProgress(id: string, run?: number | null): Promise<Progress> {
  return request<Progress>(`/api/campaigns/${encodeURIComponent(id)}/progress${run ? `?run=${run}` : ""}`);
}

export async function getRuns(id: string): Promise<RunSummary[]> {
  return (await request<{ runs?: RunSummary[] }>(`/api/campaigns/${encodeURIComponent(id)}/runs`)).runs ?? [];
}

function deliveryParams(filter: Partial<DeliveryQuery> & { run?: number }, start: Record<string, string> = {}) {
  const params = new URLSearchParams(start);
  for (const [key, value] of Object.entries(filter)) {
    if (value) params.set(key, String(value));
  }
  return params;
}

/** The address of everything a filter keeps, as a file to download. */
export function deliveriesCsvUrl(id: string, filter: Partial<DeliveryQuery> & { run?: number }): string {
  const params = deliveryParams(filter).toString();
  return `/api/campaigns/${encodeURIComponent(id)}/deliveries.csv${params ? `?${params}` : ""}`;
}

export async function getDeliveryCities(id: string): Promise<string[]> {
  return (await request<{ cities: string[] }>(`/api/campaigns/${encodeURIComponent(id)}/delivery-cities`)).cities;
}

/** What a copy would reach, counted now, and how many of them the campaign reached. */
export type DuplicatePreview = {
  not_messaged_within_days: number | null;
  source_rule: number | null;
  audience: Audience;
  reached_before: number;
  last_sent_at: string | null;
};

export function getDuplicatePreview(id: string, notMessagedWithinDays: number | null): Promise<DuplicatePreview> {
  const query = notMessagedWithinDays == null ? "" : `?not_messaged_within_days=${notMessagedWithinDays}`;
  return request<DuplicatePreview>(`/api/campaigns/${encodeURIComponent(id)}/duplicate-preview${query}`);
}

export function duplicateCampaign(id: string, notMessagedWithinDays: number | null = null): Promise<Campaign> {
  return post<Campaign>(`/api/campaigns/${encodeURIComponent(id)}/duplicate`, notMessagedWithinDays == null ? {} : { not_messaged_within_days: notMessagedWithinDays });
}

export function getDeliveries(id: string, filter: Partial<DeliveryQuery> & { run?: number; limit: number; offset?: number }): Promise<{ deliveries: Delivery[]; total: number }> {
  const { limit, offset, ...rest } = filter;
  const params = deliveryParams(rest, { limit: String(limit), ...(offset ? { offset: String(offset) } : {}) });
  return request(`/api/campaigns/${encodeURIComponent(id)}/deliveries?${params}`);
}

export type TimelineBucket = {
  at: string;
  accepted: number;
  rate_limited: number;
  server_error: number;
  rejected: number;
  timeout: number;
  /** Calls refused with a 401; absent from servers that predate it. */
  unauthorized?: number;
};

export type ReportLifecycle = {
  channel: string;
  planned: number;
  eligible: number;
  called: number;
  accepted: number;
  delivered: number;
  bounced: number;
  waiting: number;
  arrived_unknown: number;
};

export type ReportVariant = {
  variant: string;
  channel: string;
  planned: number;
  accepted: number;
  delivered: number;
  bounced: number;
  waiting: number;
  opened: number;
};

export type Report = {
  reasons: { reason: string; deliveries: number }[];
  problems: { outcome: string; deliveries: number; attempts: number }[];
  timeline: { bucket_seconds: number; buckets: TimelineBucket[] };
  engagement: { channel: string; accepted: number; opened: number }[];
  lifecycle?: ReportLifecycle[];
  variants?: ReportVariant[];
};

export async function getReport(id: string, run?: number | null): Promise<Report> {
  return request<Report>(`/api/campaigns/${encodeURIComponent(id)}/report${run ? `?run=${run}` : ""}`);
}

export async function getDeliveryAttempts(id: string, delivery: string): Promise<DeliveryAttempt[]> {
  const path = `/api/campaigns/${encodeURIComponent(id)}/deliveries/${encodeURIComponent(delivery)}/attempts`;
  return (await request<{ attempts: DeliveryAttempt[] }>(path)).attempts;
}

export type LiveState = {
  status: string;
  progress: Progress;
  /** Channel alerts open on the campaign; absent from servers that predate them. */
  alerts?: ChannelAlert[];
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
  onLanguage?: (language: string) => void;
  onPlan?: (languages: string[]) => void;
  onLanguageDone?: (language: string) => void;
  onVariant?: (variant: string) => void;
};

// Streams a generation and resolves with the validated campaign. The streamed
// text is only a preview: what counts is the campaign in the final event.
export async function generateContent(
  id: string,
  input: { instruction?: string; channel?: string; missing_languages?: boolean },
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
        if (event === "language") handlers.onLanguage?.(payload.language);
        if (event === "plan") handlers.onPlan?.(payload.languages);
        if (event === "language_done") handlers.onLanguageDone?.(payload.language);
        if (event === "variant") handlers.onVariant?.(payload.variant);
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
