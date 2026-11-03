export type SystemStatus = "operational" | "degraded" | "unavailable";

export type SystemHealth = {
  status: SystemStatus;
  checks: Record<string, string>;
};

// Without the API or the database nothing works. Anything else only holds up sending.
export async function fetchSystemHealth(): Promise<SystemHealth> {
  try {
    const response = await fetch("/api/system");
    if (!response.ok) return { status: "unavailable", checks: {} };
    const { checks } = (await response.json()) as { checks: Record<string, string> };
    if (checks.database === "down") return { status: "unavailable", checks };
    return { status: Object.values(checks).includes("down") ? "degraded" : "operational", checks };
  } catch {
    return { status: "unavailable", checks: {} };
  }
}
