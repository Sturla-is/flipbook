import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { BookOpen, Upload, BarChart3, ArrowRight, ExternalLink } from 'lucide-react';
import { flipbookApi, type FlipbookItem, type UsageInfo } from '../../api/flipbook';
import { useTenant } from '../../contexts/TenantContext';
import LoadingSpinner from '../../components/LoadingSpinner';

export default function DashboardPage() {
  const navigate = useNavigate();
  const { activeTenant } = useTenant();
  const [flipbooks, setFlipbooks] = useState<FlipbookItem[]>([]);
  const [usage, setUsage] = useState<UsageInfo | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    Promise.all([
      flipbookApi.list().then((d) => setFlipbooks(d.flipbooks || [])),
      flipbookApi.usage().then(setUsage),
    ])
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  // Redirect to plan page if user has no active paid plan (page limit = 0)
  useEffect(() => {
    if (!loading && usage && usage.pagesLimit === 0) {
      navigate('/plan', { replace: true });
    }
  }, [loading, usage, navigate]);

  if (loading) return <LoadingSpinner size="lg" className="py-20" />;

  const recent = flipbooks.slice(0, 6);
  const usagePct = usage && usage.pagesLimit > 0
    ? Math.min(100, (usage.pagesUsed / usage.pagesLimit) * 100)
    : 0;

  return (
    <div>
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-white">
          Welcome{activeTenant ? `, ${activeTenant.tenantName}` : ''}
        </h1>
        <p className="text-dark-400 mt-1">Here's an overview of your flipbooks.</p>
      </div>

      {/* Stats cards */}
      <div className="grid gap-4 grid-cols-1 md:grid-cols-3 mb-8">
        <div className="bg-dark-900/50 border border-dark-800 rounded-xl p-5">
          <div className="flex items-center gap-3 mb-3">
            <div className="p-2 bg-primary-500/10 rounded-lg">
              <BookOpen className="w-5 h-5 text-primary-400" />
            </div>
            <span className="text-dark-400 text-sm">Total Flipbooks</span>
          </div>
          <p className="text-3xl font-bold text-white">{flipbooks.length}</p>
        </div>

        <div className="bg-dark-900/50 border border-dark-800 rounded-xl p-5">
          <div className="flex items-center gap-3 mb-3">
            <div className="p-2 bg-accent-emerald/10 rounded-lg">
              <BarChart3 className="w-5 h-5 text-accent-emerald" />
            </div>
            <span className="text-dark-400 text-sm">Page Usage</span>
          </div>
          {usage && (
            <>
              <p className="text-3xl font-bold text-white">
                {usage.pagesUsed}<span className="text-lg text-dark-500">/{usage.pagesLimit}</span>
              </p>
              <div className="mt-2 h-1.5 bg-dark-800 rounded-full overflow-hidden">
                <div
                  className={`h-full rounded-full ${usagePct >= 90 ? 'bg-red-500' : 'bg-accent-emerald'}`}
                  style={{ width: `${usagePct}%` }}
                />
              </div>
            </>
          )}
        </div>

        <div className="bg-dark-900/50 border border-dark-800 rounded-xl p-5">
          <div className="flex items-center gap-3 mb-3">
            <div className="p-2 bg-accent-purple/10 rounded-lg">
              <Upload className="w-5 h-5 text-accent-purple" />
            </div>
            <span className="text-dark-400 text-sm">Plan</span>
          </div>
          <p className="text-xl font-bold text-white">{usage?.planName || 'Free'}</p>
          <Link to="/plan" className="text-sm text-primary-400 hover:text-primary-300 mt-1 inline-block">
            Manage plan <ArrowRight className="w-3 h-3 inline" />
          </Link>
        </div>
      </div>

      {/* Quick upload */}
      <Link
        to="/flipbooks/upload"
        className="block bg-gradient-to-r from-primary-500/10 to-accent-purple/10 border border-primary-500/20 rounded-xl p-6 mb-8 hover:border-primary-500/40 transition-colors"
      >
        <div className="flex items-center gap-4">
          <div className="p-3 bg-primary-500/20 rounded-xl">
            <Upload className="w-6 h-6 text-primary-400" />
          </div>
          <div>
            <h3 className="text-white font-semibold">Upload a new flipbook</h3>
            <p className="text-dark-400 text-sm">Upload a PowerPoint or PDF to create an interactive page-flip experience.</p>
          </div>
          <ArrowRight className="w-5 h-5 text-primary-400 ml-auto" />
        </div>
      </Link>

      {/* Recent flipbooks */}
      {recent.length > 0 && (
        <div>
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-semibold text-white">Recent Flipbooks</h2>
            <Link to="/flipbooks" className="text-sm text-primary-400 hover:text-primary-300">
              View all <ArrowRight className="w-3 h-3 inline" />
            </Link>
          </div>
          <div className="grid gap-3 grid-cols-1 md:grid-cols-2 lg:grid-cols-3">
            {recent.map((fb) => (
              <Link
                key={fb.id}
                to={`/flipbooks/${fb.id}`}
                className="bg-dark-900/50 border border-dark-800 rounded-xl p-4 hover:border-dark-700 transition-colors"
              >
                <div className="flex items-center justify-between">
                  <div className="min-w-0 flex-1">
                    <p className="text-white font-medium truncate">{fb.title}</p>
                    <p className="text-dark-500 text-xs mt-1">
                      {fb.pageCount > 0 ? `${fb.pageCount} pages` : fb.status} &middot;{' '}
                      {new Date(fb.createdAt).toLocaleDateString()}
                    </p>
                  </div>
                  {fb.status === 'ready' && (
                    <ExternalLink className="w-4 h-4 text-dark-500 flex-shrink-0 ml-2" />
                  )}
                </div>
              </Link>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
