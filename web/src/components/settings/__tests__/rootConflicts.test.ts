import { findRootConflicts } from '../rootConflicts';
import { arrInstance } from '../../../../test/fixtures';
import type { Root } from '../../../api/types';

function root(overrides: Partial<Root> = {}): Root {
  return {
    id: 1,
    path: '/media/movies',
    imported: true,
    enabled: true,
    arr_instance_id: 1,
    created_at: '2026-07-01T10:00:00.000000000Z',
    ...overrides,
  };
}

describe('findRootConflicts', () => {
  const radarr = arrInstance({ id: 1, name: 'radarr' });
  const radarr4k = arrInstance({ id: 2, name: 'radarr-4k' });

  it('reports the same path claimed by two enabled instances', () => {
    const conflicts = findRootConflicts(
      [root({ id: 1, arr_instance_id: 1 }), root({ id: 2, arr_instance_id: 2 })],
      [radarr, radarr4k],
    );

    expect(conflicts).toEqual([
      { path: '/media/movies', otherPath: '/media/movies', owner: 'radarr', otherOwner: 'radarr-4k' },
    ]);
  });

  it('reports a nested root owned by a different instance', () => {
    const conflicts = findRootConflicts(
      [root({ id: 1, arr_instance_id: 1 }), root({ id: 2, path: '/media/movies/4k', arr_instance_id: 2 })],
      [radarr, radarr4k],
    );

    expect(conflicts).toEqual([
      { path: '/media/movies', otherPath: '/media/movies/4k', owner: 'radarr', otherOwner: 'radarr-4k' },
    ]);
  });

  it('ignores a sibling path and a disabled instance', () => {
    expect(
      findRootConflicts(
        [root({ id: 1, arr_instance_id: 1 }), root({ id: 2, path: '/media/movies-4k', arr_instance_id: 2 })],
        [radarr, radarr4k],
      ),
    ).toEqual([]);

    expect(
      findRootConflicts(
        [root({ id: 1, arr_instance_id: 1 }), root({ id: 2, arr_instance_id: 2 })],
        [radarr, arrInstance({ id: 2, name: 'radarr-4k', enabled: false })],
      ),
    ).toEqual([]);
  });

  it('ignores two roots owned by the same instance', () => {
    expect(
      findRootConflicts(
        [root({ id: 1, arr_instance_id: 1 }), root({ id: 2, path: '/media/movies/4k', arr_instance_id: 1 })],
        [radarr],
      ),
    ).toEqual([]);
  });
});
