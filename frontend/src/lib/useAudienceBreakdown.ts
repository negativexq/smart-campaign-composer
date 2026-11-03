import { ref, watch } from "vue";

import { getAudienceBreakdown, type AudienceBreakdown, type Campaign } from "../api/campaigns";

/** Loads how a campaign's conditions narrow its audience, again whenever the audience changes. */
export function useAudienceBreakdown(campaign: () => Campaign | null) {
  const breakdown = ref<AudienceBreakdown | null>(null);
  const failed = ref(false);

  const key = () => {
    const current = campaign();
    return current?.proposal ? `${current.id} ${current.audience_as_of} ${current.approved_at}` : "";
  };

  watch(
    key,
    async (asked) => {
      breakdown.value = null;
      failed.value = false;
      if (!asked) return;
      try {
        const loaded = await getAudienceBreakdown(campaign()!.id);
        if (asked === key()) breakdown.value = loaded;
      } catch {
        if (asked === key()) failed.value = true;
      }
    },
    { immediate: true },
  );

  return { breakdown, failed };
}
