import { useEffect, useState } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { ArrowLeft, ExternalLink, Copy, Trash2, CheckCircle } from 'lucide-react';
import { flipbookApi, type FlipbookDetail as FlipbookDetailType } from '../../api/flipbook';
import LoadingSpinner from '../../components/LoadingSpinner';

export default function FlipbookDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [fb, setFb] = useState<FlipbookDetailType | null>(null);
  const [loading, setLoading] = useState(true);
  const [copied, setCopied] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');

  useEffect(() => {
    if (!id) return;
    flipbookApi.get(id)
      .then((data) => {
        setFb(data);
        setTitle(data.title);
        setDescription('');
      })
      .catch(() => navigate('/flipbooks'))
      .finally(() => setLoading(false));
  }, [id, navigate]);

  const copyToClipboard = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    setCopied(label);
    setTimeout(() => setCopied(null), 2000);
  };

  const handleDelete = async () => {
    if (!fb || !confirm(`Delete "${fb.title}"? This cannot be undone.`)) return;
    setDeleting(true);
    try {
      await flipbookApi.delete(fb.id);
      navigate('/flipbooks');
    } catch {
      setDeleting(false);
    }
  };

  const handleSave = async () => {
    if (!fb) return;
    try {
      await flipbookApi.update(fb.id, { title, description });
      setFb({ ...fb, title });
      setEditing(false);
    } catch {
      // handled by interceptor
    }
  };

  if (loading) return <LoadingSpinner size="lg" className="py-20" />;
  if (!fb) return null;

  return (
    <div>
      <Link to="/flipbooks" className="inline-flex items-center gap-1 text-dark-400 hover:text-dark-300 text-sm mb-6">
        <ArrowLeft className="w-4 h-4" /> Back to Flipbooks
      </Link>

      <div className="flex items-start justify-between mb-8">
        <div>
          {editing ? (
            <div className="flex items-center gap-3">
              <input
                type="text"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                className="text-2xl font-bold bg-dark-900 border border-dark-700 rounded-lg px-3 py-1 text-white focus:border-primary-500 focus:outline-none"
              />
              <button onClick={handleSave} className="text-sm text-primary-400 hover:text-primary-300">Save</button>
              <button onClick={() => { setEditing(false); setTitle(fb.title); }} className="text-sm text-dark-400">Cancel</button>
            </div>
          ) : (
            <h1
              className="text-2xl font-bold text-white cursor-pointer hover:text-primary-400 transition-colors"
              onClick={() => setEditing(true)}
              title="Click to edit"
            >
              {fb.title}
            </h1>
          )}
          <p className="text-dark-400 mt-1">
            {fb.pageCount} pages &middot; {fb.status === 'ready' ? 'Live' : fb.status}
          </p>
        </div>
        <button
          onClick={handleDelete}
          disabled={deleting}
          className="flex items-center gap-2 px-4 py-2 text-sm text-red-400 border border-red-500/20 rounded-lg hover:bg-red-500/10 transition-colors"
        >
          {deleting ? <LoadingSpinner size="sm" /> : <><Trash2 className="w-4 h-4" /> Delete</>}
        </button>
      </div>

      {/* Actions */}
      <div className="grid gap-4 md:grid-cols-3 mb-8">
        <div className="bg-dark-900/50 border border-dark-800 rounded-xl p-4">
          <h3 className="text-sm font-medium text-dark-400 mb-2">Viewer Link</h3>
          <div className="flex items-center gap-2">
            <a href={fb.viewerUrl} target="_blank" rel="noopener noreferrer" className="flex-1 text-sm text-primary-400 truncate hover:text-primary-300 font-mono">{fb.viewerUrl}</a>
            <button
              onClick={() => copyToClipboard(fb.viewerUrl, 'viewer')}
              className="text-dark-400 hover:text-white"
            >
              {copied === 'viewer' ? <CheckCircle className="w-4 h-4 text-accent-emerald" /> : <Copy className="w-4 h-4" />}
            </button>
            <a href={fb.viewerUrl} target="_blank" rel="noopener noreferrer" className="text-dark-400 hover:text-white">
              <ExternalLink className="w-4 h-4" />
            </a>
          </div>
        </div>

        <div className="bg-dark-900/50 border border-dark-800 rounded-xl p-4">
          <h3 className="text-sm font-medium text-dark-400 mb-2">Embed Code</h3>
          <div className="flex items-center gap-2">
            <code className="flex-1 text-xs text-dark-300 truncate">{fb.embedCode}</code>
            <button
              onClick={() => copyToClipboard(fb.embedCode, 'embed')}
              className="text-dark-400 hover:text-white"
            >
              {copied === 'embed' ? <CheckCircle className="w-4 h-4 text-accent-emerald" /> : <Copy className="w-4 h-4" />}
            </button>
          </div>
        </div>

        <div className="bg-dark-900/50 border border-dark-800 rounded-xl p-4">
          <h3 className="text-sm font-medium text-dark-400 mb-2">Embed Link</h3>
          <div className="flex items-center gap-2">
            <code className="flex-1 text-sm text-primary-400 truncate">{fb.embedUrl}</code>
            <button
              onClick={() => copyToClipboard(fb.embedUrl, 'embedUrl')}
              className="text-dark-400 hover:text-white"
            >
              {copied === 'embedUrl' ? <CheckCircle className="w-4 h-4 text-accent-emerald" /> : <Copy className="w-4 h-4" />}
            </button>
          </div>
        </div>
      </div>

      {/* Embedded preview */}
      {fb.status === 'ready' && fb.embedUrl && (
        <div className="mb-8">
          <h2 className="text-lg font-semibold text-white mb-4">Preview</h2>
          <div className="bg-dark-900 border border-dark-800 rounded-xl overflow-hidden" style={{ aspectRatio: `${fb.width || 16} / ${fb.height || 9}`, maxHeight: '600px' }}>
            <iframe
              src={fb.embedUrl}
              className="w-full h-full border-0"
              allowFullScreen
              title={fb.title}
            />
          </div>
        </div>
      )}

      {/* Thumbnail grid */}
      {fb.pages && fb.pages.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-white mb-4">Pages</h2>
          <div className="grid grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-3">
            {fb.pages.map((url, i) => (
              <div key={i} className="relative bg-dark-900 border border-dark-800 rounded-lg overflow-hidden aspect-[4/3]">
                <img
                  src={url}
                  alt={`Page ${i + 1}`}
                  className="w-full h-full object-cover"
                  loading="lazy"
                />
                <div className="absolute bottom-0 left-0 right-0 bg-black/60 text-center text-xs text-dark-300 py-1">
                  {i + 1}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
