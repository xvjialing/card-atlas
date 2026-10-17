import type { Filter, Summary, Job, FilePage, Plan, Thumbnail } from "./types";

export const isDesktop = Boolean(
  (window as unknown as { _wails?: { environment?: unknown } })._wails
    ?.environment,
);
const service = () => import("../../bindings/cardatlas/mediaservice");
export const api = {
  choose: async (output: boolean) => (await service()).ChooseDirectory(output),
  scan: async (path: string) =>
    (await (await service()).StartScan(path)) as Job,
  status: async () => (await (await service()).GetStatus()) as Job,
  summary: async () => (await (await service()).GetSummary()) as Summary,
  query: async (q: Filter) =>
    (await (await service()).QueryFiles(q)) as FilePage,
  ids: async (q: Filter) => (await (await service()).MatchingIDs(q)) ?? [],
  plan: async (ids: string[]) =>
    (await (await service()).PrepareExport(ids)) as Plan,
  export: async (path: string, ids: string[]) =>
    (await (await service()).StartExport(path, ids)) as Job,
  cancel: async () => (await service()).CancelJob(),
  history: async () => ((await (await service()).GetHistory()) ?? []) as Job[],
  thumbnail: async (id: string) =>
    (await (await service()).GetThumbnail(id)) as Thumbnail,
};
