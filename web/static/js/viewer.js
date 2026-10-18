(function () {
    'use strict';

    function startReader() {
        var data = window.FLIPBOOK_DATA;
        if (!data || !document.getElementById('reader-pages')) return;

        var pages = Array.isArray(data.pages) ? data.pages : [];
        var strings = data.strings || {};
        var stage = document.getElementById('reader-stage');
        var pagesElement = document.getElementById('reader-pages');
        var wrapper = document.getElementById('viewer-wrapper');
        var statusElement = document.getElementById('reader-status');
        var reducedMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
        var listeners = [];
        var timers = [];
        var generation = 0;
        var destroyed = false;
        var pageObserver = null;
        var resizeObserver = null;
        var activeDialog = null;
        var dialogReturnFocus = null;
        var renderTimer = null;
        var searchTimer = null;
        var resizeTimer = null;
        var storageKey = 'flipbook:reader:' + data.id;
        var stored = readStored();
        var urlParams = new URLSearchParams(window.location.search);
        var validModes = ['single', 'double', 'continuous', 'text'];
        var initialMode = validModes.indexOf(data.mode) !== -1 ? data.mode : 'single';
        if (!data.embedded && validModes.indexOf(stored.mode) !== -1 && !urlParams.has('mode') && !urlParams.has('page')) {
            initialMode = stored.mode;
        }
        if (validModes.indexOf(urlParams.get('mode')) !== -1) initialMode = urlParams.get('mode');
        var initialPage = clamp((parseInt(data.start_page, 10) || 1) - 1, 0, Math.max(pages.length - 1, 0));
        if (!data.embedded && !urlParams.has('page') && stored.page > 0 && stored.page <= pages.length) {
            initialPage = stored.page - 1;
        }
        var state = {
            current: initialPage,
            mode: initialMode,
            zoom: data.embedded ? 1 : clamp(Number(stored.zoom) || 1, 1, 4),
            panX: 0,
            panY: 0,
            bookmarks: new Set((stored.bookmarks || []).filter(function (page) { return page >= 1 && page <= pages.length; })),
            furthest: initialPage,
            search: {query: '', matches: [], index: -1, trackedQuery: ''},
            gateContext: 'reading',
            endCTAShown: false,
            completionTracked: false,
            lastFurthestTracked: -1
        };

        function t(key) {
            return strings[key] || key.replace(/_/g, ' ');
        }

        function clamp(value, minimum, maximum) {
            return Math.min(Math.max(value, minimum), maximum);
        }

        function byId(id) {
            return document.getElementById(id);
        }

        function listen(target, type, handler, options) {
            if (!target) return;
            target.addEventListener(type, handler, options);
            listeners.push(function () { target.removeEventListener(type, handler, options); });
        }

        function later(handler, delay) {
            var timer = window.setTimeout(function () {
                timers = timers.filter(function (item) { return item !== timer; });
                if (!destroyed) handler();
            }, delay);
            timers.push(timer);
            return timer;
        }

        function readStored() {
            try {
                var value = JSON.parse(window.localStorage.getItem(storageKey) || '{}');
                return value && typeof value === 'object' ? value : {};
            } catch (error) {
                return {};
            }
        }

        function persist() {
            try {
                window.localStorage.setItem(storageKey, JSON.stringify({
                    page: state.current + 1,
                    mode: state.mode,
                    zoom: state.zoom,
                    bookmarks: Array.from(state.bookmarks).sort(function (a, b) { return a - b; })
                }));
            } catch (error) {
                return;
            }
        }

        function announce(message) {
            if (!statusElement) return;
            statusElement.textContent = '';
            later(function () { statusElement.textContent = message; }, 20);
        }

        function applyTranslations() {
            document.querySelectorAll('[data-i18n-aria]').forEach(function (element) {
                var value = t(element.dataset.i18nAria);
                element.setAttribute('aria-label', value);
                element.setAttribute('title', value);
            });
            var modeSelect = byId('reader-mode');
            if (modeSelect) {
                var names = {single: 'single', double: 'double', continuous: 'continuous', text: 'text_mode'};
                Array.from(modeSelect.options).forEach(function (option) { option.textContent = t(names[option.value]); });
            }
            setText('btn-search', t('search'));
            setText('btn-bookmark', t('bookmarks'));
            setText('btn-print', t('print'));
            setText('btn-download', t('download'));
            setText('btn-share', t('share'));
            setText('share-native', t('native_share'));
            setText('copy-share', t('copy'));
            setText('copy-embed', t('copy'));
            setText('qr-svg', t('download_svg'));
            setText('qr-png', t('download_png'));
            var input = byId('search-input');
            if (input) input.placeholder = t('search_placeholder');
            setText('lead-dismiss', t('lead_dismiss'));
            document.querySelectorAll('[data-close]').forEach(function (element) {
                element.textContent = t('close');
                element.setAttribute('aria-label', t('close'));
            });
            var embedMode = byId('embed-mode');
            if (embedMode) {
                var embedNames = {single: 'single', double: 'double', continuous: 'continuous', text: 'text_mode'};
                Array.from(embedMode.options).forEach(function (option) { option.textContent = t(embedNames[option.value]); });
            }
        }

        function setText(id, value) {
            var element = byId(id);
            if (element) element.textContent = value;
        }

        function teardownPages() {
            generation += 1;
            if (pageObserver) {
                pageObserver.disconnect();
                pageObserver = null;
            }
            pagesElement.querySelectorAll('img').forEach(function (image) {
                image.onload = null;
                image.onerror = null;
            });
            pagesElement.replaceChildren();
        }

        function render(options) {
            if (destroyed) return;
            options = options || {};
            teardownPages();
            wrapper.dataset.mode = state.mode;
            pagesElement.className = 'reader-pages mode-' + state.mode;
            stage.classList.toggle('is-continuous', state.mode === 'continuous' || state.mode === 'text');
            stage.classList.toggle('is-text', state.mode === 'text');
            if (!pages.length) {
                byId('reader-empty').hidden = false;
                updateControls();
                return;
            }
            byId('reader-empty').hidden = true;
            if (state.mode === 'continuous' || state.mode === 'text') {
                pages.forEach(function (page, index) {
                    pagesElement.appendChild(state.mode === 'text' ? createTextPage(page, index) : createVisualPage(page, index));
                });
                setupPageObserver();
                if (!options.noScroll) scrollCurrentIntoView(false);
            } else {
                var indices = [state.current];
                if (state.mode === 'double' && state.current + 1 < pages.length) indices.push(state.current + 1);
                indices.forEach(function (index) { pagesElement.appendChild(createVisualPage(pages[index], index)); });
                fitPagedCanvases();
                indices.forEach(loadVisibleImage);
                prefetchAdjacent();
            }
            applyTransform();
            updateControls();
            updateBookmark();
            updateCTA();
        }

        function fitPagedCanvases() {
            var canvases = Array.from(pagesElement.querySelectorAll('.page-canvas'));
            if (!canvases.length) return;
            var styles = window.getComputedStyle(pagesElement);
            var availableWidth = pagesElement.clientWidth - parseFloat(styles.paddingLeft) - parseFloat(styles.paddingRight);
            var availableHeight = pagesElement.clientHeight - parseFloat(styles.paddingTop) - parseFloat(styles.paddingBottom);
            var gap = parseFloat(styles.columnGap) || 0;
            var maximumWidth = (availableWidth - gap * (canvases.length - 1)) / canvases.length;
            if (maximumWidth < 1 || availableHeight < 1) return;
            canvases.forEach(function (canvas) {
                var ratio = Number(canvas.dataset.width) / Number(canvas.dataset.height);
                if (!isFinite(ratio) || ratio <= 0) return;
                var width = Math.min(maximumWidth, availableHeight * ratio);
                var height = width / ratio;
                if (height > availableHeight) {
                    height = availableHeight;
                    width = height * ratio;
                }
                canvas.style.width = Math.max(1, Math.floor(width)) + 'px';
                canvas.style.height = Math.max(1, Math.floor(height)) + 'px';
            });
        }

        function createVisualPage(page, index) {
            var article = document.createElement('article');
            article.className = 'reader-page visual-page';
            article.dataset.page = String(index);
            article.setAttribute('aria-label', t('page') + ' ' + page.number);
            var canvas = document.createElement('div');
            canvas.className = 'page-canvas';
            canvas.style.aspectRatio = page.width + ' / ' + page.height;
            canvas.dataset.width = page.width;
            canvas.dataset.height = page.height;
            var image = document.createElement('img');
            image.className = 'page-image';
            image.alt = t('page') + ' ' + page.number + ' — ' + data.title;
            image.decoding = 'async';
            image.loading = 'lazy';
            image.draggable = false;
            image.dataset.source = page.src;
            image.dataset.highSource = page.high_src || page.src;
            canvas.appendChild(image);
            var highlights = document.createElement('div');
            highlights.className = 'page-highlights';
            canvas.appendChild(highlights);
            var overlays = document.createElement('div');
            overlays.className = 'page-overlays';
            buildLinkOverlays(overlays, page);
            canvas.appendChild(overlays);
            article.appendChild(canvas);
            buildSearchHighlights(article, page);
            return article;
        }

        function createTextPage(page, index) {
            var article = document.createElement('article');
            article.className = 'reader-page text-page';
            article.dataset.page = String(index);
            article.id = 'text-page-' + page.number;
            var heading = document.createElement('h2');
            heading.textContent = t('page') + ' ' + page.number;
            article.appendChild(heading);
            var content = document.createElement('p');
            if (page.text) {
                appendHighlightedText(content, page.text, state.search.query);
            } else {
                content.className = 'text-unavailable';
                content.textContent = t('text_unavailable');
            }
            article.appendChild(content);
            return article;
        }

        function buildLinkOverlays(container, page) {
            (page.links || []).forEach(function (link) {
                var style = 'left:' + (link.x * 100) + '%;top:' + (link.y * 100) + '%;width:' + (link.width * 100) + '%;height:' + (link.height * 100) + '%';
                if (link.type === 'internal') {
                    var internal = document.createElement('button');
                    internal.type = 'button';
                    internal.className = 'page-link internal-link';
                    internal.style.cssText = style;
                    internal.setAttribute('aria-label', link.title || (t('page') + ' ' + link.target_page));
                    internal.addEventListener('click', function (event) {
                        event.stopPropagation();
                        goToPage(link.target_page - 1);
                    });
                    container.appendChild(internal);
                    return;
                }
                if (link.type === 'video' && link.video_embed) {
                    var video = document.createElement('button');
                    video.type = 'button';
                    video.className = 'page-link video-link';
                    video.style.cssText = style;
                    video.setAttribute('aria-label', link.title || t('video'));
                    video.addEventListener('click', function (event) {
                        event.stopPropagation();
                        openVideo(link);
                    });
                    container.appendChild(video);
                    return;
                }
                if (!link.url) return;
                var anchor = document.createElement('a');
                anchor.className = 'page-link external-link';
                anchor.style.cssText = style;
                anchor.href = link.url;
                anchor.target = '_blank';
                anchor.rel = 'noopener nofollow';
                anchor.setAttribute('aria-label', link.title || link.url);
                anchor.addEventListener('click', function () {
                    var host = '';
                    try { host = new URL(link.url, window.location.href).host; } catch (error) { host = ''; }
                    track('outbound_click', page.number, 0, {host: host});
                });
                container.appendChild(anchor);
            });
        }

        function openVideo(link) {
            var frame = byId('video-frame');
            var title = byId('video-title');
            if (!frame) return;
            title.textContent = link.title || t('video');
            frame.src = link.video_embed;
            openDialog('video-dialog', document.activeElement);
            track('outbound_click', state.current + 1, 0, {host: new URL(link.url, window.location.href).host, media: 'video'});
        }

        function loadVisibleImage(index) {
            var element = pagesElement.querySelector('.visual-page[data-page="' + index + '"]');
            if (element) loadImage(element, state.zoom >= 1.75);
        }

        function loadImage(article, high) {
            var image = article.querySelector('.page-image');
            if (!image) return;
            var source = high ? image.dataset.highSource : image.dataset.source;
            if (!source || image.dataset.loadedSource === source || image.dataset.loadingSource === source) return;
            var currentGeneration = generation;
            image.dataset.loadingSource = source;
            image.addEventListener('load', function loaded() {
                image.removeEventListener('load', loaded);
                if (currentGeneration !== generation) return;
                image.dataset.loadedSource = source;
                delete image.dataset.loadingSource;
                article.classList.add('is-loaded');
            });
            image.addEventListener('error', function failed() {
                image.removeEventListener('error', failed);
                if (currentGeneration !== generation) return;
                delete image.dataset.loadingSource;
                if (!high && image.dataset.highSource && image.dataset.highSource !== source) {
                    loadImage(article, true);
                } else {
                    article.classList.add('load-failed');
                    image.alt = t('error');
                }
            });
            image.src = source;
        }

        function prefetchAdjacent() {
            [state.current - 1, state.current + 1, state.current + 2].forEach(function (index) {
                if (index < 0 || index >= pages.length) return;
                var source = pages[index].src;
                if (!source) return;
                var image = new Image();
                image.decoding = 'async';
                image.src = source;
            });
        }

        function setupPageObserver() {
            if (state.mode === 'text') return;
            var elements = Array.from(pagesElement.querySelectorAll('.visual-page'));
            if (!elements.length) return;
            if (typeof IntersectionObserver === 'undefined') {
                elements.slice(0, 3).forEach(function (element) { loadImage(element, false); });
                loadContinuousNearViewport();
                return;
            }
            var ratios = new Map();
            pageObserver = new IntersectionObserver(function (entries) {
                entries.forEach(function (entry) {
                    var index = Number(entry.target.dataset.page);
                    if (entry.isIntersecting) {
                        loadImage(entry.target, state.zoom >= 1.75);
                        ratios.set(index, entry.intersectionRatio);
                        var next = pagesElement.querySelector('.visual-page[data-page="' + (index + 1) + '"]');
                        if (next) loadImage(next, false);
                    } else {
                        ratios.delete(index);
                    }
                });
                var best = -1;
                var bestRatio = 0;
                ratios.forEach(function (ratio, index) {
                    if (ratio > bestRatio) {
                        best = index;
                        bestRatio = ratio;
                    }
                });
                if (best >= 0 && best !== state.current) setCurrent(best, {fromScroll: true});
            }, {root: stage, rootMargin: '120% 0px', threshold: [0, 0.25, 0.5, 0.75]});
            elements.forEach(function (element) { pageObserver.observe(element); });
        }

        function loadContinuousNearViewport() {
            if (state.mode !== 'continuous') return;
            var stageRect = stage.getBoundingClientRect();
            pagesElement.querySelectorAll('.visual-page').forEach(function (element) {
                var rect = element.getBoundingClientRect();
                if (rect.bottom > stageRect.top - stageRect.height && rect.top < stageRect.bottom + stageRect.height) {
                    loadImage(element, state.zoom >= 1.75);
                }
            });
        }

        function scrollCurrentIntoView(smooth) {
            var target = pagesElement.querySelector('.reader-page[data-page="' + state.current + '"]');
            if (!target) return;
            target.scrollIntoView({behavior: smooth && !reducedMotion ? 'smooth' : 'auto', block: 'start', inline: 'nearest'});
        }

        function goToPage(index, options) {
            options = options || {};
            if (index >= pages.length && index < data.page_count) {
                showGate('reading');
                return;
            }
            if (!pages.length) {
                showGate('reading');
                return;
            }
            var target = clamp(index, 0, pages.length - 1);
            setCurrent(target, options);
            if (!options.fromScroll) {
                if (state.mode === 'continuous' || state.mode === 'text') {
                    scrollCurrentIntoView(true);
                } else {
                    render({noScroll: true});
                }
            }
        }

        function setCurrent(index, options) {
            options = options || {};
            var changed = index !== state.current;
            state.current = index;
            state.furthest = Math.max(state.furthest, index);
            state.panX = 0;
            state.panY = 0;
            updateControls();
            updateBookmark();
            updateCTA();
            persist();
            updateURL();
            if (changed || options.forceTrack) trackPageExposure();
            if (changed) applyTransform();
        }

        function trackPageExposure() {
            var pageNumber = state.current + 1;
            track('page_view', pageNumber, 0, {mode: state.mode});
            if (state.furthest > state.lastFurthestTracked) {
                state.lastFurthestTracked = state.furthest;
                track('furthest_page', state.furthest + 1, 0);
            }
            if (data.full_access && pageNumber === data.page_count && !state.completionTracked) {
                state.completionTracked = true;
                track('completion', pageNumber, 0);
                if (data.cta && data.cta.show_on_end && !state.endCTAShown) {
                    state.endCTAShown = true;
                    later(function () { openDialog('end-cta-dialog', document.activeElement); }, 300);
                }
            }
        }

        function updateURL() {
            if (data.embedded || !window.history || !window.history.replaceState) return;
            var url = new URL(window.location.href);
            if (state.current > 0) url.searchParams.set('page', String(state.current + 1));
            else url.searchParams.delete('page');
            window.history.replaceState({page: state.current + 1}, '', url);
        }

        function updateControls() {
            var pageNumber = state.current + 1;
            var input = byId('current-page');
            if (input) {
                input.value = String(pageNumber);
                input.max = String(Math.max(pages.length, 1));
            }
            var slider = byId('page-slider');
            if (slider) {
                slider.value = String(clamp(pageNumber, 1, Math.max(pages.length, 1)));
                slider.max = String(Math.max(pages.length, 1));
                slider.disabled = pages.length < 2;
            }
            var mode = byId('reader-mode');
            if (mode) mode.value = state.mode;
            var previousDisabled = state.current <= 0;
            var nextDisabled = !pages.length || state.current >= data.page_count - 1;
            ['btn-prev', 'nav-prev'].forEach(function (id) { if (byId(id)) byId(id).disabled = previousDisabled; });
            ['btn-next', 'nav-next'].forEach(function (id) { if (byId(id)) byId(id).disabled = nextDisabled; });
            var zoomText = byId('btn-zoom-reset');
            if (zoomText) zoomText.textContent = Math.round(state.zoom * 100) + '%';
            var sharePage = byId('share-page-num');
            if (sharePage) sharePage.textContent = String(pageNumber);
            updateShareLinks();
        }

        function navigationStep() {
            return state.mode === 'double' ? 2 : 1;
        }

        function previousPage() {
            goToPage(state.current - navigationStep());
        }

        function nextPage() {
            goToPage(state.current + navigationStep());
        }

        function setMode(mode) {
            if (validModes.indexOf(mode) === -1 || mode === state.mode) return;
            state.mode = mode;
            state.panX = 0;
            state.panY = 0;
            render();
            persist();
        }

        function setZoom(value) {
            state.zoom = clamp(value, 1, 4);
            if (state.zoom === 1) {
                state.panX = 0;
                state.panY = 0;
            }
            applyTransform();
            updateControls();
            persist();
            if (state.zoom >= 1.75) {
                var selector = '.visual-page';
                if (state.mode === 'continuous') selector = '.visual-page[data-page="' + state.current + '"], .visual-page[data-page="' + (state.current + 1) + '"]';
                pagesElement.querySelectorAll(selector).forEach(function (element) { loadImage(element, true); });
            }
        }

        function applyTransform() {
            if (state.mode === 'text') {
                pagesElement.style.setProperty('--reader-text-scale', state.zoom);
                return;
            }
            pagesElement.querySelectorAll('.page-canvas').forEach(function (canvas) {
                if (state.mode === 'continuous') {
                    canvas.style.width = (state.zoom * 100) + '%';
                    canvas.style.transform = '';
                } else {
                    canvas.style.width = '';
                    canvas.style.transform = 'translate(' + state.panX + 'px,' + state.panY + 'px) scale(' + state.zoom + ')';
                }
            });
            stage.classList.toggle('is-zoomed', state.zoom > 1);
        }

        function updateBookmark() {
            var button = byId('btn-bookmark');
            if (!button) return;
            var marked = state.bookmarks.has(state.current + 1);
            button.setAttribute('aria-pressed', String(marked));
            button.classList.toggle('is-active', marked);
            button.title = marked ? t('bookmark_added') : t('bookmark');
        }

        function toggleBookmark() {
            if (!pages.length) return;
            var page = state.current + 1;
            if (state.bookmarks.has(page)) {
                state.bookmarks.delete(page);
                announce(t('bookmark_removed'));
            } else {
                state.bookmarks.add(page);
                announce(t('bookmark_added'));
            }
            persist();
            updateBookmark();
            if (activeDialog && activeDialog.id === 'library-dialog') buildBookmarks();
        }

        function updateCTA() {
            var cta = byId('publication-cta');
            if (!cta) return;
            if (!data.cta || state.current + 1 < Math.max(data.cta.start_page || 1, 1)) {
                cta.hidden = true;
                return;
            }
            cta.hidden = false;
            cta.href = data.cta.url;
            cta.textContent = data.cta.text || t('end_cta');
            cta.dataset.style = data.cta.style || 'primary';
            var endLink = byId('end-cta-link');
            if (endLink) {
                endLink.href = data.cta.url;
                endLink.textContent = data.cta.text || t('end_cta');
            }
        }

        function openDialog(id, trigger) {
            var dialog = byId(id);
            if (!dialog) return;
            if (activeDialog && activeDialog !== dialog) closeDialog(activeDialog, false);
            if (activeDialog === dialog && dialog.open) return;
            dialogReturnFocus = trigger || document.activeElement;
            activeDialog = dialog;
            if (typeof dialog.showModal === 'function') {
                if (!dialog.open) dialog.showModal();
            } else {
                dialog.setAttribute('open', '');
                dialog.classList.add('dialog-fallback');
            }
            later(function () {
                var focusable = dialogFocusable(dialog);
                if (focusable.length) focusable[0].focus();
            }, 0);
        }

        function closeDialog(dialog, restoreFocus) {
            if (!dialog) return;
            if (typeof dialog.close === 'function' && dialog.open) dialog.close();
            else dialog.removeAttribute('open');
            dialog.classList.remove('dialog-fallback');
            if (dialog.id === 'video-dialog') byId('video-frame').src = 'about:blank';
            if (dialog.id === 'share-dialog') byId('embed-preview').src = 'about:blank';
            if (activeDialog === dialog) activeDialog = null;
            if (restoreFocus !== false && dialogReturnFocus && document.contains(dialogReturnFocus)) {
                dialogReturnFocus.focus();
            }
            if (restoreFocus !== false) dialogReturnFocus = null;
        }

        function dialogFocusable(dialog) {
            return Array.from(dialog.querySelectorAll('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])')).filter(function (element) {
                return !element.hidden && element.getAttribute('aria-hidden') !== 'true' && element.offsetParent !== null;
            });
        }

        function trapDialogFocus(event) {
            if (!activeDialog || event.key !== 'Tab') return;
            var focusable = dialogFocusable(activeDialog);
            if (!focusable.length) {
                event.preventDefault();
                return;
            }
            var first = focusable[0];
            var last = focusable[focusable.length - 1];
            if (event.shiftKey && document.activeElement === first) {
                event.preventDefault();
                last.focus();
            } else if (!event.shiftKey && document.activeElement === last) {
                event.preventDefault();
                first.focus();
            }
        }

        function openSearch() {
            openDialog('search-dialog', document.activeElement);
            var input = byId('search-input');
            later(function () { input.focus(); input.select(); }, 0);
            var hasText = pages.some(function (page) { return page.text && page.text.trim(); });
            byId('search-unavailable').hidden = hasText;
            input.disabled = !hasText;
        }

        function scheduleSearch() {
            window.clearTimeout(searchTimer);
            searchTimer = window.setTimeout(performSearch, 250);
        }

        function performSearch() {
            var input = byId('search-input');
            var query = input.value.trim().toLocaleLowerCase(data.language);
            state.search.query = query;
            state.search.matches = [];
            state.search.index = -1;
            if (query) {
                pages.forEach(function (page, index) {
                    var text = page.text || '';
                    var lower = text.toLocaleLowerCase(data.language);
                    var offset = 0;
                    while (offset < lower.length) {
                        var found = lower.indexOf(query, offset);
                        if (found === -1) break;
                        state.search.matches.push({page: index, start: found, end: found + query.length, text: text});
                        offset = found + Math.max(query.length, 1);
                    }
                });
            }
            renderSearchResults();
            render();
            if (state.search.matches.length) {
                state.search.index = 0;
                goToMatch(0);
            }
            if (query && state.search.trackedQuery !== query) {
                state.search.trackedQuery = query;
                track('search', 0, 0, {query: query, result_count: state.search.matches.length});
            }
        }

        function renderSearchResults() {
            var list = byId('search-results');
            var status = byId('search-status');
            list.replaceChildren();
            var matches = state.search.matches;
            if (!state.search.query) status.textContent = '';
            else if (!matches.length) status.textContent = t('no_results');
            else status.textContent = matches.length + ' ' + t('results');
            matches.slice(0, 100).forEach(function (match, index) {
                var item = document.createElement('li');
                var button = document.createElement('button');
                button.type = 'button';
                button.className = 'search-result';
                var label = document.createElement('span');
                label.className = 'search-result-page';
                label.textContent = t('page') + ' ' + (match.page + 1);
                var snippet = document.createElement('span');
                snippet.className = 'search-result-snippet';
                var from = Math.max(0, match.start - 60);
                var to = Math.min(match.text.length, match.end + 90);
                if (from > 0) snippet.appendChild(document.createTextNode('…'));
                appendHighlightedText(snippet, match.text.slice(from, to), state.search.query);
                if (to < match.text.length) snippet.appendChild(document.createTextNode('…'));
                button.appendChild(label);
                button.appendChild(snippet);
                button.addEventListener('click', function () { goToMatch(index); });
                item.appendChild(button);
                list.appendChild(item);
            });
            updateSearchButtons();
        }

        function goToMatch(index) {
            var matches = state.search.matches;
            if (!matches.length) return;
            state.search.index = (index + matches.length) % matches.length;
            var match = matches[state.search.index];
            goToPage(match.page);
            updateSearchButtons();
            later(function () {
                var page = pagesElement.querySelector('.reader-page[data-page="' + match.page + '"]');
                var mark = page && page.querySelector('mark, .search-highlight');
                if (mark) mark.scrollIntoView({behavior: reducedMotion ? 'auto' : 'smooth', block: 'center'});
            }, 100);
        }

        function updateSearchButtons() {
            var count = state.search.matches.length;
            byId('search-prev').disabled = !count;
            byId('search-next').disabled = !count;
            if (count) byId('search-status').textContent = (state.search.index + 1) + ' / ' + count;
        }

        function appendHighlightedText(container, text, query) {
            if (!query) {
                container.appendChild(document.createTextNode(text));
                return;
            }
            var lower = text.toLocaleLowerCase(data.language);
            var normalized = query.toLocaleLowerCase(data.language);
            var offset = 0;
            while (offset < text.length) {
                var found = lower.indexOf(normalized, offset);
                if (found === -1) {
                    container.appendChild(document.createTextNode(text.slice(offset)));
                    return;
                }
                if (found > offset) container.appendChild(document.createTextNode(text.slice(offset, found)));
                var mark = document.createElement('mark');
                mark.textContent = text.slice(found, found + normalized.length);
                container.appendChild(mark);
                offset = found + normalized.length;
            }
        }

        function buildSearchHighlights(article, page) {
            if (!state.search.query || !page.spans || !page.width_points || !page.height_points) return;
            var container = article.querySelector('.page-highlights');
            if (!container) return;
            var query = state.search.query.toLocaleLowerCase(data.language);
            page.spans.forEach(function (span) {
                if (!span.text || span.text.toLocaleLowerCase(data.language).indexOf(query) === -1) return;
                var mark = document.createElement('span');
                mark.className = 'search-highlight';
                mark.style.left = (span.rect.x / page.width_points * 100) + '%';
                mark.style.top = (span.rect.y / page.height_points * 100) + '%';
                mark.style.width = (span.rect.width / page.width_points * 100) + '%';
                mark.style.height = (span.rect.height / page.height_points * 100) + '%';
                container.appendChild(mark);
            });
        }

        function openLibrary(tab) {
            buildTOC();
            buildGrid();
            buildBookmarks();
            openDialog('library-dialog', document.activeElement);
            selectLibraryTab(tab || 'toc');
        }

        function selectLibraryTab(tab) {
            ['toc', 'grid', 'bookmarks'].forEach(function (name) {
                byId('tab-' + name).setAttribute('aria-selected', String(name === tab));
                byId('panel-' + name).hidden = name !== tab;
            });
            if (tab === 'grid') {
                var active = byId('grid-container').querySelector('.is-active');
                if (active) active.scrollIntoView({block: 'center'});
            }
        }

        function buildTOC() {
            var container = byId('toc-list');
            container.replaceChildren();
            if (!data.outline || !data.outline.length) {
                var empty = document.createElement('p');
                empty.textContent = t('contents');
                container.appendChild(empty);
                return;
            }
            container.appendChild(buildTOCList(data.outline));
        }

        function buildTOCList(items) {
            var list = document.createElement('ul');
            items.forEach(function (item) {
                var row = document.createElement('li');
                if (item.target_page > 0) {
                    var button = document.createElement('button');
                    button.type = 'button';
                    button.textContent = item.title || (t('page') + ' ' + item.target_page);
                    button.addEventListener('click', function () {
                        goToPage(item.target_page - 1);
                        closeDialog(byId('library-dialog'));
                    });
                    row.appendChild(button);
                } else if (item.title) {
                    var heading = document.createElement('span');
                    heading.textContent = item.title;
                    row.appendChild(heading);
                }
                if (item.children && item.children.length) row.appendChild(buildTOCList(item.children));
                list.appendChild(row);
            });
            return list;
        }

        var gridBuilt = false;
        function buildGrid() {
            if (gridBuilt) {
                markGridActive();
                return;
            }
            var container = byId('grid-container');
            container.replaceChildren();
            pages.forEach(function (page, index) {
                var button = document.createElement('button');
                button.type = 'button';
                button.className = 'grid-page';
                button.dataset.page = String(index);
                button.setAttribute('aria-label', t('page') + ' ' + page.number);
                var image = document.createElement('img');
                image.src = page.thumb || page.src;
                image.alt = '';
                image.loading = 'lazy';
                var label = document.createElement('span');
                label.textContent = String(page.number);
                button.appendChild(image);
                button.appendChild(label);
                button.addEventListener('click', function () {
                    goToPage(index);
                    closeDialog(byId('library-dialog'));
                });
                container.appendChild(button);
            });
            gridBuilt = true;
            markGridActive();
        }

        function markGridActive() {
            byId('grid-container').querySelectorAll('.grid-page').forEach(function (element) {
                element.classList.toggle('is-active', Number(element.dataset.page) === state.current);
            });
        }

        function buildBookmarks() {
            var container = byId('bookmarks-list');
            container.replaceChildren();
            if (!state.bookmarks.size) {
                var empty = document.createElement('p');
                empty.textContent = t('no_bookmarks');
                container.appendChild(empty);
                return;
            }
            var list = document.createElement('ul');
            Array.from(state.bookmarks).sort(function (a, b) { return a - b; }).forEach(function (pageNumber) {
                if (pageNumber > pages.length) return;
                var item = document.createElement('li');
                var button = document.createElement('button');
                button.type = 'button';
                button.textContent = t('page') + ' ' + pageNumber;
                button.addEventListener('click', function () {
                    goToPage(pageNumber - 1);
                    closeDialog(byId('library-dialog'));
                });
                item.appendChild(button);
                list.appendChild(item);
            });
            container.appendChild(list);
        }

        function currentShareURL() {
            var url = new URL(data.canonical_url, window.location.href);
            var includePage = byId('share-from-page') && byId('share-from-page').checked;
            if (includePage && state.current > 0) url.searchParams.set('page', String(state.current + 1));
            else url.searchParams.delete('page');
            return url.toString();
        }

        function updateShareLinks() {
            var link = byId('share-link');
            if (!link) return;
            var url = currentShareURL();
            link.value = url;
            var email = byId('share-email');
            if (email) email.href = 'mailto:?subject=' + encodeURIComponent(data.title) + '&body=' + encodeURIComponent(url);
            updateEmbedCode();
            updateQRCodes();
        }

        function buildSocialLinks() {
            var container = byId('share-links');
            if (!container) return;
            container.replaceChildren();
            Object.keys(data.share.links || {}).forEach(function (channel) {
                if (channel === 'email') return;
                var anchor = document.createElement('a');
                anchor.href = data.share.links[channel];
                anchor.target = '_blank';
                anchor.rel = 'noopener nofollow';
                anchor.textContent = channel === 'x' ? 'X' : channel.charAt(0).toUpperCase() + channel.slice(1);
                anchor.addEventListener('click', function () { track('share', state.current + 1, 0, {channel: channel}); });
                container.appendChild(anchor);
            });
        }

        function embedValues() {
            return {
                width: clamp(parseInt(byId('embed-width').value, 10) || 800, 200, 3840),
                height: clamp(parseInt(byId('embed-height').value, 10) || 600, 150, 2160),
                aspect: byId('embed-aspect').value.trim(),
                responsive: byId('embed-responsive').checked,
                page: clamp(parseInt(byId('embed-page').value, 10) || 1, 1, Math.max(pages.length, 1)),
                mode: byId('embed-mode').value,
                background: byId('embed-background').value,
                toolbar: byId('embed-toolbar').checked,
                search: byId('embed-search').checked,
                share: byId('embed-share').checked,
                download: byId('embed-download').checked,
                branding: byId('embed-branding').checked
            };
        }

        function embedURL(options) {
            var raw = data.base_url.replace(/\/$/, '') + '/embed/' + encodeURIComponent(data.slug);
            var url = new URL(raw, window.location.href);
            ['toolbar', 'search', 'share', 'download', 'branding'].forEach(function (key) { url.searchParams.set(key, String(options[key])); });
            if (options.page > 1) url.searchParams.set('page', String(options.page));
            if (options.mode !== 'single') url.searchParams.set('mode', options.mode);
            if (options.background) url.searchParams.set('background', options.background);
            if (options.aspect && /^\d+(\.\d+)?\/\d+(\.\d+)?$/.test(options.aspect)) url.searchParams.set('aspect', options.aspect);
            if (!options.responsive) {
                url.searchParams.set('responsive', 'false');
                url.searchParams.set('width', String(options.width));
                url.searchParams.set('height', String(options.height));
            }
            return url.toString();
        }

        function updateEmbedCode() {
            var textarea = byId('embed-code');
            if (!textarea) return;
            var options = embedValues();
            var url = embedURL(options);
            var title = escapeAttribute(data.title + ' — interactive flipbook');
            var code;
            if (options.responsive) {
                var aspect = options.aspect && /^\d+(\.\d+)?\/\d+(\.\d+)?$/.test(options.aspect) ? options.aspect : options.width + '/' + options.height;
                code = '<div class="flipbook-embed" style="position:relative;width:100%;aspect-ratio:' + escapeAttribute(aspect) + '"><iframe src="' + escapeAttribute(url) + '" title="' + title + '" style="position:absolute;inset:0;width:100%;height:100%;border:0" loading="lazy" allowfullscreen></iframe></div>';
            } else {
                code = '<iframe src="' + escapeAttribute(url) + '" title="' + title + '" width="' + options.width + '" height="' + options.height + '" style="border:0" loading="lazy" allowfullscreen></iframe>';
            }
            textarea.value = code;
            var preview = byId('embed-preview');
            var container = byId('embed-preview-container');
            if (activeDialog && activeDialog.id === 'share-dialog' && !byId('panel-embed').hidden) {
                container.hidden = false;
                if (preview.dataset.source !== url) {
                    preview.dataset.source = url;
                    preview.src = url;
                }
            } else {
                container.hidden = true;
            }
        }

        function updateQRCodes() {
            var base = data.endpoints.qr;
            if (!base || !byId('qr-image')) return;
            var page = byId('share-from-page') && byId('share-from-page').checked ? state.current + 1 : 0;
            var svg = withQuery(base, {format: 'svg', page: page});
            var png = withQuery(base, {format: 'png', page: page, size: 512});
            var image = byId('qr-image');
            if (image.dataset.source !== svg) {
                image.dataset.source = svg;
                image.src = svg;
            }
            byId('qr-svg').href = svg;
            byId('qr-png').href = png;
        }

        function withQuery(raw, values) {
            var url = new URL(raw, window.location.href);
            Object.keys(values).forEach(function (key) {
                if (values[key]) url.searchParams.set(key, String(values[key]));
                else url.searchParams.delete(key);
            });
            return url.toString();
        }

        function escapeAttribute(value) {
            return String(value).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
        }

        function copyField(id, button) {
            var field = byId(id);
            if (!field) return;
            var copied = function () {
                var original = button.textContent;
                button.textContent = t('copied');
                later(function () { button.textContent = original; }, 1400);
            };
            if (navigator.clipboard && window.isSecureContext) {
                navigator.clipboard.writeText(field.value).then(copied).catch(function () { fallbackCopy(field, copied); });
            } else {
                fallbackCopy(field, copied);
            }
        }

        function fallbackCopy(field, callback) {
            field.focus();
            field.select();
            try { document.execCommand('copy'); } catch (error) { return; }
            callback();
        }

        function selectShareTab(tab) {
            ['share', 'embed', 'qr'].forEach(function (name) {
                byId('tab-' + name).setAttribute('aria-selected', String(name === tab));
                byId('panel-' + name).hidden = name !== tab;
            });
            if (tab === 'embed') updateEmbedCode();
            if (tab !== 'embed') {
                byId('embed-preview').src = 'about:blank';
                delete byId('embed-preview').dataset.source;
            }
        }

        function showGate(context) {
            state.gateContext = context || 'reading';
            var kind = '';
            if (state.gateContext === 'download') kind = data.download_gate;
            if (!kind && data.lead && data.lead.required) kind = 'lead';
            if (!kind && data.sale && data.sale.required) kind = 'purchase';
            if (kind === 'lead') openDialog('lead-dialog', document.activeElement);
            else if (kind === 'purchase') openDialog('purchase-dialog', document.activeElement);
        }

        function postJSON(url, body) {
            var options = {method: 'POST', credentials: 'same-origin', headers: {'Accept': 'application/json'}};
            if (body !== null) {
                options.headers['Content-Type'] = 'application/json';
                options.body = JSON.stringify(body);
            }
            return fetch(url, options).then(function (response) {
                return response.json().catch(function () { return {}; }).then(function (result) {
                    if (!response.ok) throw new Error(result.error || t('error'));
                    return result;
                });
            });
        }

        function submitLead(event) {
            event.preventDefault();
            var form = byId('lead-form');
            var button = form.querySelector('button[type="submit"]');
            var error = byId('lead-error');
            error.hidden = true;
            button.disabled = true;
            postJSON(data.endpoints.lead, {email: byId('lead-email').value.trim(), consent: byId('lead-consent').checked})
                .then(function () { window.location.reload(); })
                .catch(function (reason) {
                    error.textContent = reason.message;
                    error.hidden = false;
                    button.disabled = false;
                });
        }

        function dismissLead() {
            var button = byId('lead-dismiss');
            button.disabled = true;
            postJSON(data.endpoints.dismiss, null)
                .then(function () { window.location.reload(); })
                .catch(function (reason) {
                    var error = byId('lead-error');
                    error.textContent = reason.message;
                    error.hidden = false;
                    button.disabled = false;
                });
        }

        function startCheckout() {
            var button = byId('btn-checkout');
            var error = byId('purchase-error');
            button.disabled = true;
            error.hidden = true;
            postJSON(data.endpoints.checkout, null).then(function (result) {
                if (!result.checkout_url) throw new Error(t('error'));
                var target = new URL(result.checkout_url, window.location.href);
                if (target.protocol !== 'https:' && target.protocol !== 'http:') throw new Error(t('error'));
                window.location.assign(target.toString());
            }).catch(function (reason) {
                error.textContent = reason.message;
                error.hidden = false;
                button.disabled = false;
            });
        }

        function printCurrentPage() {
            if (data.download_gate) {
                showGate('download');
                return;
            }
            if (!pages.length) return;
            var image = byId('print-image');
            var page = pages[state.current];
            var printed = false;
            image.alt = t('page') + ' ' + page.number;
            var print = function () {
                if (printed) return;
                printed = true;
                document.body.classList.add('reader-printing');
                window.print();
            };
            image.onload = print;
            image.onerror = function () { announce(t('error')); };
            image.src = page.high_src || page.src;
            if (image.complete && image.naturalWidth) print();
        }

        var eventQueue = [];
        var sessionID = createSessionID();
        var qualifiedRead = false;
        var activeMilliseconds = 0;
        var activeStarted = 0;

        function createSessionID() {
            var allowed = data.analytics.policy === 'anonymous' || data.analytics.consented;
            try {
                if (allowed) {
                    var existing = window.sessionStorage.getItem('flipbook:analytics-session');
                    if (existing) return existing;
                }
                var values = new Uint32Array(4);
                window.crypto.getRandomValues(values);
                var id = Array.from(values).map(function (value) { return value.toString(16); }).join('');
                if (allowed) window.sessionStorage.setItem('flipbook:analytics-session', id);
                return id;
            } catch (error) {
                return Math.random().toString(36).slice(2) + Date.now().toString(36);
            }
        }

        function track(type, page, duration, metadata) {
            if (!data.endpoints.events) return;
            eventQueue.push({type: type, page: page || 0, duration_ms: Math.max(0, Math.round(duration || 0)), session_id: sessionID, metadata: metadata || {}});
            if (eventQueue.length >= 20) flushEvents(false);
            if (eventQueue.length > 200) eventQueue.splice(0, eventQueue.length - 200);
        }

        function eventEndpoint() {
            var url = new URL(data.endpoints.events, window.location.href);
            var current = new URL(window.location.href);
            ['utm_source', 'utm_medium', 'utm_campaign', 'utm_content', 'utm_term'].forEach(function (key) {
                if (current.searchParams.has(key)) url.searchParams.set(key, current.searchParams.get(key));
            });
            url.searchParams.set('placement', data.analytics.placement);
            return url.toString();
        }

        function flushEvents(beacon) {
            if (!eventQueue.length || !data.endpoints.events) return;
            var batch = eventQueue.splice(0, 50);
            var payload = JSON.stringify({events: batch});
            if (beacon && navigator.sendBeacon) {
                var sent = navigator.sendBeacon(eventEndpoint(), new Blob([payload], {type: 'application/json'}));
                if (sent) return;
            }
            fetch(eventEndpoint(), {method: 'POST', credentials: 'same-origin', headers: {'Content-Type': 'application/json'}, body: payload, keepalive: !!beacon})
                .then(function (response) {
                    if (!response.ok) throw new Error('event ingestion failed');
                })
                .catch(function () {
                    if (!destroyed) eventQueue = batch.concat(eventQueue).slice(-200);
                });
        }

        function updateActiveTime() {
            var now = Date.now();
            var shouldTrack = document.visibilityState === 'visible' && document.hasFocus();
            if (shouldTrack && !activeStarted) activeStarted = now;
            if (!shouldTrack && activeStarted) {
                accumulateActive(now);
                activeStarted = 0;
            }
        }

        function accumulateActive(now) {
            if (!activeStarted) return;
            var elapsed = Math.max(0, now - activeStarted);
            activeStarted = now;
            activeMilliseconds += elapsed;
            if (elapsed >= 500) track('active_time', state.current + 1, elapsed, {mode: state.mode});
            if (!qualifiedRead && activeMilliseconds >= 10000) {
                qualifiedRead = true;
                track('qualified_read', state.current + 1, 0);
            }
        }

        function toggleFullscreen() {
            if (document.fullscreenElement) {
                document.exitFullscreen();
                return;
            }
            if (wrapper.requestFullscreen) wrapper.requestFullscreen().catch(function () { wrapper.classList.toggle('pseudo-fullscreen'); });
            else wrapper.classList.toggle('pseudo-fullscreen');
            scheduleResize();
        }

        function scheduleResize() {
            window.clearTimeout(resizeTimer);
            resizeTimer = window.setTimeout(function () {
                if (state.mode === 'single' || state.mode === 'double') render({noScroll: true});
                else applyTransform();
            }, 160);
        }

        function handleKeyboard(event) {
            trapDialogFocus(event);
            var target = event.target;
            var editing = target && (/^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName) || target.isContentEditable);
            if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f' && data.show_search) {
                event.preventDefault();
                openSearch();
                return;
            }
            if (event.key === 'Escape' && activeDialog) {
                if (activeDialog.id === 'lead-dialog' && data.lead.required && !data.lead.dismissible && state.gateContext === 'reading') {
                    event.preventDefault();
                    return;
                }
                closeDialog(activeDialog);
                return;
            }
            if (editing || activeDialog) return;
            switch (event.key) {
                case 'ArrowLeft':
                case 'PageUp':
                    event.preventDefault();
                    previousPage();
                    break;
                case 'ArrowRight':
                case 'PageDown':
                case ' ':
                    event.preventDefault();
                    nextPage();
                    break;
                case 'Home':
                    event.preventDefault();
                    goToPage(0);
                    break;
                case 'End':
                    event.preventDefault();
                    goToPage(data.page_count - 1);
                    break;
                case '+':
                case '=':
                    event.preventDefault();
                    setZoom(state.zoom + 0.25);
                    break;
                case '-':
                    event.preventDefault();
                    setZoom(state.zoom - 0.25);
                    break;
                case '0':
                    event.preventDefault();
                    setZoom(1);
                    break;
                case 'f':
                case 'F':
                    event.preventDefault();
                    toggleFullscreen();
                    break;
                case 'g':
                case 'G':
                    event.preventDefault();
                    openLibrary('grid');
                    break;
                case 'b':
                case 'B':
                    event.preventDefault();
                    toggleBookmark();
                    break;
            }
        }

        var pointers = new Map();
        var gesture = null;
        function pointerDown(event) {
            if (event.target.closest('a,button,input,select,textarea,dialog')) return;
            pointers.set(event.pointerId, {x: event.clientX, y: event.clientY});
            if (pointers.size > 1) {
                gesture = null;
                return;
            }
            gesture = {id: event.pointerId, x: event.clientX, y: event.clientY, lastX: event.clientX, lastY: event.clientY, pan: state.zoom > 1 && state.mode !== 'continuous' && state.mode !== 'text'};
            if (gesture.pan) stage.setPointerCapture(event.pointerId);
        }

        function pointerMove(event) {
            if (!gesture || gesture.id !== event.pointerId || pointers.size !== 1) return;
            var dx = event.clientX - gesture.lastX;
            var dy = event.clientY - gesture.lastY;
            gesture.lastX = event.clientX;
            gesture.lastY = event.clientY;
            if (gesture.pan) {
                event.preventDefault();
                var canvas = pagesElement.querySelector('.visual-page[data-page="' + state.current + '"] .page-canvas');
                var maximumX = canvas ? (state.zoom - 1) * canvas.clientWidth / 2 : 0;
                var maximumY = canvas ? (state.zoom - 1) * canvas.clientHeight / 2 : 0;
                state.panX = clamp(state.panX + dx, -maximumX, maximumX);
                state.panY = clamp(state.panY + dy, -maximumY, maximumY);
                applyTransform();
            } else if (state.mode === 'single' || state.mode === 'double') {
                var totalX = event.clientX - gesture.x;
                var totalY = event.clientY - gesture.y;
                if (Math.abs(totalX) > Math.abs(totalY) && event.cancelable) event.preventDefault();
            }
        }

        function pointerEnd(event) {
            pointers.delete(event.pointerId);
            if (!gesture || gesture.id !== event.pointerId) return;
            var dx = event.clientX - gesture.x;
            var dy = event.clientY - gesture.y;
            var wasPan = gesture.pan;
            gesture = null;
            if (!wasPan && Math.abs(dx) > 55 && Math.abs(dx) > Math.abs(dy) * 1.4 && (state.mode === 'single' || state.mode === 'double')) {
                var next = data.direction === 'rtl' ? dx > 0 : dx < 0;
                if (next) nextPage();
                else previousPage();
            }
        }

        function destroy() {
            if (destroyed) return;
            updateActiveTime();
            accumulateActive(Date.now());
            flushEvents(true);
            destroyed = true;
            teardownPages();
            listeners.splice(0).forEach(function (remove) { remove(); });
            timers.splice(0).forEach(window.clearTimeout);
            window.clearTimeout(renderTimer);
            window.clearTimeout(searchTimer);
            window.clearTimeout(resizeTimer);
            window.clearInterval(eventTimer);
            if (resizeObserver) resizeObserver.disconnect();
            if (activeDialog) closeDialog(activeDialog, false);
        }

        applyTranslations();
        buildSocialLinks();
        var nativeShare = byId('share-native');
        if (nativeShare) nativeShare.hidden = !data.share.native || !navigator.share;
        var price = byId('purchase-price');
        if (price && data.sale) {
            try {
                price.textContent = new Intl.NumberFormat(data.language, {style: 'currency', currency: data.sale.currency || 'USD'}).format((data.sale.price_cents || 0) / 100);
            } catch (error) {
                price.textContent = ((data.sale.price_cents || 0) / 100).toFixed(2) + ' ' + (data.sale.currency || 'USD');
            }
        }

        listen(byId('btn-prev'), 'click', previousPage);
        listen(byId('nav-prev'), 'click', previousPage);
        listen(byId('btn-next'), 'click', nextPage);
        listen(byId('nav-next'), 'click', nextPage);
        listen(byId('page-form'), 'submit', function (event) {
            event.preventDefault();
            goToPage((parseInt(byId('current-page').value, 10) || 1) - 1);
        });
        listen(byId('page-slider'), 'input', function (event) { goToPage(Number(event.target.value) - 1); });
        listen(byId('reader-mode'), 'change', function (event) { setMode(event.target.value); });
        listen(byId('btn-zoom-out'), 'click', function () { setZoom(state.zoom - 0.25); });
        listen(byId('btn-zoom-in'), 'click', function () { setZoom(state.zoom + 0.25); });
        listen(byId('btn-zoom-reset'), 'click', function () { setZoom(1); });
        listen(byId('btn-fit-width'), 'click', function () { setZoom(1); });
        listen(byId('btn-search'), 'click', openSearch);
        listen(byId('btn-library'), 'click', function () { openLibrary('toc'); });
        listen(byId('btn-bookmark'), 'click', toggleBookmark);
        listen(byId('btn-fullscreen'), 'click', toggleFullscreen);
        listen(byId('btn-print'), 'click', printCurrentPage);
        listen(byId('btn-download'), 'click', function (event) {
            if (data.download_gate || !data.capabilities.download_pdf) {
                event.preventDefault();
                showGate('download');
            }
        });
        listen(byId('btn-share'), 'click', function () {
            updateShareLinks();
            openDialog('share-dialog', document.activeElement);
        });
        listen(byId('share-from-page'), 'change', updateShareLinks);
        listen(byId('share-native'), 'click', function () {
            navigator.share({title: data.title, url: currentShareURL()}).then(function () { track('share', state.current + 1, 0, {channel: 'native'}); }).catch(function () { return; });
        });
        listen(byId('share-email'), 'click', function () { track('share', state.current + 1, 0, {channel: 'email'}); });
        listen(byId('copy-share'), 'click', function (event) { copyField('share-link', event.currentTarget); });
        listen(byId('copy-embed'), 'click', function (event) { copyField('embed-code', event.currentTarget); });
        listen(byId('search-input'), 'input', scheduleSearch);
        listen(byId('search-input'), 'keydown', function (event) {
            if (event.key === 'Enter') {
                event.preventDefault();
                goToMatch(state.search.index + (event.shiftKey ? -1 : 1));
            }
        });
        listen(byId('search-prev'), 'click', function () { goToMatch(state.search.index - 1); });
        listen(byId('search-next'), 'click', function () { goToMatch(state.search.index + 1); });
        listen(byId('lead-form'), 'submit', submitLead);
        listen(byId('lead-dismiss'), 'click', dismissLead);
        listen(byId('btn-checkout'), 'click', startCheckout);
        listen(byId('publication-cta'), 'click', function () { track('cta_click', state.current + 1, 0, {host: new URL(data.cta.url).host}); });
        listen(byId('end-cta-link'), 'click', function () { track('cta_click', state.current + 1, 0, {host: new URL(data.cta.url).host, placement: 'end'}); });
        listen(byId('print-image'), 'afterprint', function () { return; });

        document.querySelectorAll('[data-close]').forEach(function (button) {
            listen(button, 'click', function () { closeDialog(button.closest('dialog')); });
        });
        document.querySelectorAll('[data-library-tab]').forEach(function (button) {
            listen(button, 'click', function () { selectLibraryTab(button.dataset.libraryTab); });
        });
        document.querySelectorAll('[data-share-tab]').forEach(function (button) {
            listen(button, 'click', function () { selectShareTab(button.dataset.shareTab); });
        });
        document.querySelectorAll('.embed-settings input, .embed-settings select').forEach(function (field) {
            listen(field, 'input', updateEmbedCode);
            listen(field, 'change', updateEmbedCode);
        });
        document.querySelectorAll('dialog').forEach(function (dialog) {
            listen(dialog, 'cancel', function (event) {
                if (dialog.id === 'lead-dialog' && data.lead.required && !data.lead.dismissible && state.gateContext === 'reading') event.preventDefault();
            });
            listen(dialog, 'close', function () {
                if (dialog.id === 'video-dialog') byId('video-frame').src = 'about:blank';
                if (activeDialog === dialog) activeDialog = null;
            });
            listen(dialog, 'click', function (event) {
                if (event.target === dialog && !(dialog.id === 'lead-dialog' && data.lead.required && !data.lead.dismissible)) closeDialog(dialog);
            });
        });

        listen(document, 'keydown', handleKeyboard);
        listen(document, 'visibilitychange', updateActiveTime);
        listen(window, 'focus', updateActiveTime);
        listen(window, 'blur', updateActiveTime);
        listen(window, 'resize', scheduleResize, {passive: true});
        listen(document, 'fullscreenchange', scheduleResize);
        listen(window, 'afterprint', function () { document.body.classList.remove('reader-printing'); });
        listen(window, 'pagehide', function (event) {
            updateActiveTime();
            accumulateActive(Date.now());
            flushEvents(true);
            if (!event.persisted) destroy();
        });
        listen(window, 'popstate', function () {
            var page = parseInt(new URL(window.location.href).searchParams.get('page'), 10) || 1;
            goToPage(page - 1);
        });
        listen(stage, 'pointerdown', pointerDown);
        listen(stage, 'pointermove', pointerMove, {passive: false});
        listen(stage, 'pointerup', pointerEnd);
        listen(stage, 'pointercancel', pointerEnd);
        listen(stage, 'scroll', loadContinuousNearViewport, {passive: true});
        listen(stage, 'wheel', function (event) {
            if (!event.ctrlKey && !event.metaKey) return;
            event.preventDefault();
            setZoom(state.zoom + (event.deltaY < 0 ? 0.15 : -0.15));
        }, {passive: false});

        if (typeof ResizeObserver !== 'undefined') {
            resizeObserver = new ResizeObserver(scheduleResize);
            resizeObserver.observe(stage);
        }
        var eventTimer = window.setInterval(function () {
            accumulateActive(Date.now());
            flushEvents(false);
        }, 15000);
        timers.push(eventTimer);
        updateActiveTime();
        render({noScroll: true});
        trackPageExposure();
        if (!pages.length) {
            later(function () { showGate('reading'); }, 250);
        }

        window.FlipbookReader = {
            goToPage: function (page) { goToPage(page - 1); },
            search: function (query) {
                byId('search-input').value = query;
                openSearch();
                performSearch();
            },
            destroy: destroy
        };
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', startReader, {once: true});
    else startReader();
}());
