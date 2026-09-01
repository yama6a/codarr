import type { ArrInstance, Root } from '../../api/types';

export interface RootConflict {
  path: string;
  otherPath: string;
  owner: string;
  otherOwner: string;
}

function trimSlash(path: string): string {
  return path.length > 1 && path.endsWith('/') ? path.slice(0, -1) : path;
}

function overlaps(a: string, b: string): boolean {
  if (a === b) {
    return true;
  }
  return b.startsWith(`${a}/`) || a.startsWith(`${b}/`);
}

/**
 * findRootConflicts surfaces the case plan.md 16.2 refuses to guess about: two enabled instances
 * claiming the same tree. Codarr never reassigns an owner, so the UI has to say so.
 */
export function findRootConflicts(roots: Root[], instances: ArrInstance[]): RootConflict[] {
  const enabled = new Map(instances.filter((instance) => instance.enabled).map((i) => [i.id, i.name]));
  const owned = roots
    .filter((root) => root.enabled && root.arr_instance_id !== null && root.arr_instance_id !== undefined)
    .filter((root) => enabled.has(root.arr_instance_id as number));

  const conflicts: RootConflict[] = [];
  for (let i = 0; i < owned.length; i += 1) {
    for (let j = i + 1; j < owned.length; j += 1) {
      const a = owned[i];
      const b = owned[j];
      if (a.arr_instance_id === b.arr_instance_id) {
        continue;
      }
      if (overlaps(trimSlash(a.path), trimSlash(b.path))) {
        conflicts.push({
          path: a.path,
          otherPath: b.path,
          owner: enabled.get(a.arr_instance_id as number) ?? String(a.arr_instance_id),
          otherOwner: enabled.get(b.arr_instance_id as number) ?? String(b.arr_instance_id),
        });
      }
    }
  }
  return conflicts;
}
