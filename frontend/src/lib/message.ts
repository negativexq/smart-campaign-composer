export const SUPPORTED_VARIABLES = ["first_name"] as const;

export type MessageValues = Record<(typeof SUPPORTED_VARIABLES)[number], string>;

const FALLBACKS: MessageValues = { first_name: "there" };
const VARIABLE = /\{\{\s*([a-zA-Z0-9_]+)\s*\}\}/g;

// Counts Unicode code points. The backend counts the same way, so a message
// never passes in the browser and then fails on the server.
export function countCharacters(text: string): number {
  return [...text].length;
}

export function renderMessage(template: string, values: MessageValues): string {
  return template.replace(VARIABLE, (placeholder, name: string) => {
    if (!isSupported(name)) return placeholder;
    return values[name] || FALLBACKS[name];
  });
}

export function findUnknownVariables(template: string): string[] {
  const names = [...template.matchAll(VARIABLE)].map((match) => match[1]);
  return [...new Set(names.filter((name) => !isSupported(name)))];
}

function isSupported(name: string): name is keyof MessageValues {
  return (SUPPORTED_VARIABLES as readonly string[]).includes(name);
}
