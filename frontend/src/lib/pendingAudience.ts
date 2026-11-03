/** A campaign just created from a brief, whose audience page should start looking for its audience. */
let pending = "";

export function requestAudience(campaignId: string) {
  pending = campaignId;
}

/** True once, so reopening a draft later never starts a model call by itself. */
export function takeAudienceRequest(campaignId: string): boolean {
  const asked = pending === campaignId;
  if (asked) pending = "";
  return asked;
}
