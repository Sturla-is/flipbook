(() => {
    'use strict';

    const globalStatus = document.getElementById('integration-global-status');

    function announce(message) {
        if (globalStatus) {
            globalStatus.textContent = message || '';
        }
    }

    function formParameters(form) {
        const parameters = new URLSearchParams();
        for (const [key, value] of new FormData(form).entries()) {
            if (typeof value === 'string') {
                parameters.append(key, value);
            }
        }
        return parameters;
    }

    async function submitForm(form) {
        const method = (form.getAttribute('method') || 'get').toUpperCase();
        const parameters = formParameters(form);
        let target = new URL(form.action, window.location.origin);
        const headers = {
            'Accept': 'application/json',
            'X-Requested-With': 'XMLHttpRequest'
        };
        const options = { method, headers, credentials: 'same-origin' };
        if (method === 'GET') {
            target.search = parameters.toString();
        } else {
            headers['Content-Type'] = 'application/x-www-form-urlencoded;charset=UTF-8';
            const csrf = form.querySelector('input[name="csrf_token"]');
            if (csrf && csrf.value) {
                headers['X-CSRF-Token'] = csrf.value;
            }
            options.body = parameters.toString();
        }
        const response = await fetch(target.toString(), options);
        const text = await response.text();
        let data = {};
        if (text) {
            try {
                data = JSON.parse(text);
            } catch (_) {
                throw new Error('The server returned an unreadable integration response.');
            }
        }
        if (!response.ok) {
            const error = new Error(data.error || `The integration request failed (HTTP ${response.status}).`);
            error.result = data.result || null;
            throw error;
        }
        return data;
    }

    function setBusy(form, busy, busyLabel) {
        form.dataset.busy = busy ? 'true' : 'false';
        for (const button of form.querySelectorAll('button[type="submit"], input[type="submit"]')) {
            if (busy) {
                button.dataset.originalValue = button.tagName === 'INPUT' ? button.value : button.textContent;
                button.disabled = true;
                if (button.tagName === 'INPUT') {
                    button.value = busyLabel;
                } else {
                    button.textContent = busyLabel;
                }
                button.setAttribute('aria-busy', 'true');
            } else {
                button.disabled = false;
                if (button.tagName === 'INPUT') {
                    button.value = button.dataset.originalValue || button.value;
                } else {
                    button.textContent = button.dataset.originalValue || button.textContent;
                }
                button.removeAttribute('aria-busy');
            }
        }
    }

    function showResult(container, message, isError) {
        if (!container) {
            return;
        }
        container.replaceChildren();
        container.hidden = false;
        container.classList.toggle('is-error', Boolean(isError));
        container.setAttribute('role', isError ? 'alert' : 'status');
        const paragraph = document.createElement('p');
        paragraph.textContent = message;
        container.appendChild(paragraph);
        container.setAttribute('tabindex', '-1');
        container.focus({ preventScroll: true });
    }

    function resultLink(container, href, label) {
        if (!container || !href) {
            return;
        }
        const link = document.createElement('a');
        link.href = href;
        link.textContent = label;
        container.appendChild(link);
    }

    function redirectWithNotice(message, warning) {
        const target = new URL('/admin/integrations', window.location.origin);
        if (message) {
            target.searchParams.set('notice', message);
        }
        if (warning) {
            target.searchParams.set('warning', warning);
        }
        window.location.assign(target.toString());
    }

    for (const form of document.querySelectorAll('.js-integration-action')) {
        form.addEventListener('submit', async (event) => {
            event.preventDefault();
            if (form.dataset.busy === 'true') {
                return;
            }
            const confirmation = form.dataset.confirm;
            if (confirmation && !window.confirm(confirmation)) {
                return;
            }
            setBusy(form, true, 'Working…');
            announce('Integration action in progress.');
            try {
                const data = await submitForm(form);
                if (data.disconnected) {
                    redirectWithNotice('Provider disconnected.', data.warning || '');
                    return;
                }
                if (data.verified) {
                    redirectWithNotice('Provider connection verified.', '');
                    return;
                }
                if (typeof data.enabled === 'boolean') {
                    redirectWithNotice('Webhook subscription updated.', '');
                    return;
                }
                if (data.event) {
                    redirectWithNotice(`Test event queued for ${data.queued || 0} subscription(s).`, '');
                    return;
                }
                redirectWithNotice('Integration action completed.', '');
            } catch (error) {
                announce(error.message);
                window.alert(error.message);
                setBusy(form, false, 'Working…');
            }
        });
    }

    for (const form of document.querySelectorAll('.js-export-form')) {
        form.addEventListener('submit', async (event) => {
            event.preventDefault();
            if (form.dataset.busy === 'true') {
                return;
            }
            const result = form.parentElement.querySelector('.js-export-result');
            setBusy(form, true, 'Exporting…');
            showResult(result, 'Exporting the document and waiting for the provider job. Keep this page open.', false);
            try {
                const data = await submitForm(form);
                showResult(result, `Revision ${data.revision_id} was saved and queued for conversion. `, false);
                resultLink(result, data.admin_url, 'Open the publication');
                announce('Provider export queued for conversion.');
                if (Array.isArray(data.warnings) && data.warnings.length) {
                    const warning = document.createElement('p');
                    warning.textContent = data.warnings.join(' ');
                    result.appendChild(warning);
                }
            } catch (error) {
                showResult(result, error.message, true);
                if (error.result && error.result.admin_url) {
                    resultLink(result, error.result.admin_url, 'Open the saved publication revision');
                }
            } finally {
                setBusy(form, false, 'Exporting…');
            }
        });
    }

    for (const form of document.querySelectorAll('.js-statistics-form')) {
        form.addEventListener('submit', async (event) => {
            event.preventDefault();
            if (form.dataset.busy === 'true') {
                return;
            }
            const result = form.parentElement.querySelector('.js-statistics-result');
            setBusy(form, true, 'Loading…');
            showResult(result, 'Loading provider statistics and metadata…', false);
            try {
                const data = await submitForm(form);
                result.replaceChildren();
                const output = document.createElement('pre');
                output.className = 'integration-statistics';
                output.tabIndex = 0;
                output.textContent = JSON.stringify(data, null, 2);
                result.appendChild(output);
                result.hidden = false;
                result.setAttribute('role', 'status');
                output.focus({ preventScroll: true });
                announce('Provider statistics loaded.');
            } catch (error) {
                showResult(result, error.message, true);
            } finally {
                setBusy(form, false, 'Loading…');
            }
        });
    }

    async function copyText(value) {
        if (navigator.clipboard && window.isSecureContext) {
            await navigator.clipboard.writeText(value);
            return;
        }
        const input = document.createElement('textarea');
        input.value = value;
        input.setAttribute('readonly', '');
        input.className = 'integration-sr-only';
        document.body.appendChild(input);
        input.select();
        const copied = document.execCommand('copy');
        input.remove();
        if (!copied) {
            throw new Error('Copy was not available. Select the secret and copy it manually.');
        }
    }

    for (const button of document.querySelectorAll('[data-copy-target]')) {
        button.addEventListener('click', async () => {
            const target = document.getElementById(button.dataset.copyTarget);
            if (!target) {
                return;
            }
            const original = button.textContent;
            try {
                await copyText(target.textContent);
                button.textContent = 'Copied';
                announce('Webhook signing secret copied.');
            } catch (error) {
                button.textContent = 'Copy failed';
                announce(error.message);
            }
            window.setTimeout(() => {
                button.textContent = original;
            }, 2000);
        });
    }
})();
