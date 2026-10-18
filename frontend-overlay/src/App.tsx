import { useEffect, useState } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuthProvider } from './contexts/AuthContext';
import { TenantProvider } from './contexts/TenantContext';
import { BrandingProvider } from './contexts/BrandingContext';
import Layout from './components/Layout';
import AdminLayout from './components/AdminLayout';
import ProtectedRoute from './components/ProtectedRoute';
import BrandingThemeInjector from './components/BrandingThemeInjector';
import LoadingSpinner from './components/LoadingSpinner';
import { bootstrapApi } from './api/client';

// Auth pages
import LoginPage from './pages/auth/LoginPage';
import SignupPage from './pages/auth/SignupPage';
import VerifyEmailPage from './pages/auth/VerifyEmailPage';
import ForgotPasswordPage from './pages/auth/ForgotPasswordPage';
import ResetPasswordPage from './pages/auth/ResetPasswordPage';
import AuthCallbackPage from './pages/auth/AuthCallbackPage';
import BootstrapPage from './pages/BootstrapPage';

// App pages (flipbook-customized)
import DashboardPage from './pages/app/DashboardPage';
import FlipbooksPage from './pages/app/FlipbooksPage';
import FlipbookUpload from './pages/app/FlipbookUpload';
import FlipbookDetail from './pages/app/FlipbookDetail';
import TeamPage from './pages/app/TeamPage';
import SettingsPage from './pages/app/SettingsPage';
import PlanPage from './pages/app/PlanPage';
import BuyCreditsPage from './pages/app/BuyCreditsPage';
import BillingSuccessPage from './pages/app/BillingSuccessPage';
import BillingCancelPage from './pages/app/BillingCancelPage';
import TestEntitlementsPage from './pages/app/TestEntitlementsPage';

// Admin pages
import AdminDashboardPage from './pages/admin/DashboardPage';
import AdminMessagesPage from './pages/admin/MessagesPage';
import AdminUsersPage from './pages/admin/UsersPage';
import AdminTenantsPage from './pages/admin/TenantsPage';
import AdminLogsPage from './pages/admin/LogsPage';
import AdminConfigPage from './pages/admin/ConfigPage';
import AdminUserProfilePage from './pages/admin/UserProfilePage';
import AdminAboutPage from './pages/admin/AboutPage';
import AdminPlansPage from './pages/admin/PlansPage';
import AdminTenantProfilePage from './pages/admin/TenantProfilePage';
import AdminHealthPage from './pages/admin/HealthPage';
import AdminFinancialPage from './pages/admin/FinancialPage';
import AdminAPIPage from './pages/admin/APIPage';
import AdminBrandingPage from './pages/admin/BrandingPage';
import AdminRootMembersPage from './pages/admin/RootMembersPage';
import AdminPromotionsPage from './pages/admin/PromotionsPage';
import AdminAnnouncementsPage from './pages/admin/AnnouncementsPage';
import AdminPMPage from './pages/admin/PMPage';

// Public pages
import LandingPage from './pages/public/LandingPage';
import CustomPage from './pages/public/CustomPage';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 1000 * 60,
      retry: 1,
    },
  },
});

function ScrollToTop() {
  const { pathname } = useLocation();
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [pathname]);
  return null;
}

function BootstrapGuard({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<'loading' | 'initialized' | 'needs-setup'>('loading');

  useEffect(() => {
    bootstrapApi.status()
      .then((data) => setStatus(data.initialized ? 'initialized' : 'needs-setup'))
      .catch(() => setStatus('initialized'));
  }, []);

  if (status === 'loading') {
    return (
      <div className="min-h-screen bg-dark-950 flex items-center justify-center">
        <LoadingSpinner size="lg" />
      </div>
    );
  }

  if (status === 'needs-setup') {
    return (
      <BrowserRouter>
        <Routes>
          <Route path="/setup" element={<BootstrapPage />} />
          <Route path="*" element={<Navigate to="/setup" replace />} />
        </Routes>
      </BrowserRouter>
    );
  }

  return <>{children}</>;
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BootstrapGuard>
        <BrandingProvider>
          <AuthProvider>
            <TenantProvider>
              <BrowserRouter>
                <ScrollToTop />
                <BrandingThemeInjector />
                <Routes>
                  {/* Public landing page */}
                  <Route path="/" element={<LandingPage />} />

                  {/* Public custom pages */}
                  <Route path="/p/:slug" element={<CustomPage />} />

                  {/* Public auth routes */}
                  <Route path="/login" element={<LoginPage />} />
                  <Route path="/signup" element={<SignupPage />} />
                  <Route path="/verify-email" element={<VerifyEmailPage />} />
                  <Route path="/forgot-password" element={<ForgotPasswordPage />} />
                  <Route path="/reset-password" element={<ResetPasswordPage />} />
                  <Route path="/auth/callback" element={<AuthCallbackPage />} />

                  {/* Protected app routes */}
                  <Route element={<ProtectedRoute />}>
                    <Route element={<Layout />}>
                      <Route path="/dashboard" element={<DashboardPage />} />
                      <Route path="/flipbooks" element={<FlipbooksPage />} />
                      <Route path="/flipbooks/upload" element={<FlipbookUpload />} />
                      <Route path="/flipbooks/:id" element={<FlipbookDetail />} />
                      <Route path="/team" element={<TeamPage />} />
                      <Route path="/plan" element={<PlanPage />} />
                      <Route path="/buy-credits" element={<BuyCreditsPage />} />
                      <Route path="/billing/success" element={<BillingSuccessPage />} />
                      <Route path="/billing/cancel" element={<BillingCancelPage />} />
                      <Route path="/settings" element={<SettingsPage />} />
                      <Route path="/test-entitlements" element={<TestEntitlementsPage />} />
                      <Route path="/messages" element={<AdminMessagesPage />} />
                    </Route>

                    {/* Admin routes (root tenant only, enforced by AdminLayout) */}
                    <Route path="/last" element={<AdminLayout />}>
                      <Route index element={<AdminDashboardPage />} />
                      <Route path="messages" element={<AdminMessagesPage />} />
                      <Route path="users" element={<AdminUsersPage />} />
                      <Route path="users/:userId" element={<AdminUserProfilePage />} />
                      <Route path="tenants" element={<AdminTenantsPage />} />
                      <Route path="tenants/:tenantId" element={<AdminTenantProfilePage />} />
                      <Route path="members" element={<AdminRootMembersPage />} />
                      <Route path="plans" element={<AdminPlansPage />} />
                      <Route path="financial" element={<AdminFinancialPage />} />
                      <Route path="pm" element={<AdminPMPage />} />
                      <Route path="promotions" element={<AdminPromotionsPage />} />
                      <Route path="announcements" element={<AdminAnnouncementsPage />} />
                      <Route path="health" element={<AdminHealthPage />} />
                      <Route path="logs" element={<AdminLogsPage />} />
                      <Route path="config" element={<AdminConfigPage />} />
                      <Route path="api" element={<AdminAPIPage />} />
                      <Route path="branding" element={<AdminBrandingPage />} />
                      <Route path="about" element={<AdminAboutPage />} />
                    </Route>
                  </Route>

                  {/* Fallback */}
                  <Route path="*" element={<Navigate to="/dashboard" replace />} />
                </Routes>
              </BrowserRouter>
            </TenantProvider>
          </AuthProvider>
        </BrandingProvider>
      </BootstrapGuard>
    </QueryClientProvider>
  );
}
