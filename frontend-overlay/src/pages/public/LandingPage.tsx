import { Navigate, Link } from 'react-router-dom';
import { useAuth } from '../../contexts/AuthContext';
import { BookOpen, Zap, Globe, Server, ArrowRight, ExternalLink, Check } from 'lucide-react';

export default function LandingPage() {
  const { isAuthenticated } = useAuth();

  if (isAuthenticated) {
    return <Navigate to="/dashboard" replace />;
  }

  return (
    <div className="min-h-screen bg-dark-950 flex flex-col">
      {/* Nav */}
      <header className="border-b border-dark-800/50">
        <div className="max-w-6xl mx-auto px-6 py-4 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <BookOpen className="w-7 h-7 text-primary-400" />
            <span className="text-xl font-bold text-white">Flipbook</span>
          </div>
          <div className="flex items-center gap-4">
            <Link to="/login" className="text-sm text-dark-300 hover:text-white transition-colors">
              Log in
            </Link>
            <Link
              to="/signup"
              className="px-4 py-2 text-sm font-medium bg-primary-500 text-white rounded-lg hover:bg-primary-600 transition-colors"
            >
              Sign up free
            </Link>
          </div>
        </div>
      </header>

      {/* Hero */}
      <main className="flex-1">
        <section className="max-w-6xl mx-auto px-6 pt-20 pb-16 text-center">
          <h1 className="text-4xl md:text-5xl font-bold text-white leading-tight mb-6">
            Turn presentations into<br />
            <span className="text-primary-400">interactive flipbooks</span>
          </h1>
          <p className="text-lg text-dark-300 max-w-2xl mx-auto mb-10">
            Upload a PowerPoint or PDF and get a beautiful, embeddable flipbook in seconds.
            Share with a link or embed on any website.
          </p>
          <div className="flex items-center justify-center gap-4 flex-wrap">
            <Link
              to="/signup"
              className="inline-flex items-center gap-2 px-6 py-3 bg-primary-500 text-white rounded-lg font-medium hover:bg-primary-600 transition-colors text-lg"
            >
              Get started <ArrowRight className="w-5 h-5" />
            </Link>
          </div>
        </section>

        {/* Self-host callout */}
        <section className="max-w-4xl mx-auto px-6 pb-16">
          <div className="bg-dark-900/60 border border-dark-800 rounded-2xl p-6 md:p-8 text-center">
            <div className="flex items-center justify-center gap-2 mb-3">
              <Server className="w-5 h-5 text-accent-emerald" />
              <h2 className="text-lg font-semibold text-white">Prefer to self-host?</h2>
            </div>
            <p className="text-dark-300 mb-4 max-w-xl mx-auto">
              Flipbook is free and open source. Run it on your own infrastructure with full control over your data.
            </p>
            <a
              href="https://metavert.io/flipbook"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 text-primary-400 hover:text-primary-300 font-medium transition-colors"
            >
              Learn about self-hosting <ExternalLink className="w-4 h-4" />
            </a>
          </div>
        </section>

        {/* Features */}
        <section className="max-w-6xl mx-auto px-6 pb-20">
          <div className="grid gap-8 md:grid-cols-3">
            <div className="text-center">
              <div className="w-12 h-12 bg-primary-500/10 rounded-xl flex items-center justify-center mx-auto mb-4">
                <Zap className="w-6 h-6 text-primary-400" />
              </div>
              <h3 className="text-lg font-semibold text-white mb-2">Instant conversion</h3>
              <p className="text-dark-400 text-sm">
                Upload a .pptx or .pdf and get a ready-to-share flipbook in under a minute.
              </p>
            </div>
            <div className="text-center">
              <div className="w-12 h-12 bg-accent-purple/10 rounded-xl flex items-center justify-center mx-auto mb-4">
                <Globe className="w-6 h-6 text-accent-purple" />
              </div>
              <h3 className="text-lg font-semibold text-white mb-2">Share anywhere</h3>
              <p className="text-dark-400 text-sm">
                Get a direct viewer link or embed code to put your flipbook on any website.
              </p>
            </div>
            <div className="text-center">
              <div className="w-12 h-12 bg-accent-emerald/10 rounded-xl flex items-center justify-center mx-auto mb-4">
                <BookOpen className="w-6 h-6 text-accent-emerald" />
              </div>
              <h3 className="text-lg font-semibold text-white mb-2">Beautiful viewer</h3>
              <p className="text-dark-400 text-sm">
                Responsive page-flip viewer that works on desktop and mobile with keyboard navigation.
              </p>
            </div>
          </div>
        </section>

        {/* Pricing */}
        <section className="max-w-6xl mx-auto px-6 pb-20">
          <h2 className="text-3xl font-bold text-white text-center mb-3">Simple pricing</h2>
          <p className="text-dark-400 text-center mb-10 max-w-xl mx-auto">
            Start free, upgrade when you need more.
          </p>
          <div className="grid gap-6 md:grid-cols-3">
            {/* Free */}
            <div className="bg-dark-900/50 border border-dark-800 rounded-2xl p-6">
              <h3 className="text-lg font-bold text-white mb-1">Free</h3>
              <p className="text-dark-400 text-sm mb-4">Try it out</p>
              <div className="flex items-baseline gap-1 mb-6">
                <span className="text-3xl font-bold text-white">$0</span>
              </div>
              <ul className="space-y-3 mb-6">
                <li className="flex items-center gap-2 text-sm text-dark-300">
                  <Check className="w-4 h-4 text-accent-emerald flex-shrink-0" /> View &amp; share flipbooks
                </li>
              </ul>
              <Link
                to="/signup"
                className="block w-full py-2.5 text-sm font-medium text-center bg-dark-800 text-dark-300 rounded-lg hover:bg-dark-700 transition-colors"
              >
                Get started
              </Link>
            </div>

            {/* Basic */}
            <div className="bg-dark-900/50 border border-primary-500/30 rounded-2xl p-6 relative">
              <div className="absolute -top-3 left-6 px-3 py-0.5 bg-primary-500 text-white text-xs font-medium rounded-full">
                Popular
              </div>
              <h3 className="text-lg font-bold text-white mb-1 pt-1">Basic</h3>
              <p className="text-dark-400 text-sm mb-4">For individuals</p>
              <div className="flex items-baseline gap-1 mb-6">
                <span className="text-3xl font-bold text-white">$9.99</span>
                <span className="text-dark-400 text-sm">/mo</span>
              </div>
              <ul className="space-y-3 mb-6">
                <li className="flex items-center gap-2 text-sm text-dark-300">
                  <Check className="w-4 h-4 text-accent-emerald flex-shrink-0" /> Up to 50 pages
                </li>
                <li className="flex items-center gap-2 text-sm text-dark-300">
                  <Check className="w-4 h-4 text-accent-emerald flex-shrink-0" /> Upload &amp; convert
                </li>
                <li className="flex items-center gap-2 text-sm text-dark-300">
                  <Check className="w-4 h-4 text-accent-emerald flex-shrink-0" /> Share &amp; embed
                </li>
              </ul>
              <Link
                to="/signup"
                className="block w-full py-2.5 text-sm font-medium text-center bg-primary-500 text-white rounded-lg hover:bg-primary-600 transition-colors"
              >
                Start free trial
              </Link>
            </div>

            {/* Professional */}
            <div className="bg-dark-900/50 border border-dark-800 rounded-2xl p-6">
              <h3 className="text-lg font-bold text-white mb-1">Professional</h3>
              <p className="text-dark-400 text-sm mb-4">For power users</p>
              <div className="flex items-baseline gap-1 mb-6">
                <span className="text-3xl font-bold text-white">$29.99</span>
                <span className="text-dark-400 text-sm">/mo</span>
              </div>
              <ul className="space-y-3 mb-6">
                <li className="flex items-center gap-2 text-sm text-dark-300">
                  <Check className="w-4 h-4 text-accent-emerald flex-shrink-0" /> Up to 1,000 pages
                </li>
                <li className="flex items-center gap-2 text-sm text-dark-300">
                  <Check className="w-4 h-4 text-accent-emerald flex-shrink-0" /> Upload &amp; convert
                </li>
                <li className="flex items-center gap-2 text-sm text-dark-300">
                  <Check className="w-4 h-4 text-accent-emerald flex-shrink-0" /> Share &amp; embed
                </li>
              </ul>
              <Link
                to="/signup"
                className="block w-full py-2.5 text-sm font-medium text-center bg-dark-800 text-dark-300 rounded-lg hover:bg-dark-700 transition-colors"
              >
                Get started
              </Link>
            </div>
          </div>
        </section>
      </main>

      {/* Footer */}
      <footer className="border-t border-dark-800/50 py-8">
        <div className="max-w-6xl mx-auto px-6 flex flex-col md:flex-row items-center justify-between gap-4">
          <p className="text-dark-500 text-sm">
            &copy; 2026{' '}
            <a href="https://metavert.io" target="_blank" rel="noopener noreferrer" className="hover:text-dark-300 transition-colors">
              Metavert LLC
            </a>
          </p>
          <div className="flex items-center gap-6 text-sm text-dark-500">
            <a
              href="https://metavert.io/terms-of-service"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-dark-300 transition-colors"
            >
              Terms of Service
            </a>
            <a
              href="https://metavert.io/privacy-policy"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-dark-300 transition-colors"
            >
              Privacy Policy
            </a>
          </div>
        </div>
      </footer>
    </div>
  );
}
