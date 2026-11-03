import { ApiError, errorMessage, request } from "./campaigns";

type Session = { identity: string | null };

export async function fetchSession(): Promise<string | null> {
  return (await request<Session>("/api/session")).identity;
}

export async function createSession(identity: string): Promise<string | null> {
  const session = await request<Session>("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ identity }),
  });
  return session.identity;
}

export async function endSession(): Promise<void> {
  await request<unknown>("/api/session", { method: "DELETE" });
}

export { ApiError, errorMessage };
