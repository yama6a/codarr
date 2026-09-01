import type { MediaFilter, MediaStatus, PlanKind, Provenance } from '../../api/types';

export interface LibraryFilterState {
  q: string;
  status: MediaStatus | '';
  plan_kind: PlanKind | '';
  video_codec: string;
  arr_instance_id: string;
  provenance: Provenance | '';
}

export const emptyFilters: LibraryFilterState = {
  q: '',
  status: '',
  plan_kind: '',
  video_codec: '',
  arr_instance_id: '',
  provenance: '',
};

/** toMediaFilter drops empty values so "select all matching filter" never widens the selection. */
export function toMediaFilter(state: LibraryFilterState): MediaFilter {
  const filter: MediaFilter = {};
  if (state.q) filter.q = state.q;
  if (state.status) filter.status = state.status;
  if (state.plan_kind) filter.plan_kind = state.plan_kind;
  if (state.video_codec) filter.video_codec = state.video_codec;
  if (state.arr_instance_id) filter.arr_instance_id = Number(state.arr_instance_id);
  if (state.provenance) filter.provenance = state.provenance;
  return filter;
}

export function describeFilter(state: LibraryFilterState): string {
  const parts: string[] = [];
  if (state.q) parts.push(`path contains "${state.q}"`);
  if (state.status) parts.push(`status ${state.status}`);
  if (state.plan_kind) parts.push(`plan ${state.plan_kind}`);
  if (state.video_codec) parts.push(`video codec ${state.video_codec}`);
  if (state.arr_instance_id) parts.push(`instance ${state.arr_instance_id}`);
  if (state.provenance) parts.push(`provenance ${state.provenance}`);
  return parts.length === 0 ? 'the whole library' : parts.join(', ');
}
