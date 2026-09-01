import { lazy, Suspense } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import Sidebar from './components/layout/Sidebar';
import ErrorBoundary from './components/layout/ErrorBoundary';
import { LoadingSpinner } from './components/ui/LoadingSpinner';
import { ToastProvider } from './components/ui/Toast';

const Dashboard = lazy(() => import('./pages/Dashboard'));
const Library = lazy(() => import('./pages/Library'));
const Logs = lazy(() => import('./pages/Logs'));
const SettingsGeneral = lazy(() => import('./pages/settings/General'));
const SettingsPlex = lazy(() => import('./pages/settings/Plex'));
const SettingsArr = lazy(() => import('./pages/settings/Arr'));
const SettingsRoots = lazy(() => import('./pages/settings/Roots'));
const SettingsPolicy = lazy(() => import('./pages/settings/Policy'));
const SettingsHardware = lazy(() => import('./pages/settings/Hardware'));

export default function App() {
  return (
    <ToastProvider>
      <BrowserRouter>
        <div className="flex h-screen overflow-hidden bg-background-dark font-display text-slate-100">
          <Sidebar />
          <main className="h-full flex-1 overflow-y-auto">
            <ErrorBoundary>
              <Suspense fallback={<LoadingSpinner message="Loading page..." />}>
                <Routes>
                  <Route path="/" element={<Dashboard />} />
                  <Route path="/library" element={<Library />} />
                  <Route path="/logs" element={<Logs />} />
                  <Route path="/settings" element={<Navigate to="/settings/general" replace />} />
                  <Route path="/settings/general" element={<SettingsGeneral />} />
                  <Route path="/settings/plex" element={<SettingsPlex />} />
                  <Route path="/settings/arr" element={<SettingsArr />} />
                  <Route path="/settings/roots" element={<SettingsRoots />} />
                  <Route path="/settings/policy" element={<SettingsPolicy />} />
                  <Route path="/settings/hardware" element={<SettingsHardware />} />
                  <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
              </Suspense>
            </ErrorBoundary>
          </main>
        </div>
      </BrowserRouter>
    </ToastProvider>
  );
}
