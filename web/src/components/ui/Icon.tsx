import {
  AlertTriangle,
  Check,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  Cpu,
  Film,
  FolderOpen,
  Gavel,
  Info,
  Inbox,
  LayoutDashboard,
  Library,
  Network,
  PlayCircle,
  ScrollText,
  Search,
  SlidersHorizontal,
  X,
  type LucideIcon,
} from 'lucide-react';

// Named rather than passing components around, so call sites stay
// `<Icon name="folder" />` and every icon the binary embeds is visible in one
// place. lucide tree-shakes on the named imports above; a font would have put
// its whole glyph set into the Go binary via go:embed.
const icons = {
  alert: AlertTriangle,
  check: Check,
  chevron_left: ChevronLeft,
  chevron_right: ChevronRight,
  close: X,
  dashboard: LayoutDashboard,
  error: CircleAlert,
  folder: FolderOpen,
  gavel: Gavel,
  hardware: Cpu,
  hub: Network,
  inbox: Inbox,
  info: Info,
  library: Library,
  logo: Film,
  logs: ScrollText,
  plex: PlayCircle,
  search: Search,
  success: CircleCheck,
  tune: SlidersHorizontal,
} satisfies Record<string, LucideIcon>;

export type IconName = keyof typeof icons;

interface IconProps {
  name: IconName;
  size?: number;
  className?: string;
}

export function Icon({ name, size = 20, className }: IconProps) {
  const Glyph = icons[name];

  return <Glyph size={size} strokeWidth={2} className={className} aria-hidden="true" />;
}
