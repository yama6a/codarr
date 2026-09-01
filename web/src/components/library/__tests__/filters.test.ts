import { describeFilter, emptyFilters, toMediaFilter } from '../filters';

describe('toMediaFilter', () => {
  it('drops empty values so select-all never widens the selection', () => {
    expect(toMediaFilter(emptyFilters)).toEqual({});
  });

  it('carries every set field, with the instance id as a number', () => {
    expect(
      toMediaFilter({
        q: 'arrival',
        status: 'done',
        plan_kind: 'full',
        video_codec: 'h264',
        arr_instance_id: '2',
        provenance: 'modified_since_transcode',
      }),
    ).toEqual({
      q: 'arrival',
      status: 'done',
      plan_kind: 'full',
      video_codec: 'h264',
      arr_instance_id: 2,
      provenance: 'modified_since_transcode',
    });
  });
});

describe('describeFilter', () => {
  it('names the whole library when nothing is set', () => {
    expect(describeFilter(emptyFilters)).toBe('the whole library');
  });

  it('lists the active clauses', () => {
    expect(describeFilter({ ...emptyFilters, plan_kind: 'remux', q: 'arrival' })).toBe(
      'path contains "arrival", plan remux',
    );
  });
});
