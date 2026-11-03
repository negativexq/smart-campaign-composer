export type SystemStatus = "operational" | "unavailable";

export async function fetchSystemStatus(): Promise<SystemStatus> {
  try {
    const response = await fetch("/readyz");
    return response.ok ? "operational" : "unavailable";
  } catch {
    return "unavailable";
  }
}
