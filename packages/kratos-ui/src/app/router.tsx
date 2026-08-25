import { createBrowserRouter, Navigate } from 'react-router-dom';
import LoginPage from './pages/LoginPage';
import RegistrationPage from './pages/RegistrationPage';
import VerificationPage from './pages/VerificationPage';
import RecoveryPage from './pages/RecoveryPage';
import ErrorPage from './pages/ErrorPage';
import SettingsPage from './pages/SettingsPage';

export const router = createBrowserRouter([
  {
    path: '/auth',
    children: [
      { index: true, element: <Navigate to="/auth/login" replace /> },
      { path: 'login', element: <LoginPage /> },
      { path: 'registration', element: <RegistrationPage /> },
      { path: 'verification', element: <VerificationPage /> },
      { path: 'recovery', element: <RecoveryPage /> },
      { path: 'error', element: <ErrorPage /> },
    ],
  },
  {
    path: '/settings',
    element: <SettingsPage />,
  },
  {
    path: '*',
    element: <Navigate to="/auth/login" replace />,
  },
]);
