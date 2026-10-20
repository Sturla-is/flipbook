/* ============================================================
   Link hotspot editor
   Draw, move, resize, import, duplicate, and style clickable
   link overlays over flipbook pages.
   ============================================================ */
(function () {
    "use strict";

    var data = window.LINK_EDITOR_DATA;
    if (!data || !data.api) return;

    /* The markup and script are versioned together: if the page still uses the
       previous template (server not restarted), explain instead of half-working. */
    var requiredIds = ["page-strip", "page-canvas", "add-link", "import-all", "props-form", "page-indicator", "props-close"];
    var missing = requiredIds.filter(function (id) { return !document.getElementById(id); });
    if (missing.length || !document.querySelector('input[name="link-type"]')) {
        var notice = document.getElementById("client-error");
        if (notice) {
            notice.textContent = "The link editor was updated. Restart the Flipbook server and reload this page to activate it.";
            notice.classList.remove("hidden");
        }
        return;
    }

    var MIN_SIZE = 0.01;
    var DEDUP_TOLERANCE = 0.005;
    var SNAP_TOLERANCE = 0.015;
    var SNAP_POINTS = [0, 0.5, 1];
    var HANDLES = ["nw", "n", "ne", "e", "se", "s", "sw", "w"];

    var strip = document.getElementById("page-strip");
    var stage = document.querySelector(".link-editor-stage");
    var canvas = document.getElementById("page-canvas");
    var pageImage = document.getElementById("page-image");
    var overlayLayer = document.getElementById("overlay-layer");
    var drawRect = document.getElementById("draw-rect");
    var stageHint = document.getElementById("stage-hint");
    var stageEmpty = document.getElementById("stage-empty");
    var snapV = document.getElementById("snap-v");
    var snapH = document.getElementById("snap-h");
    var sizeReadout = document.getElementById("size-readout");
    var addButton = document.getElementById("add-link");
    var importAllButton = document.getElementById("import-all");
    var pdfCountPill = document.getElementById("pdf-count");
    var pageStats = document.getElementById("page-stats");
    var pageIndicator = document.getElementById("page-indicator");
    var prevPageButton = document.getElementById("prev-page");
    var nextPageButton = document.getElementById("next-page");
    var propsEmpty = document.getElementById("props-empty");
    var propsForm = document.getElementById("props-form");
    var propsClose = document.getElementById("props-close");
    var saveButton = document.getElementById("link-save");
    var deleteButton = document.getElementById("link-delete");
    var duplicateButton = document.getElementById("link-duplicate");
    var openButton = document.getElementById("link-open");
    var urlField = document.querySelector("[data-field-url]");
    var targetField = document.querySelector("[data-field-target]");
    var toasts = document.getElementById("toasts");

    var state = {
        page: 1,
        overlays: Array.isArray(data.overlays) ? data.overlays : [],
        selectedId: null,
        pending: null,
        drawMode: false,
        drag: null,
        moved: false
    };

    /* ---------------- helpers ---------------- */

    function pageData(number) {
        var pages = data.pages || [];
        for (var i = 0; i < pages.length; i++) {
            if (pages[i].number === number) return pages[i];
        }
        return null;
    }

    function csrfToken() {
        var input = document.querySelector('input[name="csrf_token"]');
        return input && input.value ? input.value : "";
    }

    function clamp(value, min, max) {
        return Math.min(max, Math.max(min, value));
    }

    function normalizeRect(rect) {
        rect.width = clamp(rect.width, MIN_SIZE, 1);
        rect.height = clamp(rect.height, MIN_SIZE, 1);
        rect.x = clamp(rect.x, 0, 1 - rect.width);
        rect.y = clamp(rect.y, 0, 1 - rect.height);
        return rect;
    }

    function rectsMatch(a, b) {
        return Math.abs(a.x - b.x) <= DEDUP_TOLERANCE &&
            Math.abs(a.y - b.y) <= DEDUP_TOLERANCE &&
            Math.abs(a.width - b.width) <= DEDUP_TOLERANCE &&
            Math.abs(a.height - b.height) <= DEDUP_TOLERANCE;
    }

    function findOverlay(id) {
        for (var i = 0; i < state.overlays.length; i++) {
            if (state.overlays[i].id === id) return state.overlays[i];
        }
        return null;
    }

    function isTyping() {
        var el = document.activeElement;
        if (!el) return false;
        var tag = el.tagName;
        return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || el.isContentEditable;
    }

    function percent(value) {
        return (clamp(value, 0, 1) * 100).toFixed(1).replace(/\.0$/, "") + "%";
    }

    function fmtRect(rect) {
        return percent(rect.width) + " \u00d7 " + percent(rect.height);
    }

    async function api(method, path, body) {
        var headers = { "Accept": "application/json", "X-Requested-With": "XMLHttpRequest" };
        var token = csrfToken();
        if (token) headers["X-CSRF-Token"] = token;
        if (body !== undefined) headers["Content-Type"] = "application/json";
        var response = await fetch(data.api + path, {
            method: method,
            credentials: "same-origin",
            headers: headers,
            body: body !== undefined ? JSON.stringify(body) : undefined
        });
        if (response.status === 204) return null;
        var payload = {};
        try { payload = await response.json(); } catch (_) { payload = {}; }
        if (!response.ok) {
            throw new Error(payload.error || "The request could not be completed (" + response.status + ").");
        }
        return payload;
    }

    /* ---------------- feedback ---------------- */

    var hintTimer = null;
    function hint(message, isError) {
        stageHint.textContent = message || "";
        stageHint.classList.toggle("text-error", Boolean(isError));
        if (hintTimer) clearTimeout(hintTimer);
        if (message) {
            hintTimer = setTimeout(function () {
                if (stageHint.textContent === message) {
                    stageHint.textContent = "";
                    stageHint.classList.remove("text-error");
                }
            }, 5000);
        }
    }

    function toast(message, type) {
        var el = document.createElement("div");
        el.className = "toast" + (type ? " is-" + type : "");
        el.textContent = message;
        el.setAttribute("role", "status");
        toasts.appendChild(el);
        var remove = function () {
            el.classList.add("is-leaving");
            setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, 220);
        };
        var timer = setTimeout(remove, 3200);
        el.addEventListener("click", function () { clearTimeout(timer); remove(); });
    }

    /* ---------------- rendering ---------------- */

    function overlaysOnPage(number) {
        return state.overlays.filter(function (o) { return o.page === number; }).length;
    }

    function renderStrip() {
        strip.textContent = "";
        (data.pages || []).forEach(function (page) {
            var button = document.createElement("button");
            button.type = "button";
            button.className = "page-thumb" + (page.number === state.page ? " active" : "");
            button.setAttribute("aria-label", "Page " + page.number);
            button.setAttribute("aria-current", page.number === state.page ? "page" : "false");
            var img = document.createElement("img");
            img.src = page.thumb || page.src;
            img.alt = "";
            img.loading = "lazy";
            img.draggable = false;
            var label = document.createElement("span");
            label.className = "thumb-label";
            label.textContent = String(page.number);
            button.appendChild(img);
            button.appendChild(label);
            var count = overlaysOnPage(page.number);
            if (count > 0) {
                var badge = document.createElement("span");
                badge.className = "thumb-count";
                badge.textContent = String(count);
                button.appendChild(badge);
            }
            button.addEventListener("click", function () { selectPage(page.number); });
            strip.appendChild(button);
        });
    }

    function positionStyle(rect) {
        return "left:" + rect.x * 100 + "%;top:" + rect.y * 100 + "%;width:" + rect.width * 100 + "%;height:" + rect.height * 100 + "%";
    }

    function applyStyleVars(element, style) {
        var color = style && typeof style.color === "string" && /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(style.color)
            ? style.color : "#C63E2B";
        var hex = color.slice(1);
        if (hex.length === 3) {
            hex = hex.charAt(0) + hex.charAt(0) + hex.charAt(1) + hex.charAt(1) + hex.charAt(2) + hex.charAt(2);
        }
        var r = parseInt(hex.slice(0, 2), 16);
        var g = parseInt(hex.slice(2, 4), 16);
        var b = parseInt(hex.slice(4, 6), 16);
        var opacity = Number(style && style.opacity);
        if (!isFinite(opacity) || opacity <= 0 || opacity > 1) opacity = 0.18;
        element.style.setProperty("--link-fill", "rgba(" + r + "," + g + "," + b + "," + opacity + ")");
        element.style.setProperty("--link-highlight", "rgba(" + r + "," + g + "," + b + "," + Math.min(1, opacity + 0.08) + ")");
        element.style.setProperty("--link-border", color);
        var radius = Number(style && style.radius);
        if (isFinite(radius) && radius >= 0 && radius <= 50) {
            element.style.setProperty("--link-radius", radius + "px");
        }
    }

    function overlayLabel(overlay) {
        if (overlay.type === "internal") return "Page " + overlay.target_page;
        if (overlay.type === "video") return "Video";
        return "Web";
    }

    function isPDFLinkImported(link, page) {
        var rect = { x: link.x, y: link.y, width: link.width, height: link.height };
        return state.overlays.some(function (overlay) {
            return overlay.page === page && overlay.source_rect && rectsMatch(overlay.source_rect, rect);
        });
    }

    function renderOverlays() {
        overlayLayer.textContent = "";
        var page = pageData(state.page);
        if (!page) return;
        (page.pdf_links || []).forEach(function (link) {
            if (isPDFLinkImported(link, state.page)) return;
            var box = document.createElement("button");
            box.type = "button";
            box.className = "link-box-pdf";
            box.style.cssText = positionStyle(link);
            var badge = document.createElement("span");
            badge.className = "pdf-badge";
            badge.textContent = "PDF \u2192 import";
            box.appendChild(badge);
            box.title = link.title || link.url || ("Page " + link.target_page);
            box.setAttribute("aria-label", "Import PDF link: " + box.title);
            box.addEventListener("click", function (event) {
                event.stopPropagation();
                importPDFLink(link);
            });
            overlayLayer.appendChild(box);
        });
        state.overlays.forEach(function (overlay) {
            if (overlay.page !== state.page) return;
            overlayLayer.appendChild(renderBox(overlay, false));
        });
        if (state.pending && state.pending.page === state.page) {
            overlayLayer.appendChild(renderBox(state.pending, true));
        }
        updatePageStats();
        updateEmptyState();
    }

    function renderBox(overlay, unsaved) {
        var box = document.createElement("div");
        box.className = "link-box" +
            (isSelected(overlay) ? " is-selected" : "") +
            (overlay.enabled === false ? " is-disabled" : "") +
            (unsaved ? " is-unsaved" : "");
        box.style.cssText = positionStyle(overlay);
        applyStyleVars(box, overlay.style);
        box.setAttribute("role", "button");
        box.tabIndex = 0;
        box.setAttribute("aria-label", "Edit link: " + (overlay.title || overlay.url || overlayLabel(overlay)));
        box.title = overlay.title || overlay.url || overlayLabel(overlay);
        var label = document.createElement("span");
        label.className = "box-label";
        label.textContent = overlayLabel(overlay);
        box.appendChild(label);
        if (isSelected(overlay)) {
            var meta = document.createElement("span");
            meta.className = "box-meta";
            meta.textContent = fmtRect(overlay);
            box.appendChild(meta);
            HANDLES.forEach(function (name) {
                var handle = document.createElement("span");
                handle.className = "resize-handle";
                handle.dataset.handle = name;
                handle.setAttribute("aria-hidden", "true");
                box.appendChild(handle);
            });
        }
        box.addEventListener("mousedown", function (event) { startDrag(event, overlay, unsaved); });
        box.addEventListener("keydown", function (event) {
            if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                selectOverlay(overlay);
                if (event.key === "Enter") focusUrl();
            }
        });
        return box;
    }

    function isSelected(overlay) {
        if (state.pending && overlay === state.pending) return true;
        return overlay.id && overlay.id === state.selectedId;
    }

    function renderStage() {
        var page = pageData(state.page);
        if (!page) return;
        canvas.style.aspectRatio = page.width + " / " + page.height;
        pageImage.src = page.src;
        pageImage.alt = "Page " + state.page;
        renderOverlays();
        updateEmptyState();
    }

    function updatePageStats() {
        var page = pageData(state.page);
        var overlays = overlaysOnPage(state.page);
        var pdf = (page && page.pdf_links ? page.pdf_links.filter(function (l) { return !isPDFLinkImported(l, state.page); }).length : 0);
        pageStats.textContent = overlays + " hotspot" + (overlays === 1 ? "" : "s") + " \u00b7 " + pdf + " PDF link" + (pdf === 1 ? "" : "s") + " available";
        pdfCountPill.textContent = String(pdf);
        pdfCountPill.hidden = pdf === 0;
        importAllButton.disabled = pdf === 0;
    }

    function updateEmptyState() {
        var page = pageData(state.page);
        var overlays = overlaysOnPage(state.page);
        var pdf = (page && page.pdf_links ? page.pdf_links.length : 0);
        var pending = state.pending && state.pending.page === state.page;
        stageEmpty.hidden = overlays > 0 || pdf > 0 || pending;
    }

    function selectPage(number) {
        if (state.pending && state.pending.page === state.page) discardPending();
        number = clamp(number, 1, (data.pages || []).length);
        state.page = number;
        state.selectedId = null;
        setDrawMode(false);
        renderStrip();
        renderStage();
        renderProps();
        pageIndicator.textContent = "Page " + number + " / " + ((data.pages || []).length);
    }

    /* ---------------- selection & properties ---------------- */

    function selectedOverlay() {
        if (state.pending) return state.pending;
        return state.selectedId ? findOverlay(state.selectedId) : null;
    }

    function selectOverlay(overlay) {
        state.selectedId = overlay.id || null;
        renderOverlays();
        renderProps();
    }

    function deselect() {
        state.selectedId = null;
        renderOverlays();
        renderProps();
    }

    function focusUrl() {
        var type = currentType();
        if (type === "internal") {
            var target = document.getElementById("link-target-page");
            if (target) target.focus();
        } else if (urlField) {
            urlField.querySelector("input").focus();
        }
    }

    function currentType() {
        var checked = document.querySelector('input[name="link-type"]:checked');
        return checked ? checked.value : "external";
    }

    function setType(value) {
        var radio = document.querySelector('input[name="link-type"][value="' + value + '"]');
        if (radio) radio.checked = true;
        syncTypeFields();
    }

    function syncTypeFields() {
        var type = currentType();
        urlField.hidden = type === "internal";
        targetField.hidden = type !== "internal";
        var input = urlField.querySelector("input");
        openButton.disabled = type === "internal" || !(input && input.value.trim());
    }

    function syncSwatches() {
        var color = document.getElementById("link-color").value.trim().toLowerCase();
        document.querySelectorAll("#color-swatches .swatch").forEach(function (swatch) {
            swatch.classList.toggle("is-active", (swatch.dataset.color || "").toLowerCase() === color);
        });
    }

    function renderProps() {
        var overlay = selectedOverlay();
        propsForm.hidden = !overlay;
        propsEmpty.hidden = Boolean(overlay);
        propsClose.hidden = !overlay;
        if (!overlay) return;
        setType(overlay.type || "external");
        urlField.querySelector("input").value = overlay.url || "";
        document.getElementById("link-target-page").value = overlay.target_page || "";
        document.getElementById("link-title").value = overlay.title || "";
        document.getElementById("link-enabled").checked = overlay.enabled !== false;
        var style = overlay.style || {};
        document.getElementById("link-color").value = style.color || "";
        var opacity = Number(style.opacity);
        if (!isFinite(opacity) || opacity <= 0 || opacity > 1) opacity = 0.18;
        document.getElementById("link-opacity").value = String(opacity);
        document.getElementById("opacity-value").value = opacity.toFixed(2);
        var radius = Number(style.radius);
        if (!isFinite(radius) || radius < 0) radius = 0;
        document.getElementById("link-radius").value = String(radius);
        document.getElementById("radius-value").value = radius + "px";
        syncRectInputs(overlay);
        deleteButton.hidden = !overlay.id;
        duplicateButton.hidden = !overlay.id;
        saveButton.textContent = overlay.id ? "Save link" : "Create link";
        syncSwatches();
        syncTypeFields();
    }

    function syncRectInputs(overlay) {
        document.getElementById("link-x").value = (overlay.x * 100).toFixed(1);
        document.getElementById("link-y").value = (overlay.y * 100).toFixed(1);
        document.getElementById("link-w").value = (overlay.width * 100).toFixed(1);
        document.getElementById("link-h").value = (overlay.height * 100).toFixed(1);
    }

    function readRectInputs() {
        var x = parseFloat(document.getElementById("link-x").value);
        var y = parseFloat(document.getElementById("link-y").value);
        var w = parseFloat(document.getElementById("link-w").value);
        var h = parseFloat(document.getElementById("link-h").value);
        if (!isFinite(x) || !isFinite(y) || !isFinite(w) || !isFinite(h)) return null;
        return normalizeRect({ x: x / 100, y: y / 100, width: w / 100, height: h / 100 });
    }

    function formPayload(overlay) {
        var type = currentType();
        var payload = {
            page: overlay.page,
            x: overlay.x, y: overlay.y, width: overlay.width, height: overlay.height,
            type: type,
            url: type === "internal" ? "" : urlField.querySelector("input").value.trim(),
            target_page: type === "internal" ? parseInt(document.getElementById("link-target-page").value, 10) || 0 : 0,
            title: document.getElementById("link-title").value.trim(),
            enabled: document.getElementById("link-enabled").checked
        };
        var color = document.getElementById("link-color").value.trim();
        var opacity = parseFloat(document.getElementById("link-opacity").value);
        var radius = parseFloat(document.getElementById("link-radius").value);
        if (color || isFinite(opacity) || isFinite(radius)) {
            payload.style = { color: color };
            if (isFinite(opacity)) payload.style.opacity = opacity;
            if (isFinite(radius)) payload.style.radius = radius;
        } else if (overlay.style) {
            payload.style = {};
        }
        return payload;
    }

    /* ---------------- form events ---------------- */

    document.querySelectorAll('input[name="link-type"]').forEach(function (radio) {
        radio.addEventListener("change", syncTypeFields);
    });

    urlField.querySelector("input").addEventListener("input", function () {
        syncTypeFields();
        syncSwatches();
    });

    document.getElementById("link-color").addEventListener("input", syncSwatches);
    document.getElementById("link-opacity").addEventListener("input", function () {
        document.getElementById("opacity-value").value = parseFloat(this.value).toFixed(2);
    });
    document.getElementById("link-radius").addEventListener("input", function () {
        document.getElementById("radius-value").value = parseInt(this.value, 10) + "px";
    });

    ["link-x", "link-y", "link-w", "link-h"].forEach(function (id) {
        document.getElementById(id).addEventListener("input", function () {
            var overlay = selectedOverlay();
            if (!overlay) return;
            var rect = readRectInputs();
            if (!rect) return;
            Object.assign(overlay, rect);
            renderOverlays();
            var meta = document.querySelector(".link-box.is-selected .box-meta");
            if (meta) meta.textContent = fmtRect(overlay);
        });
    });

    document.getElementById("color-swatches").addEventListener("click", function (event) {
        var swatch = event.target.closest(".swatch[data-color]");
        if (!swatch) return;
        document.getElementById("link-color").value = swatch.dataset.color;
        syncSwatches();
    });

    openButton.addEventListener("click", function () {
        var url = urlField.querySelector("input").value.trim();
        if (!url) return;
        try {
            var parsed = new URL(url, window.location.origin);
            window.open(parsed.href, "_blank", "noopener");
        } catch (_) {
            toast("Enter a valid URL first.", "error");
        }
    });

    propsClose.addEventListener("click", deselect);

    propsForm.addEventListener("submit", async function (event) {
        event.preventDefault();
        if (!propsForm.reportValidity()) return;
        var overlay = selectedOverlay();
        if (!overlay) return;
        var rect = readRectInputs();
        if (rect) Object.assign(overlay, rect);
        var payload = formPayload(overlay);
        saveButton.disabled = true;
        try {
            if (overlay.id) {
                var updated = await api("PATCH", "/" + encodeURIComponent(overlay.id), payload);
                Object.assign(overlay, updated);
                toast("Link saved.");
            } else {
                var created = await api("POST", "", payload);
                state.overlays.push(created);
                state.pending = null;
                state.selectedId = created.id;
                setDrawMode(false);
                toast("Hotspot created and live in the reader.");
            }
            renderOverlays();
            renderProps();
            renderStrip();
        } catch (error) {
            toast(error.message, "error");
        } finally {
            saveButton.disabled = false;
        }
    });

    deleteButton.addEventListener("click", async function () {
        var overlay = selectedOverlay();
        if (!overlay) return;
        if (!overlay.id) {
            discardPending();
            renderOverlays();
            renderProps();
            renderStrip();
            return;
        }
        if (!window.confirm("Delete this hotspot? The PDF link it replaced, if any, will reappear.")) return;
        deleteButton.disabled = true;
        try {
            await api("DELETE", "/" + encodeURIComponent(overlay.id));
            state.overlays = state.overlays.filter(function (candidate) { return candidate.id !== overlay.id; });
            state.selectedId = null;
            renderOverlays();
            renderProps();
            renderStrip();
            toast("Hotspot deleted.");
        } catch (error) {
            toast(error.message, "error");
        } finally {
            deleteButton.disabled = false;
        }
    });

    duplicateButton.addEventListener("click", async function () {
        var overlay = selectedOverlay();
        if (!overlay || !overlay.id) return;
        var rect = readRectInputs();
        if (rect) Object.assign(overlay, rect);
        var payload = formPayload(overlay);
        payload.x = clamp(overlay.x + 0.02, 0, 1 - overlay.width);
        payload.y = clamp(overlay.y + 0.02, 0, 1 - overlay.height);
        duplicateButton.disabled = true;
        try {
            var created = await api("POST", "", payload);
            state.overlays.push(created);
            state.selectedId = created.id;
            renderOverlays();
            renderProps();
            renderStrip();
            toast("Hotspot duplicated.");
        } catch (error) {
            toast(error.message, "error");
        } finally {
            duplicateButton.disabled = false;
        }
    });

    /* ---------------- PDF link import ---------------- */

    async function importPDFLink(link) {
        var payload = {
            page: state.page,
            x: link.x, y: link.y, width: link.width, height: link.height,
            type: link.type === "internal" || link.type === "video" ? link.type : "external",
            title: link.title || "",
            enabled: true,
            source_rect: { x: link.x, y: link.y, width: link.width, height: link.height }
        };
        if (payload.type === "internal") {
            payload.target_page = link.target_page;
        } else {
            payload.url = link.url || "";
        }
        try {
            var created = await api("POST", "", payload);
            state.overlays.push(created);
            state.selectedId = created.id;
            renderOverlays();
            renderProps();
            renderStrip();
            toast("PDF link imported \u2014 now an editable hotspot.");
        } catch (error) {
            toast(error.message, "error");
        }
    }

    importAllButton.addEventListener("click", async function () {
        var page = pageData(state.page);
        var links = (page && page.pdf_links || []).filter(function (l) { return !isPDFLinkImported(l, state.page); });
        if (!links.length) {
            toast("No new PDF links to import on this page.");
            return;
        }
        importAllButton.disabled = true;
        var done = 0;
        try {
            for (var i = 0; i < links.length; i++) {
                var link = links[i];
                var payload = {
                    page: state.page,
                    x: link.x, y: link.y, width: link.width, height: link.height,
                    type: link.type === "internal" || link.type === "video" ? link.type : "external",
                    title: link.title || "",
                    enabled: true,
                    source_rect: { x: link.x, y: link.y, width: link.width, height: link.height }
                };
                if (payload.type === "internal") {
                    payload.target_page = link.target_page;
                } else {
                    payload.url = link.url || "";
                }
                var created = await api("POST", "", payload);
                state.overlays.push(created);
                done++;
            }
            state.selectedId = null;
            renderOverlays();
            renderProps();
            renderStrip();
            toast("Imported " + done + " PDF link" + (done === 1 ? "" : "s") + " on this page.");
        } catch (error) {
            toast(error.message, "error");
        } finally {
            importAllButton.disabled = false;
        }
    });

    /* ---------------- drag, resize, draw ---------------- */

    function canvasPoint(event) {
        var rect = canvas.getBoundingClientRect();
        return {
            x: (event.clientX - rect.left) / rect.width,
            y: (event.clientY - rect.top) / rect.height
        };
    }

    function showSnapGuides(rect) {
        var v = null, h = null;
        SNAP_POINTS.forEach(function (p) {
            if (Math.abs(rect.x - p) <= SNAP_TOLERANCE || Math.abs(rect.x + rect.width - p) <= SNAP_TOLERANCE) v = p;
            if (Math.abs(rect.y - p) <= SNAP_TOLERANCE || Math.abs(rect.y + rect.height - p) <= SNAP_TOLERANCE) h = p;
        });
        if (v === null) { snapV.hidden = true; } else { snapV.hidden = false; snapV.style.left = v * 100 + "%"; }
        if (h === null) { snapH.hidden = true; } else { snapH.hidden = false; snapH.style.top = h * 100 + "%"; }
        return { v: v, h: h };
    }

    function applySnap(rect) {
        SNAP_POINTS.forEach(function (p) {
            if (Math.abs(rect.x - p) <= SNAP_TOLERANCE) rect.x = p;
            if (Math.abs(rect.y - p) <= SNAP_TOLERANCE) rect.y = p;
            if (Math.abs(rect.x + rect.width - p) <= SNAP_TOLERANCE) rect.width = p - rect.x;
            if (Math.abs(rect.y + rect.height - p) <= SNAP_TOLERANCE) rect.height = p - rect.y;
        });
        return normalizeRect(rect);
    }

    function hideGuides() {
        snapV.hidden = true;
        snapH.hidden = true;
        sizeReadout.hidden = true;
    }

    function showSizeReadout(event, rect) {
        var point = canvasPoint(event);
        sizeReadout.hidden = false;
        sizeReadout.textContent = fmtRect(rect);
        sizeReadout.style.left = clamp(point.x * 100, 8, 92) + "%";
        sizeReadout.style.top = clamp(point.y * 100, 4, 96) + "%";
    }

    function startDrag(event, overlay, unsaved) {
        if (event.button !== 0) return;
        event.preventDefault();
        event.stopPropagation();
        selectOverlay(overlay);
        var handle = event.target.classList && event.target.classList.contains("resize-handle") ? event.target.dataset.handle : null;
        state.drag = {
            overlay: overlay,
            unsaved: unsaved,
            handle: handle,
            start: canvasPoint(event),
            origin: { x: overlay.x, y: overlay.y, width: overlay.width, height: overlay.height }
        };
        state.moved = false;
        document.addEventListener("mousemove", onDragMove);
        document.addEventListener("mouseup", onDragEnd, { once: true });
    }

    function onDragMove(event) {
        var drag = state.drag;
        if (!drag) return;
        var point = canvasPoint(event);
        var dx = point.x - drag.start.x;
        var dy = point.y - drag.start.y;
        var rect = Object.assign({}, drag.origin);
        if (!drag.handle) {
            rect.x = drag.origin.x + dx;
            rect.y = drag.origin.y + dy;
        } else {
            if (drag.handle.indexOf("w") !== -1) {
                rect.x = drag.origin.x + dx;
                rect.width = drag.origin.width - dx;
            }
            if (drag.handle.indexOf("e") !== -1) {
                rect.width = drag.origin.width + dx;
            }
            if (drag.handle.indexOf("n") !== -1) {
                rect.y = drag.origin.y + dy;
                rect.height = drag.origin.height - dy;
            }
            if (drag.handle.indexOf("s") !== -1) {
                rect.height = drag.origin.height + dy;
            }
            if (rect.width < MIN_SIZE) {
                if (drag.handle.indexOf("w") !== -1) rect.x = drag.origin.x + drag.origin.width - MIN_SIZE;
                rect.width = MIN_SIZE;
            }
            if (rect.height < MIN_SIZE) {
                if (drag.handle.indexOf("n") !== -1) rect.y = drag.origin.y + drag.origin.height - MIN_SIZE;
                rect.height = MIN_SIZE;
            }
        }
        if (!state.moved && (Math.abs(dx) > 0.002 || Math.abs(dy) > 0.002)) state.moved = true;
        if (state.moved) {
            applySnap(rect);
            showSnapGuides(rect);
            showSizeReadout(event, rect);
            Object.assign(drag.overlay, rect);
            renderOverlays();
            if (drag.overlay === selectedOverlay()) syncRectInputs(drag.overlay);
        }
    }

    async function onDragEnd() {
        document.removeEventListener("mousemove", onDragMove);
        var drag = state.drag;
        state.drag = null;
        hideGuides();
        if (!drag) return;
        var overlay = drag.overlay;
        var moved = overlay.x !== drag.origin.x || overlay.y !== drag.origin.y ||
            overlay.width !== drag.origin.width || overlay.height !== drag.origin.height;
        if (!moved || drag.unsaved || !overlay.id) {
            renderOverlays();
            return;
        }
        try {
            var updated = await api("PATCH", "/" + encodeURIComponent(overlay.id), {
                x: overlay.x, y: overlay.y, width: overlay.width, height: overlay.height
            });
            Object.assign(overlay, updated);
            toast("Position saved.");
        } catch (error) {
            Object.assign(overlay, drag.origin);
            toast(error.message, "error");
        }
        renderOverlays();
        if (overlay.id === state.selectedId) syncRectInputs(overlay);
    }

    /* ---------------- draw mode ---------------- */

    function setDrawMode(active) {
        state.drawMode = active;
        stage.classList.toggle("drawing", active);
        addButton.setAttribute("aria-pressed", String(active));
        addButton.classList.toggle("is-active", active);
        hint(active
            ? "Draw mode: drag a rectangle on the page, then set its link. Press Escape to cancel."
            : "");
    }

    addButton.addEventListener("click", function () {
        if (state.drawMode) {
            setDrawMode(false);
            return;
        }
        if (state.pending && state.pending.page === state.page) discardPending();
        setDrawMode(true);
    });

    var drawStart = null;

    canvas.addEventListener("mousedown", function (event) {
        if (event.button !== 0) return;
        if (!state.drawMode) {
            if (event.target === canvas || event.target === pageImage || event.target === overlayLayer) {
                deselect();
            }
            return;
        }
        event.preventDefault();
        discardPending();
        drawStart = canvasPoint(event);
        document.addEventListener("mousemove", onDrawMove);
        document.addEventListener("mouseup", onDrawEnd, { once: true });
    });

    function onDrawMove(event) {
        if (!drawStart) return;
        var point = canvasPoint(event);
        var rect = {
            x: Math.min(drawStart.x, point.x),
            y: Math.min(drawStart.y, point.y),
            width: Math.abs(point.x - drawStart.x),
            height: Math.abs(point.y - drawStart.y)
        };
        applySnap(rect);
        showSnapGuides(rect);
        drawRect.hidden = false;
        drawRect.style.cssText = positionStyle(rect);
        showSizeReadout(event, rect);
    }

    function onDrawEnd(event) {
        document.removeEventListener("mousemove", onDrawMove);
        if (!drawStart) return;
        var point = canvasPoint(event);
        var rect = {
            x: Math.min(drawStart.x, point.x),
            y: Math.min(drawStart.y, point.y),
            width: Math.abs(point.x - drawStart.x),
            height: Math.abs(point.y - drawStart.y)
        };
        drawStart = null;
        drawRect.hidden = true;
        hideGuides();
        setDrawMode(false);
        if (rect.width < MIN_SIZE || rect.height < MIN_SIZE) {
            toast("That area was too small \u2014 drag a larger rectangle.", "error");
            return;
        }
        applySnap(rect);
        state.pending = {
            page: state.page,
            x: rect.x, y: rect.y, width: rect.width, height: rect.height,
            type: "external", url: "", title: "", enabled: true
        };
        state.selectedId = null;
        renderOverlays();
        renderProps();
        updateEmptyState();
        focusUrl();
        toast("New hotspot drawn \u2014 set the link and press Create.");
    }

    function discardPending() {
        state.pending = null;
    }

    /* ---------------- keyboard ---------------- */

    document.addEventListener("keydown", function (event) {
        if (isTyping()) return;
        var overlay = selectedOverlay();
        var step = event.shiftKey ? 0.001 : 0.01;
        var dx = 0, dy = 0;
        if (event.key === "Escape") {
            if (state.drawMode) {
                setDrawMode(false);
                hideGuides();
                return;
            }
            if (state.selectedId || state.pending) { deselect(); return; }
        }
        switch (event.key) {
            case "d": case "D":
                if (!event.metaKey && !event.ctrlKey && !event.altKey) {
                    event.preventDefault();
                    setDrawMode(!state.drawMode);
                }
                return;
            case "PageUp":
                event.preventDefault(); selectPage(state.page - 1); return;
            case "PageDown":
                event.preventDefault(); selectPage(state.page + 1); return;
            case "ArrowLeft": dx = -step; break;
            case "ArrowRight": dx = step; break;
            case "ArrowUp": dy = -step; break;
            case "ArrowDown": dy = step; break;
            case "Delete": case "Backspace":
                if (overlay && overlay.id) {
                    event.preventDefault();
                    deleteButton.click();
                } else if (overlay && !overlay.id) {
                    discardPending();
                    renderOverlays();
                    renderProps();
                    updateEmptyState();
                }
                return;
            default:
                return;
        }
        if (!overlay || !dx && !dy) return;
        event.preventDefault();
        var rect = normalizeRect({ x: overlay.x + dx, y: overlay.y + dy, width: overlay.width, height: overlay.height });
        Object.assign(overlay, rect);
        renderOverlays();
        syncRectInputs(overlay);
        if (overlay.id) {
            api("PATCH", "/" + encodeURIComponent(overlay.id), {
                x: overlay.x, y: overlay.y, width: overlay.width, height: overlay.height
            }).then(function (updated) {
                Object.assign(overlay, updated);
                renderOverlays();
                syncRectInputs(overlay);
                hint("Nudged \u2014 position saved.");
            }).catch(function (error) {
                toast(error.message, "error");
            });
        }
    });

    /* ---------------- boot ---------------- */

    prevPageButton.addEventListener("click", function () { selectPage(state.page - 1); });
    nextPageButton.addEventListener("click", function () { selectPage(state.page + 1); });

    renderStrip();
    renderStage();
    renderProps();
    pageIndicator.textContent = "Page 1 / " + ((data.pages || []).length);
    setDrawMode(false);
})();
