(function () {
    'use strict';

    function ready(callback) {
        if (document.readyState === 'loading') {
            document.addEventListener('DOMContentLoaded', callback);
        } else {
            callback();
        }
    }

    ready(function () {
        var checkout = document.querySelector('[data-sales-checkout]');
        if (checkout) {
            checkout.addEventListener('submit', async function (event) {
                event.preventDefault();
                var button = checkout.querySelector('button[type="submit"]');
                var status = document.getElementById('sales-checkout-status');
                var email = checkout.elements.email;
                if (!email || !email.checkValidity()) {
                    if (email) email.reportValidity();
                    return;
                }
                button.disabled = true;
                status.textContent = 'Preparing secure checkout…';
                var headers = {
                    'Content-Type': 'application/json',
                    'Accept': 'application/json'
                };
                var csrf = checkout.querySelector('input[name="csrf_token"]');
                if (csrf && csrf.value) headers['X-CSRF-Token'] = csrf.value;
                try {
                    var response = await fetch(checkout.action, {
                        method: 'POST',
                        credentials: 'same-origin',
                        headers: headers,
                        body: JSON.stringify({email: email.value.trim()})
                    });
                    var data = await response.json().catch(function () { return {}; });
                    if (!response.ok || !data.checkout_url) {
                        throw new Error(data.error || 'Checkout could not be started. Please try again.');
                    }
                    var destination = new URL(data.checkout_url, window.location.href);
                    if (destination.protocol !== 'https:' && destination.protocol !== 'http:') {
                        throw new Error('Checkout returned an invalid destination.');
                    }
                    status.textContent = 'Redirecting to secure checkout…';
                    window.location.assign(destination.href);
                } catch (error) {
                    button.disabled = false;
                    status.textContent = error.message || 'Checkout could not be started. Please try again.';
                    status.setAttribute('role', 'alert');
                }
            });
        }

        document.querySelectorAll('[data-confirm]').forEach(function (form) {
            form.addEventListener('submit', function (event) {
                if (!window.confirm(form.getAttribute('data-confirm'))) {
                    event.preventDefault();
                }
            });
        });

        document.querySelectorAll('[data-sales-mutation]').forEach(function (form) {
            form.addEventListener('submit', function () {
                var button = form.querySelector('button[type="submit"]');
                if (button) {
                    button.disabled = true;
                    button.setAttribute('aria-busy', 'true');
                }
            });
        });
    });
}());
