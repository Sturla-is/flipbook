/* Link hotspot editor: draw, move, resize, and import link overlays. */
(function () {
    "use strict";

    var data = window.LINK_EDITOR_DATA;
    if (!data || !data.api) return;

    var MIN_SIZE = 0.01;
    var DEDUP_TOLERANCE = 0.005;
    var HANDLES = ["nw", "n", "ne", "e", "se", "s", "sw", "w"];

    var strip = document.getElementById("page-strip");
    var stage = document.querySelector(".link-editor-stage");
    var canvas = document.getElementById("page-canvas");
    var pageImage = document.getElementById("page-image");
    var overlayLayer = document.getElementById("overlay-layer");
    var drawRect = document.getElementById("draw-rect");
    var stageHint = document.getElementById("stage-hint");
    var addButton = document.getElementById("add-link");
    var propsEmpty = document.getElementById("props-empty");
    var propsForm = document.getElementById("props-form");
    var saveButton = document.getElementById("link-save");
    var deleteButton = document.getElementById("link-delete");
    var typeSelect = document.getElementById("link-type");
    var urlField = document.querySelector("[data-field-url]");
    var targetField = document.querySelector("[data-field-target]");

    var state = {
        page: 1,
        overlays: Array.isArray(data.overlays) ? data.overlays : [],
        selectedId: null,
        pending: null, // unsaved overlay created via draw mode
        drawMode: false,
        drag: null
    };

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

    function showStatus(message, isError) {
        var region = document.getElementById(isError ? "client-error" : "client-status");
        if (!region) return;
        var other = document.getElementById(isError ? "client-status" : "client-error");
        if (other) { other.textContent = ""; other.classList.add("hidden"); }
        region.textContent = message;
        region.classList.remove("hidden");
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

    function isPDFLinkImported(link, page) {
        var rect = { x: link.x, y: link.y, width: link.width, height: link.height };
        return state.overlays.some(function (overlay) {
            return overlay.page === page && overlay.source_rect && rectsMatch(overlay.source_rect, rect);
        });
    }

    /* ---------- rendering ---------- */

    function renderStrip() {
        strip.textContent = "";
        (data.pages || []).forEach(function (page) {
            var button = document.createElement("button");
            button.type = "button";
            button.className = "page-thumb" + (page.number === state.page ? " active" : "");
            button.setAttribute("aria-label", "Page " + page.number);
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
            button.addEventListener("click", function () { selectPage(page.number); });
            strip.appendChild(button);
        });
    }

    function positionStyle(rect) {
        return "left:" + rect.x * 100 + "%;top:" + rect.y * 100 + "%;width:" + rect.width * 100 + "%;height:" + rect.height * 100 + "%";
    }

    function applyStyleVars(element, style) {
        if (!style || typeof style.color !== "string") return;
        var match = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.exec(style.color);
        if (!match) return;
        var hex = match[1];
        if (hex.length === 3) {
            hex = hex.charAt(0) + hex.charAt(0) + hex.charAt(1) + hex.charAt(1) + hex.charAt(2) + hex.charAt(2);
        }
        var r = parseInt(hex.slice(0, 2), 16);
        var g = parseInt(hex.slice(2, 4), 16);
        var b = parseInt(hex.slice(4, 6), 16);
        var opacity = Number(style.opacity);
        if (!isFinite(opacity) || opacity < 0 || opacity > 1) opacity = 0.18;
        element.style.setProperty("--link-fill", "rgba(" + r + "," + g + "," + b + "," + opacity + ")");
        element.style.setProperty("--link-highlight", "rgba(" + r + "," + g + "," + b + "," + Math.min(1, opacity + 0.14) + ")");
        element.style.setProperty("--link-border", style.color);
        var radius = Number(style.radius);
        if (isFinite(radius) && radius >= 0 && radius <= 50) {
            element.style.setProperty("--link-radius", radius + "px");
        }
    }

    function overlayLabel(overlay) {
        if (overlay.type === "internal") return "Page " + overlay.target_page;
        if (overlay.type === "video") return "Video";
        return "URL";
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
            badge.textContent = "PDF";
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
        var label = document.createElement("span");
        label.className = "box-label";
        label.textContent = overlayLabel(overlay);
        box.appendChild(label);
        if (isSelected(overlay)) {
            HANDLES.forEach(function (name) {
                var handle = document.createElement("span");
                handle.className = "resize-handle";
                handle.dataset.handle = name;
                box.appendChild(handle);
            });
        }
        box.addEventListener("mousedown", function (event) { startDrag(event, overlay, unsaved); });
        box.addEventListener("keydown", function (event) {
            if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                selectOverlay(overlay);
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
    }

    function selectPage(number) {
        if (state.pending && state.pending.page === state.page) {
            discardPending();
        }
        state.page = number;
        state.selectedId = null;
        renderStrip();
        renderStage();
        renderProps();
    }

    /* ---------- selection and properties panel ---------- */

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

    function renderProps() {
        var overlay = selectedOverlay();
        propsForm.hidden = !overlay;
        propsEmpty.hidden = Boolean(overlay);
        if (!overlay) return;
        typeSelect.value = overlay.type || "external";
        document.getElementById("link-url").value = overlay.url || "";
        document.getElementById("link-target-page").value = overlay.target_page || "";
        document.getElementById("link-title").value = overlay.title || "";
        document.getElementById("link-enabled").checked = overlay.enabled !== false;
        var style = overlay.style || {};
        document.getElementById("link-color").value = style.color || "";
        document.getElementById("link-opacity").value = style.opacity !== undefined && style.opacity !== null && style.opacity !== 0 ? style.opacity : "";
        document.getElementById("link-radius").value = style.radius || "";
        deleteButton.hidden = !overlay.id;
        saveButton.textContent = overlay.id ? "Save link" : "Create link";
        syncTypeFields();
    }

    function syncTypeFields() {
        var type = typeSelect.value;
        urlField.hidden = type === "internal";
        targetField.hidden = type !== "internal";
    }

    typeSelect.addEventListener("change", syncTypeFields);

    function formPayload(overlay) {
        var type = typeSelect.value;
        var payload = {
            page: overlay.page,
            x: overlay.x, y: overlay.y, width: overlay.width, height: overlay.height,
            type: type,
            url: type === "internal" ? "" : document.getElementById("link-url").value.trim(),
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

    propsForm.addEventListener("submit", async function (event) {
        event.preventDefault();
        if (!propsForm.reportValidity()) return;
        var overlay = selectedOverlay();
        if (!overlay) return;
        var payload = formPayload(overlay);
        saveButton.disabled = true;
        try {
            if (overlay.id) {
                var updated = await api("PATCH", "/" + encodeURIComponent(overlay.id), payload);
                Object.assign(overlay, updated);
                showStatus("Link saved.", false);
            } else {
                var created = await api("POST", "", payload);
                state.overlays.push(created);
                state.pending = null;
                state.selectedId = created.id;
                setDrawMode(false);
                showStatus("Link created.", false);
            }
            renderOverlays();
            renderProps();
        } catch (error) {
            showStatus(error.message, true);
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
            return;
        }
        if (!window.confirm("Delete this link hotspot? The PDF link it replaced, if any, will reappear.")) return;
        deleteButton.disabled = true;
        try {
            await api("DELETE", "/" + encodeURIComponent(overlay.id));
            state.overlays = state.overlays.filter(function (candidate) { return candidate.id !== overlay.id; });
            state.selectedId = null;
            renderOverlays();
            renderProps();
            showStatus("Link deleted.", false);
        } catch (error) {
            showStatus(error.message, true);
        } finally {
            deleteButton.disabled = false;
        }
    });

    /* ---------- PDF link import ---------- */

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
            showStatus("PDF link imported. It is now an editable hotspot.", false);
        } catch (error) {
            showStatus(error.message, true);
        }
    }

    /* ---------- drag, resize, draw ---------- */

    function canvasPoint(event) {
        var rect = canvas.getBoundingClientRect();
        return {
            x: (event.clientX - rect.left) / rect.width,
            y: (event.clientY - rect.top) / rect.height
        };
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
        normalizeRect(rect);
        Object.assign(drag.overlay, rect);
        renderOverlays();
    }

    async function onDragEnd() {
        document.removeEventListener("mousemove", onDragMove);
        var drag = state.drag;
        state.drag = null;
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
            showStatus("Position saved.", false);
        } catch (error) {
            Object.assign(overlay, drag.origin);
            showStatus(error.message, true);
        }
        renderOverlays();
    }

    function setDrawMode(active) {
        state.drawMode = active;
        stage.classList.toggle("drawing", active);
        addButton.setAttribute("aria-pressed", String(active));
        stageHint.textContent = active
            ? "Draw mode: drag a rectangle on the page to create a link hotspot. Press Escape to cancel."
            : "Select a page on the left. Dashed boxes are links embedded in the PDF; solid boxes are custom hotspots.";
    }

    addButton.addEventListener("click", function () {
        setDrawMode(!state.drawMode);
    });

    document.addEventListener("keydown", function (event) {
        if (event.key === "Escape" && state.drawMode) {
            setDrawMode(false);
        }
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
        var rect = normalizeRect({
            x: Math.min(drawStart.x, point.x),
            y: Math.min(drawStart.y, point.y),
            width: Math.abs(point.x - drawStart.x),
            height: Math.abs(point.y - drawStart.y)
        });
        drawRect.hidden = false;
        drawRect.style.cssText = positionStyle(rect);
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
        setDrawMode(false);
        if (rect.width < MIN_SIZE || rect.height < MIN_SIZE) {
            showStatus("The drawn area was too small. Drag a larger rectangle.", true);
            return;
        }
        normalizeRect(rect);
        state.pending = {
            page: state.page,
            x: rect.x, y: rect.y, width: rect.width, height: rect.height,
            type: "external", url: "", title: "", enabled: true
        };
        state.selectedId = null;
        renderOverlays();
        renderProps();
        document.getElementById("link-url").focus();
        showStatus("New hotspot drawn. Fill in the link settings and choose Create link.", false);
    }

    function discardPending() {
        state.pending = null;
    }

    /* ---------- boot ---------- */

    renderStrip();
    renderStage();
    renderProps();
    setDrawMode(false);
})();
