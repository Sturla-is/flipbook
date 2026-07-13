import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { BookOpen, Plus, ExternalLink, Code, Trash2, AlertCircle } from 'lucide-react';
import { flipbookApi, type FlipbookItem } from '../../api/flipbook';
import LoadingSpinner from '../../components/LoadingSpinner';

function statusBadge(status: string) {
  const map: Record<string, { label: string; cls: string }> = {
    ready: { label: 'Ready', cls: 'bg-accent-emerald/10 text-accent-emerald' },
    pending: { label: 'Pending', cls: 'bg-yellow-500/10 text-yellow-400' },
    converting: { label: 'Converting', cls: 'bg-blue-500/10 text-blue-400' },
    error: { label: 'Error', cls: 'bg-red-500/10 text-red-400' },
    regenerating: { label: 'Regenerating', cls: 'bg-blue-500/10 text-blue-400' },
  };
  const s = map[status] || { label: status, cls: 'bg-dark-800 text-dark-400' };
  return (
    <span className={`px-2 py-0.5 text-xs font-medium rounded-full ${s.cls}`}>
      {s.label}
    </span>
  );
}

export default function FlipbooksPage() {
  const [flipbooks, setFlipbooks] = useState<FlipbookItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleting, setDeleting] = useState<string | null>(null);

  const load = () => {
    flipbookApi.list()
      .then((d) => setFlipbooks(d.flipbooks || []))
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => { load(); }, []);

  const handleDelete = async (id: string, title: string) => {
    if (!confirm(`Delete "${title}"? This cannot be undone.`)) return;
    setDeleting(id);
    try {
      await flipbookApi.delete(id);
      setFlipbooks((prev) => prev.filter((f) => f.id !== id));
    } catch {
      // handled by interceptor
    } finally {
      setDeleting(null);
    }
  };

  if (loading) return <LoadingSpinner size="lg" className="py-20" />;

  return (
    <div>
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-white flex items-center gap-3">
            <BookOpen className="w-7 h-7 text-primary-400" />
            My Flipbooks
          </h1>
          <p className="text-dark-400 mt-1">Manage your interactive presentations</p>
        </div>
        <Link
          to="/flipbooks/upload"
          className="flex items-center gap-2 px-4 py-2.5 bg-primary-500 text-white rounded-lg hover:bg-primary-600 transition-colors font-medium text-sm"
        >
          <Plus className="w-4 h-4" /> Upload
        </Link>
      </div>

      {flipbooks.length === 0 ? (
        <div className="text-center py-20">
          <BookOpen className="w-16 h-16 text-dark-700 mx-auto mb-4" />
          <h2 className="text-xl font-semibold text-white mb-2">No flipbooks yet</h2>
          <p className="text-dark-400 mb-6">Upload a PowerPoint or PDF to create your first flipbook.</p>
          <Link
            to="/flipbooks/upload"
            className="inline-flex items-center gap-2 px-6 py-3 bg-primary-500 text-white rounded-lg hover:bg-primary-600 font-medium"
          >
            <Plus className="w-5 h-5" /> Upload your first flipbook
          </Link>
        </div>
      ) : (
        <div className="grid gap-4 grid-cols-1 md:grid-cols-2 lg:grid-cols-3">
          {flipbooks.map((fb) => (
            <div
              key={fb.id}
              className="bg-dark-900/50 border border-dark-800 rounded-xl overflow-hidden hover:border-dark-700 transition-colors"
            >
              {/* Thumbnail */}
              {fb.thumbnailUrl && fb.status === 'ready' ? (
                <a href={fb.viewerUrl} target="_blank" rel="noopener noreferrer" className="block">
                  <div className="aspect-[4/3] bg-dark-900 overflow-hidden">
                    <img
                      src={fb.thumbnailUrl}
                      alt={fb.title}
                      className="w-full h-full object-cover hover:scale-105 transition-transform duration-300"
                      loading="lazy"
                    />
                  </div>
                </a>
              ) : (
                <div className="aspect-[4/3] bg-dark-900 flex items-center justify-center">
                  <BookOpen className="w-12 h-12 text-dark-700" />
                </div>
              )}

              <div className="p-5">
                <div className="flex items-start justify-between mb-3">
                  <div className="min-w-0 flex-1">
                    <Link
                      to={`/flipbooks/${fb.id}`}
                      className="text-white font-semibold hover:text-primary-400 transition-colors truncate block"
                    >
                      {fb.title}
                    </Link>
                    <p className="text-dark-500 text-xs mt-1">
                      {new Date(fb.createdAt).toLocaleDateString()}
                    </p>
                  </div>
                  {statusBadge(fb.status)}
                </div>

                <div className="text-sm text-dark-400 mb-4">
                  {fb.pageCount > 0 ? `${fb.pageCount} pages` : 'Processing...'}
                </div>

                <div className="flex items-center gap-2 text-sm">
                  {fb.status === 'ready' && (
                    <>
                      <a
                        href={fb.viewerUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex items-center gap-1 text-primary-400 hover:text-primary-300"
                      >
                        <ExternalLink className="w-3.5 h-3.5" /> View
                      </a>
                      <span className="text-dark-700">|</span>
                      <button
                        onClick={() => navigator.clipboard.writeText(fb.viewerUrl)}
                        className="text-dark-400 hover:text-dark-300"
                      >
                        <Code className="w-3.5 h-3.5 inline mr-1" />Copy link
                      </button>
                    </>
                  )}
                  {fb.status === 'error' && (
                    <span className="flex items-center gap-1 text-red-400">
                      <AlertCircle className="w-3.5 h-3.5" /> Conversion failed
                    </span>
                  )}
                  <div className="flex-1" />
                  <button
                    onClick={() => handleDelete(fb.id, fb.title)}
                    disabled={deleting === fb.id}
                    className="text-dark-500 hover:text-red-400 transition-colors"
                    title="Delete"
                  >
                    {deleting === fb.id ? <LoadingSpinner size="sm" /> : <Trash2 className="w-4 h-4" />}
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
