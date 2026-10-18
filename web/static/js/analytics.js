(function () {
    'use strict';

    var app = document.getElementById('analytics-app');
    if (!app) return;

    var form = document.getElementById('analytics-filters');
    var status = document.getElementById('analytics-status');
    var preset = document.getElementById('analytics-preset');
    var activeRequest = null;
    var number = new Intl.NumberFormat();

    function announce(message, isError) {
        status.textContent = message;
        status.classList.toggle('analytics-error', Boolean(isError));
        status.setAttribute('role', isError ? 'alert' : 'status');
    }

    function formatDuration(value) {
        var seconds = Math.max(0, Math.round(Number(value) || 0));
        if (seconds < 60) return seconds + 's';
        if (seconds < 3600) return Math.floor(seconds / 60) + 'm ' + (seconds % 60) + 's';
        if (seconds < 86400) return Math.floor(seconds / 3600) + 'h ' + (Math.floor((seconds % 3600) / 60)) + 'm';
        return Math.floor(seconds / 86400) + 'd ' + (Math.floor((seconds % 86400) / 3600)) + 'h';
    }

    function formatPercent(value) {
        return ((Number(value) || 0) * 100).toFixed(1) + '%';
    }

    function cell(value, header) {
        var element = document.createElement(header ? 'th' : 'td');
        if (header) element.scope = 'row';
        if (value && value.href) {
            var link = document.createElement('a');
            link.href = value.href;
            link.textContent = value.text;
            element.appendChild(link);
        } else {
            element.textContent = String(value == null ? '' : value);
        }
        return element;
    }

    function emptyRow(colspan, message) {
        var row = document.createElement('tr');
        var value = cell(message || 'No data in this range.', false);
        value.colSpan = colspan;
        value.className = 'analytics-empty';
        row.appendChild(value);
        return row;
    }

    function renderRows(tbody, rows, columns, emptyMessage) {
        tbody.replaceChildren();
        if (!rows || rows.length === 0) {
            tbody.appendChild(emptyRow(columns.length, emptyMessage));
            return;
        }
        rows.forEach(function (item) {
            var row = document.createElement('tr');
            columns.forEach(function (column, index) {
                row.appendChild(cell(column(item), index === 0));
            });
            tbody.appendChild(row);
        });
    }

    function renderMetrics(totals) {
        document.querySelectorAll('[data-metric]').forEach(function (element) {
            var key = element.getAttribute('data-metric');
            var value = Number(totals[key]) || 0;
            if (element.getAttribute('data-format') === 'duration') {
                element.textContent = formatDuration(value);
            } else if (element.getAttribute('data-format') === 'percent') {
                element.textContent = formatPercent(value);
            } else {
                element.textContent = number.format(value);
            }
        });
        var legacy = document.querySelector('[data-legacy]');
        if (legacy) legacy.textContent = number.format(totals.legacy_views || 0);
    }

    function renderReport(report) {
        var totals = report.totals || {};
        renderMetrics(totals);

        renderRows(document.querySelector('[data-table="publications"]'), report.publications || [], [
            function (item) { return { text: item.publication.title, href: '/admin/flipbooks/' + encodeURIComponent(item.publication.id) + '/analytics' }; },
            function (item) { return number.format(item.totals.views); },
            function (item) { return number.format(item.totals.qualified_reads); },
            function (item) { return number.format(item.totals.sessions); },
            function (item) { return formatDuration(item.totals.active_seconds); },
            function (item) { return formatPercent(item.totals.completion_rate); },
            function (item) { return number.format(item.totals.legacy_views); }
        ], 'No publications are available.');

        renderRows(document.querySelector('[data-table="trends"]'), report.trends || [], [
            function (item) { return item.date; },
            function (item) { return number.format(item.views); },
            function (item) { return number.format(item.qualified_reads); },
            function (item) { return number.format(item.sessions); },
            function (item) { return formatDuration(item.active_seconds); },
            function (item) { return number.format(item.completions); }
        ]);

        renderRows(document.querySelector('[data-table="pages"]'), report.pages || [], [
            function (item) { return item.page; },
            function (item) { return number.format(item.page_views); },
            function (item) { return number.format(item.sessions); },
            function (item) { return formatPercent(item.retention_rate); },
            function (item) { return number.format(item.drop_off) + ' (' + formatPercent(item.drop_off_rate) + ')'; },
            function (item) { return formatDuration(item.active_seconds); },
            function (item) { return number.format(item.furthest); },
            function (item) { return number.format(item.completions); }
        ], 'No page activity in this range.');

        renderRows(document.querySelector('[data-table="funnel"]'), report.funnel || [], [
            function (item) { return item.name; },
            function (item) { return number.format(item.events); },
            function (item) { return number.format(item.sessions); }
        ]);

        document.querySelectorAll('[data-breakdown]').forEach(function (tbody) {
            var dimension = tbody.getAttribute('data-breakdown');
            var rows = report.breakdowns && report.breakdowns[dimension] ? report.breakdowns[dimension] : [];
            renderRows(tbody, rows, [
                function (item) { return item.key; },
                function (item) { return number.format(item.views); },
                function (item) { return number.format(item.qualified_reads); },
                function (item) { return number.format(item.sessions); },
                function (item) { return formatDuration(item.active_seconds); },
                function (item) { return number.format(item.events); }
            ]);
        });
        renderFunnels(report.funnels || []);
    }

    function renderFunnels(funnels) {
        var container = document.querySelector('[data-funnels]');
        container.replaceChildren();
        funnels.forEach(function (funnel) {
            var section = document.createElement('section');
            section.className = 'analytics-subcard';
            var heading = document.createElement('h3');
            heading.textContent = funnel.key;
            section.appendChild(heading);
            var table = document.createElement('table');
            table.className = 'analytics-table';
            var thead = document.createElement('thead');
            var headRow = document.createElement('tr');
            ['Stage', 'Events', 'Sessions'].forEach(function (label) {
                var th = document.createElement('th');
                th.scope = 'col';
                th.textContent = label;
                headRow.appendChild(th);
            });
            thead.appendChild(headRow);
            table.appendChild(thead);
            var tbody = document.createElement('tbody');
            table.appendChild(tbody);
            section.appendChild(table);
            container.appendChild(section);
            renderRows(tbody, funnel.steps || [], [
                function (item) { return item.name; },
                function (item) { return number.format(item.events); },
                function (item) { return number.format(item.sessions); }
            ]);
        });
    }

    function currentQuery() {
        return new URLSearchParams(new FormData(form));
    }

    function updateExports(query) {
        var suffix = '?' + query.toString();
        var paths = {
            'report-json': '/admin/analytics/export.json',
            'report-csv': '/admin/analytics/export.csv',
            'events-json': '/admin/analytics/events.json',
            'events-csv': '/admin/analytics/events.csv'
        };
        Object.keys(paths).forEach(function (key) {
            var link = document.querySelector('[data-export="' + key + '"]');
            if (link) link.href = paths[key] + suffix;
        });
    }

    function loadReport(query, updateHistory) {
        if (activeRequest) activeRequest.abort();
        activeRequest = new AbortController();
        announce('Loading analytics…', false);
        fetch(app.getAttribute('data-report-url') + '?' + query.toString(), {
            headers: { 'Accept': 'application/json' },
            credentials: 'same-origin',
            signal: activeRequest.signal
        }).then(function (response) {
            return response.json().catch(function () { return {}; }).then(function (body) {
                if (!response.ok) throw new Error(body.error || 'Analytics could not be loaded (' + response.status + ').');
                return body;
            });
        }).then(function (report) {
            renderReport(report);
            updateExports(query);
            if (updateHistory) {
                history.pushState({ analytics: true }, '', form.action + '?' + query.toString());
            }
            announce('Report updated.', false);
        }).catch(function (error) {
            if (error.name !== 'AbortError') announce(error.message, true);
        });
    }

    form.addEventListener('submit', function (event) {
        event.preventDefault();
        if (!form.reportValidity()) return;
        loadReport(currentQuery(), true);
    });

    preset.addEventListener('change', function () {
        var days = Number(preset.value);
        if (!days) return;
        var now = new Date();
        var to = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
        var from = new Date(to.getTime());
        from.setUTCDate(from.getUTCDate() - days + 1);
        document.getElementById('analytics-from').value = from.toISOString().slice(0, 10);
        document.getElementById('analytics-to').value = to.toISOString().slice(0, 10);
        if (typeof form.requestSubmit === 'function') form.requestSubmit();
    });

    window.addEventListener('popstate', function () {
        var query = new URLSearchParams(window.location.search);
        ['from', 'to', 'flipbook_id'].forEach(function (name) {
            var field = form.elements.namedItem(name);
            if (field) field.value = query.get(name) || '';
        });
        loadReport(query, false);
    });
}());
