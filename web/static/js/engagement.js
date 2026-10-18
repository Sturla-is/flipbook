(function () {
    "use strict";

    function csrfHeaders(form, json) {
        var headers = { "Accept": "application/json", "X-Requested-With": "XMLHttpRequest" };
        var token = form.querySelector('input[name="csrf_token"]');
        if (token && token.value) {
            headers["X-CSRF-Token"] = token.value;
        }
        if (json) {
            headers["Content-Type"] = "application/json";
        }
        return headers;
    }

    function setStatus(form, message, isError) {
        var region = form.querySelector("[data-engagement-status]");
        if (!region) {
            region = document.createElement("div");
            region.className = "engagement-status";
            region.setAttribute("role", "status");
            region.setAttribute("aria-live", "polite");
            form.prepend(region);
        }
        region.textContent = message;
        region.setAttribute("data-error", isError ? "true" : "false");
    }

    function setBusy(form, busy) {
        form.querySelectorAll("button, input, select, textarea").forEach(function (control) {
            if (control.type === "hidden") {
                return;
            }
            if (busy) {
                control.setAttribute("data-was-disabled", control.disabled ? "true" : "false");
                control.disabled = true;
            } else if (control.getAttribute("data-was-disabled") !== "true") {
                control.disabled = false;
            }
        });
    }

    async function responseJSON(response) {
        var data = {};
        try {
            data = await response.json();
        } catch (_) {
            data = {};
        }
        if (!response.ok) {
            throw new Error(data.error || "The request could not be completed (" + response.status + ").");
        }
        return data;
    }

    function value(form, name) {
        var field = form.elements.namedItem(name);
        return field && typeof field.value === "string" ? field.value : "";
    }

    function checked(form, name) {
        var field = form.elements.namedItem(name);
        return Boolean(field && field.checked);
    }

    function leadPayload(form) {
        return {
            email: value(form, "email"),
            name: value(form, "name"),
            source: value(form, "source") || "lead_gate",
            consent: checked(form, "consent")
        };
    }

    function subscriptionPayload(form) {
        return {
            email: value(form, "email"),
            source: value(form, "source") || "catalog",
            consent: checked(form, "consent")
        };
    }

    function enhanceJSONForm(form, kind) {
        form.addEventListener("submit", async function (event) {
            event.preventDefault();
            if (!form.reportValidity()) {
                return;
            }
            setStatus(form, "Submitting…", false);
            setBusy(form, true);
            try {
                var response = await fetch(form.action, {
                    method: "POST",
                    credentials: "same-origin",
                    headers: csrfHeaders(form, true),
                    body: JSON.stringify(kind === "lead" ? leadPayload(form) : subscriptionPayload(form))
                });
                var data = await responseJSON(response);
                var message = data.message || (kind === "lead" ? "Access unlocked." : "Check your inbox to confirm your subscription.");
                setStatus(form, message, false);
                var eventName = kind === "lead" ? "engagement:lead-captured" : "engagement:subscription-pending";
                form.dispatchEvent(new CustomEvent(eventName, { bubbles: true, detail: data }));
                if (kind === "lead") {
                    closeGate(form.closest("[data-engagement-gate]"), true);
                }
            } catch (error) {
                setStatus(form, error.message || "A network error occurred. Try again.", true);
            } finally {
                setBusy(form, false);
            }
        });
    }

    document.querySelectorAll("[data-engagement-lead]").forEach(function (form) {
        enhanceJSONForm(form, "lead");
    });
    document.querySelectorAll("[data-engagement-subscribe]").forEach(function (form) {
        enhanceJSONForm(form, "subscription");
    });

    document.querySelectorAll("[data-engagement-dismiss]").forEach(function (form) {
        form.addEventListener("submit", async function (event) {
            event.preventDefault();
            setBusy(form, true);
            try {
                var response = await fetch(form.action, {
                    method: "POST",
                    credentials: "same-origin",
                    headers: csrfHeaders(form, false),
                    body: new URLSearchParams({ return_to: value(form, "return_to") })
                });
                var data = await responseJSON(response);
                form.dispatchEvent(new CustomEvent("engagement:lead-dismissed", { bubbles: true, detail: data }));
                closeGate(form.closest("[data-engagement-gate]"), true);
                var redirect = value(form, "return_to");
                if (!form.closest("[data-engagement-gate]") && redirect) {
                    window.location.assign(redirect);
                }
            } catch (error) {
                var gate = form.closest("[data-engagement-gate]");
                var target = gate ? gate.querySelector("[data-engagement-lead]") : form;
                setStatus(target, error.message || "The gate could not be dismissed.", true);
            } finally {
                setBusy(form, false);
            }
        });
    });

    var activeGate = null;
    var previousFocus = null;

    function focusable(gate) {
        return Array.from(gate.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex="0"]')).filter(function (element) {
            return element.getClientRects().length > 0;
        });
    }

    function showLeadGate(target) {
        var gate = typeof target === "string" ? document.querySelector(target) : target;
        if (!gate && !activeGate) {
            gate = document.querySelector("[data-engagement-gate]");
        }
        if (!gate) {
            return false;
        }
        if (activeGate && activeGate !== gate) {
            closeGate(activeGate, true);
        }
        if (!activeGate) {
            previousFocus = document.activeElement;
        }
        activeGate = gate;
        gate.hidden = false;
        document.body.classList.add("engagement-gate-open");
        var controls = focusable(gate);
        var panel = gate.querySelector("[data-engagement-gate-panel]");
        (controls[0] || panel || gate).focus();
        gate.dispatchEvent(new CustomEvent("engagement:gate-open", { bubbles: true }));
        return true;
    }

    function closeGate(gate, completed) {
        gate = gate || activeGate;
        if (!gate) {
            return false;
        }
        if (!completed && gate.getAttribute("data-dismissible") !== "true") {
            return false;
        }
        gate.hidden = true;
        if (activeGate === gate) {
            activeGate = null;
            document.body.classList.remove("engagement-gate-open");
            if (previousFocus && typeof previousFocus.focus === "function" && document.contains(previousFocus)) {
                previousFocus.focus();
            }
            previousFocus = null;
        }
        gate.dispatchEvent(new CustomEvent("engagement:gate-close", { bubbles: true, detail: { completed: Boolean(completed) } }));
        return true;
    }

    document.addEventListener("keydown", function (event) {
        if (!activeGate) {
            return;
        }
        if (event.key === "Escape") {
            if (closeGate(activeGate, false)) {
                event.preventDefault();
            }
            return;
        }
        if (event.key !== "Tab") {
            return;
        }
        var controls = focusable(activeGate);
        if (!controls.length) {
            event.preventDefault();
            return;
        }
        var first = controls[0];
        var last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) {
            event.preventDefault();
            last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault();
            first.focus();
        }
    });

    window.FlipbookEngagement = Object.freeze({
        showLeadGate: showLeadGate,
        hideLeadGate: function () { return closeGate(activeGate, false); },
        isLeadGateOpen: function () { return Boolean(activeGate); }
    });

    document.querySelectorAll("form[data-confirm]").forEach(function (form) {
        form.addEventListener("submit", function (event) {
            if (!window.confirm(form.getAttribute("data-confirm") || "Continue with this destructive action?")) {
                event.preventDefault();
            }
        });
    });

    document.querySelectorAll("[data-copy-target]").forEach(function (button) {
        button.addEventListener("click", async function () {
            var input = document.getElementById(button.getAttribute("data-copy-target"));
            if (!input) {
                return;
            }
            var copied = false;
            try {
                await navigator.clipboard.writeText(input.value);
                copied = true;
            } catch (_) {
                input.focus();
                input.select();
                copied = document.execCommand("copy");
            }
            var status = button.parentElement.parentElement.querySelector("[data-copy-status]");
            if (status) {
                status.textContent = copied ? "Copied." : "Copy failed. Select the secret and copy it manually.";
            }
        });
    });
})();
