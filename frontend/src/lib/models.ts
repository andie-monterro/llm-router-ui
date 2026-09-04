export function filterModels(models: string[], query: string): string[] {
  const needle = query.trim().toLowerCase();
  return [...new Set(models)]
    .filter((model) => !needle || model.toLowerCase().includes(needle))
    .sort((a, b) => (a === 'auto' ? -1 : b === 'auto' ? 1 : a.localeCompare(b)));
}
