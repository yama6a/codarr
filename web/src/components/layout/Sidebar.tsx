import { NavLink } from 'react-router-dom';
import { Icon, type IconName } from '../ui/Icon';

interface NavItem {
  to: string;
  label: string;
  icon: IconName;
  end?: boolean;
}

const main: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: 'dashboard', end: true },
  { to: '/library', label: 'Library', icon: 'library', end: false },
  { to: '/logs', label: 'Logs', icon: 'logs', end: false },
];

const settings: NavItem[] = [
  { to: '/settings/general', label: 'General', icon: 'tune' },
  { to: '/settings/plex', label: 'Plex', icon: 'plex' },
  { to: '/settings/arr', label: 'Radarr & Sonarr', icon: 'hub' },
  { to: '/settings/roots', label: 'Roots', icon: 'folder' },
  { to: '/settings/policy', label: 'Policy', icon: 'gavel' },
  { to: '/settings/hardware', label: 'Hardware', icon: 'hardware' },
];

const linkClasses = ({ isActive }: { isActive: boolean }) =>
  `flex items-center gap-3 rounded-lg px-3 py-2.5 transition-colors ${
    isActive ? 'bg-primary/20 text-blue-300' : 'text-gray-400 hover:bg-gray-800'
  }`;

export default function Sidebar() {
  return (
    <aside className="flex h-screen w-64 flex-shrink-0 flex-col border-r border-gray-800 bg-sidebar-dark">
      <div className="flex h-full flex-col gap-6 p-4">
        <div className="flex items-center gap-3 px-2">
          <Icon name="logo" size={24} className="text-primary" />
          <div className="flex flex-col">
            <h1 className="text-base font-bold tracking-tight text-white">Codarr</h1>
            <p className="text-xs text-gray-400">Transcode manager</p>
          </div>
        </div>

        <nav className="flex flex-1 flex-col gap-1 overflow-y-auto">
          {main.map((item) => (
            <NavLink key={item.to} to={item.to} end={item.end} className={linkClasses}>
              <Icon name={item.icon} size={20} />
              <span className="text-sm font-medium">{item.label}</span>
            </NavLink>
          ))}

          <div className="mt-4 mb-2 px-3">
            <span className="text-[10px] font-bold tracking-wider text-gray-500 uppercase">Settings</span>
          </div>
          {settings.map((item) => (
            <NavLink key={item.to} to={item.to} className={linkClasses}>
              <Icon name={item.icon} size={20} />
              <span className="text-sm font-medium">{item.label}</span>
            </NavLink>
          ))}
        </nav>
      </div>
    </aside>
  );
}
