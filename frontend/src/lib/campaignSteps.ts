const CHANNEL_LABELS: Record<string, string> = { sms: "SMS", email: "email" };

/** The steps of setting up a campaign. Each channel it sends on has its own. */
export function wizardSteps(channels: string[]) {
  return [
    { id: "brief", label: "Brief" },
    { id: "audience", label: "Audience" },
    ...(channels.includes("sms") ? [{ id: "sms", label: "SMS" }] : []),
    ...(channels.includes("email") ? [{ id: "email", label: "Email" }] : []),
    { id: "review", label: "Review" },
  ];
}

export function channelLabel(channel: string) {
  return CHANNEL_LABELS[channel] ?? channel;
}

export function firstChannel(channels: string[]) {
  return channels.includes("sms") ? "sms" : "email";
}

export function lastChannel(channels: string[]) {
  return channels.includes("email") ? "email" : "sms";
}

export function firstMessagePath(id: string, channels: string[]) {
  return `/campaigns/${id}/${firstChannel(channels)}`;
}

export function lastMessagePath(id: string, channels: string[]) {
  return `/campaigns/${id}/${lastChannel(channels)}`;
}
