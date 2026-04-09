// AWS RHSA/CVE Compliance Check JavaScript
(function() {
    'use strict';

    const ENDPOINT = '/aws/rhsa-compliance';
    const RHSA_RE = /^RHSA-\d{4}:\d+$/;
    const CVE_RE  = /^CVE-\d{4}-\d{4,}$/;

    let checkType = 'rhsa';   // 'rhsa' | 'cve'
    let currentBatchId = null;
    let compliancePolling = null;
    let instanceSelector = null;

    // ── Helpers ───────────────────────────────────────────────────────────────

    function el(tag, cls, text) {
        const e = document.createElement(tag);
        if (cls) e.className = cls;
        if (text !== undefined) e.textContent = text;
        return e;
    }

    function append(parent) {
        for (let i = 1; i < arguments.length; i++) {
            if (arguments[i]) parent.appendChild(arguments[i]);
        }
        return parent;
    }

    function activeRe() {
        return checkType === 'cve' ? CVE_RE : RHSA_RE;
    }

    // ── Init ──────────────────────────────────────────────────────────────────

    document.addEventListener('DOMContentLoaded', function() {
        instanceSelector = new InstanceSelector('instance-selector-body');
        instanceSelector.onSelectionChange(function() {
            updateLaunchButton();
        });

        if (window.currentChangeData) {
            instanceSelector.setInstances(window.currentChangeData.instances || []);
            updateLaunchButton();
        }
    });

    // ── Mode toggle ───────────────────────────────────────────────────────────

    window.switchMode = function(mode) {
        checkType = mode;
        const textarea = document.getElementById('advisory-input');
        const label    = document.getElementById('input-label');
        const hint     = document.getElementById('input-hint');

        // Clear any previous input and validation message when switching modes
        textarea.value = '';
        document.getElementById('advisory-validation-msg').replaceChildren();

        if (mode === 'cve') {
            textarea.placeholder = 'CVE-2024-12345\nCVE-2024-67890\nCVE-2023-44487\n\nOne CVE ID per line. Blank lines and duplicates are ignored.';
            label.textContent = 'CVE IDs';
            hint.textContent = 'Enter one CVE ID per line. Format: CVE-YYYY-NNNNN';
        } else {
            textarea.placeholder = 'RHSA-2024:0001\nRHSA-2024:0002\nRHSA-2023:7856\n\nOne RHSA ID per line. Blank lines and duplicates are ignored.';
            label.textContent = 'RHSA Advisory IDs';
            hint.textContent = 'Enter one RHSA advisory ID per line. Format: RHSA-YYYY:NNNNN';
        }

        updateLaunchButton();
    };

    // ── Input parsing / dedup ─────────────────────────────────────────────────

    function parseAdvisoryInput(text) {
        const re = activeRe();
        const seen = new Set();
        const ids = [];
        text.split(/[\n,]+/).forEach(function(s) {
            const id = s.trim().toUpperCase();
            if (re.test(id) && !seen.has(id)) {
                seen.add(id);
                ids.push(id);
            }
        });
        return ids;
    }

    // ── Launch button state ───────────────────────────────────────────────────

    window.updateLaunchButton = function() {
        const btn      = document.getElementById('launch-btn');
        const statusEl = document.getElementById('launch-status');
        if (!btn || !statusEl) return;

        const text = (document.getElementById('advisory-input').value || '').trim();
        const ids  = parseAdvisoryInput(text);
        const sel  = instanceSelector ? instanceSelector.getSelected() : [];
        const kind = checkType === 'cve' ? 'CVE' : 'RHSA';

        if (ids.length > 0 && sel.length > 0) {
            btn.disabled = false;
            statusEl.textContent = 'Ready: ' + ids.length + ' ' + kind + ' IDs on ' + sel.length + ' instance(s)';
            statusEl.className = 'text-success small';
        } else if (ids.length === 0) {
            btn.disabled = true;
            statusEl.textContent = 'Enter ' + kind + ' IDs above';
            statusEl.className = 'text-muted small';
        } else {
            btn.disabled = true;
            statusEl.textContent = 'No instances selected — select at least one above';
            statusEl.className = 'text-warning small';
        }
    };

    // ── Validate / Clear / Import ─────────────────────────────────────────────

    window.validateInput = function() {
        const text  = (document.getElementById('advisory-input').value || '').trim();
        const msgEl = document.getElementById('advisory-validation-msg');
        if (!msgEl) return;
        msgEl.replaceChildren();

        if (!text) {
            msgEl.appendChild(el('div', 'alert alert-warning py-2', 'No input provided.'));
            return;
        }

        const re    = activeRe();
        const kind  = checkType === 'cve' ? 'CVE' : 'RHSA';
        const lines = text.split(/[\n,]+/).map(function(s) { return s.trim(); }).filter(Boolean);
        const valid = lines.filter(function(s) { return re.test(s.toUpperCase()); });
        const invalid = lines.filter(function(s) { return !re.test(s.toUpperCase()); });
        const dupes = lines.length - new Set(valid.map(function(s) { return s.toUpperCase(); })).size;

        const a = el('div', invalid.length === 0 ? 'alert alert-success py-2' : 'alert alert-warning py-2');

        if (invalid.length === 0) {
            let msg = valid.length + ' valid ' + kind + ' ID' + (valid.length !== 1 ? 's' : '');
            if (dupes > 0) msg += ' (' + dupes + ' duplicate' + (dupes > 1 ? 's' : '') + ' will be ignored)';
            a.textContent = msg;
        } else {
            const summary = el('div');
            summary.textContent = valid.length + ' valid, ' + invalid.length + ' invalid. Invalid: ';
            invalid.slice(0, 5).forEach(function(s, i) {
                if (i > 0) summary.appendChild(document.createTextNode(', '));
                const code = el('code');
                code.textContent = s;
                summary.appendChild(code);
            });
            if (invalid.length > 5) {
                summary.appendChild(document.createTextNode(' and ' + (invalid.length - 5) + ' more'));
            }
            const fmt = checkType === 'cve' ? 'CVE-YYYY-NNNNN' : 'RHSA-YYYY:NNNNN';
            const hint = el('div', 'small mt-1', 'Expected format: ' + fmt);
            append(a, summary, hint);
        }

        msgEl.appendChild(a);
    };

    window.clearInput = function() {
        document.getElementById('advisory-input').value = '';
        document.getElementById('advisory-validation-msg').replaceChildren();
        updateLaunchButton();
    };

    window.importAdvisoryCsv = function(input) {
        const file = input.files && input.files[0];
        if (!file) return;
        const re = activeRe();
        const reader = new FileReader();
        reader.onload = function(e) {
            const text = e.target.result || '';
            const seen = new Set();
            const ids = [];
            text.split(/[\n,;]+/).forEach(function(s) {
                const id = s.replace(/['"]/g, '').trim().toUpperCase();
                if (re.test(id) && !seen.has(id)) {
                    seen.add(id);
                    ids.push(id);
                }
            });
            if (ids.length === 0) {
                showToast('No valid ' + (checkType === 'cve' ? 'CVE' : 'RHSA') + ' IDs found in file', 'warning');
                return;
            }
            const existing = (document.getElementById('advisory-input').value || '').trim();
            document.getElementById('advisory-input').value = existing ? existing + '\n' + ids.join('\n') : ids.join('\n');
            updateLaunchButton();
            showToast('Imported ' + ids.length + ' IDs from file', 'success');
        };
        reader.readAsText(file);
        input.value = '';
    };

    // ── Connectivity Test ─────────────────────────────────────────────────────

    window.testConnectivity = async function() {
        const selected = instanceSelector ? instanceSelector.getSelected() : [];
        if (selected.length === 0) { showToast('No instances selected', 'warning'); return; }

        const resultsDiv = document.getElementById('connectivity-results');
        const testBtn    = document.getElementById('test-connectivity-btn');
        testBtn.disabled = true;
        testBtn.textContent = '';
        testBtn.appendChild(el('span', 'spinner-border spinner-border-sm me-2'));
        testBtn.appendChild(document.createTextNode('Confirming...'));
        resultsDiv.className = 'alert alert-info';
        resultsDiv.textContent = 'Confirming connectivity to all instances...';

        try {
            const resp = await fetch(ENDPOINT + '/test-connectivity', {
                method: 'POST',
                headers: Object.assign({'Content-Type': 'application/json'},
                    (window.Utils && window.Utils.getCsrfToken()) ? {'X-CSRFToken': window.Utils.getCsrfToken()} : {}),
                body: JSON.stringify({instance_ids: selected.map(function(i) { return i.instance_id; })}),
                credentials: 'same-origin',
            });

            resultsDiv.replaceChildren();

            if (resp.ok) {
                const data    = await resp.json();
                const results = data.results || [];
                const accessible = results.filter(function(r) { return r.accessible; }).length;
                const total      = results.length;
                const isGood     = accessible === total;
                const isPartial  = accessible > 0 && accessible < total;

                resultsDiv.className = 'alert ' + (isGood ? 'alert-success' : isPartial ? 'alert-warning' : 'alert-danger');
                resultsDiv.appendChild(el('div', 'mb-2 fw-bold', accessible + ' of ' + total + ' instances accessible'));

                const list = el('div', 'small');
                results.forEach(function(r) {
                    list.appendChild(el('div', null,
                        (r.accessible ? '✅ ' : '❌ ') + r.instance_id + ': ' +
                        (r.accessible ? 'Accessible' : (r.error || 'Not accessible'))));
                });
                resultsDiv.appendChild(list);
            } else {
                const errData = await resp.json().catch(function() { return {detail: 'Connectivity test failed'}; });
                resultsDiv.className = 'alert alert-danger';
                resultsDiv.textContent = 'Error: ' + (errData.detail || 'Unknown error');
            }
        } catch (err) {
            resultsDiv.className = 'alert alert-danger';
            resultsDiv.textContent = 'Error: ' + err.message;
        } finally {
            testBtn.disabled = false;
            testBtn.textContent = '';
            append(testBtn, el('i', 'bi bi-wifi me-2'));
            testBtn.appendChild(document.createTextNode('Confirm Connectivity'));
        }
    };

    // ── Launch ────────────────────────────────────────────────────────────────

    window.launchCompliance = async function() {
        const text = (document.getElementById('advisory-input').value || '').trim();
        const ids  = parseAdvisoryInput(text);

        if (ids.length === 0) {
            showToast('No valid ' + (checkType === 'cve' ? 'CVE' : 'RHSA') + ' IDs entered', 'warning');
            return;
        }

        const selected = instanceSelector ? instanceSelector.getSelected() : [];
        if (selected.length === 0) { showToast('No instances selected', 'warning'); return; }

        const btn = document.getElementById('launch-btn');
        btn.disabled = true;
        btn.textContent = '';
        append(btn, el('span', 'spinner-border spinner-border-sm me-2'));
        btn.appendChild(document.createTextNode('Starting...'));

        try {
            const resp = await fetch(ENDPOINT + '/execute', {
                method: 'POST',
                headers: Object.assign({'Content-Type': 'application/json'},
                    (window.Utils && window.Utils.getCsrfToken()) ? {'X-CSRFToken': window.Utils.getCsrfToken()} : {}),
                body: JSON.stringify({
                    check_type:   checkType,
                    advisory_ids: ids,
                    instance_ids: selected.map(function(i) { return i.instance_id; }),
                }),
                credentials: 'same-origin',
            });

            if (resp.ok) {
                const data = await resp.json();
                currentBatchId = data.batch_id;
                showToast('Compliance check started on ' + data.execution_count + ' instance(s)', 'success');
                pollCompliance(data.batch_id);
            } else {
                const errData = await resp.json().catch(function() { return {detail: 'Launch failed'}; });
                showToast('Error: ' + (errData.detail || 'Unknown error'), 'danger');
                _resetLaunchBtn();
            }
        } catch (err) {
            showToast('Error: ' + err.message, 'danger');
            _resetLaunchBtn();
        }
    };

    function _resetLaunchBtn() {
        const btn = document.getElementById('launch-btn');
        if (!btn) return;
        btn.disabled = false;
        btn.textContent = '';
        append(btn, el('i', 'bi bi-play-circle me-2'));
        btn.appendChild(document.createTextNode('Run Compliance Check'));
    }

    // ── Polling ───────────────────────────────────────────────────────────────

    function pollCompliance(batchId) {
        if (compliancePolling && compliancePolling.stop) { compliancePolling.stop(); compliancePolling = null; }

        const polling = window.Utils.createPolling(async function() {
            const resp = await fetch(ENDPOINT + '/results/' + batchId, {credentials: 'same-origin'});
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            const data = await resp.json();

            displayComplianceResults(data);

            const inProgress = (data.status_counts.pending || 0) + (data.status_counts.running || 0);
            if (inProgress === 0) {
                const hasFailed = (data.status_counts.failed || 0) > 0;
                showComplianceStatus('Compliance check complete', hasFailed ? 'warning' : 'success');
                showToast('Compliance check completed', hasFailed ? 'warning' : 'success');
                _resetLaunchBtn();
                document.getElementById('download-section').style.display = 'block';
                return {continue: false};
            }
        }, {
            initialInterval: 3000, maxInterval: 15000, backoffMultiplier: 1.5, maxPolls: 120,
            onMaxPollsReached: function() {
                showComplianceStatus('Check timed out after 10 minutes', 'warning');
                _resetLaunchBtn();
            },
            onError: function() { showComplianceStatus('Lost connection to execution monitor', 'danger'); },
        });

        compliancePolling = polling;
        polling.start();
    }

    // ── Results display ───────────────────────────────────────────────────────

    function displayComplianceResults(data) {
        const statusDiv  = document.getElementById('compliance-status');
        const resultsDiv = document.getElementById('compliance-results');

        const total   = data.results.length;
        const done    = (data.status_counts.completed || 0) + (data.status_counts.failed || 0);
        const pct     = total > 0 ? Math.round((done / total) * 100) : 0;
        const allDone = done === total && total > 0;

        statusDiv.replaceChildren();
        const alertDiv    = el('div', 'alert ' + (allDone ? 'alert-success' : 'alert-info'));
        const progressLine = el('div');
        append(progressLine,
            el('i', 'bi bi-' + (allDone ? 'check-circle-fill' : 'hourglass-split') + ' me-2'),
            el('strong', null, 'Progress: '));
        progressLine.appendChild(document.createTextNode(done + ' / ' + total + ' instances completed (' + pct + '%)'));
        alertDiv.appendChild(progressLine);

        const sub = el('div', 'mt-1');
        sub.appendChild(allDone
            ? el('strong', null, '✓ All checks complete — ready to download.')
            : el('small', 'text-muted', 'Running compliance checks, please wait...'));
        alertDiv.appendChild(sub);
        statusDiv.appendChild(alertDiv);

        // Table
        const wrapper = el('div', 'table-responsive');
        const table   = el('table', 'table table-sm table-striped table-hover');
        const thead   = el('thead');
        const hrow    = el('tr');

        // CVE mode adds an extra "RHSA" column to show the fixing advisory
        const headers = ['Instance ID', 'Account', 'Region', 'Compliance', 'Applied', 'Missing', 'N/A', 'Details'];
        headers.forEach(function(h) {
            const th = el('th', null, h);
            if (['Applied', 'Missing', 'N/A'].indexOf(h) !== -1) th.className = 'text-center';
            hrow.appendChild(th);
        });
        thead.appendChild(hrow);
        table.appendChild(thead);

        const tbody = el('tbody');
        data.results.forEach(function(result) { tbody.appendChild(buildResultRow(result)); });
        table.appendChild(tbody);
        wrapper.appendChild(table);

        resultsDiv.replaceChildren();
        resultsDiv.appendChild(wrapper);
    }

    function buildResultRow(result) {
        const c  = result.compliance || {};
        const tr = el('tr');

        const tdId = el('td');
        tdId.appendChild(el('code', null, result.instance_id));
        tr.appendChild(tdId);
        tr.appendChild(el('td')).appendChild(el('small', null, result.account_id || '—'));
        tr.appendChild(el('td')).appendChild(el('small', null, result.region || '—'));

        const tdStatus = el('td');
        tdStatus.appendChild(makeComplianceBadge(result.status, c.compliance_status));
        tr.appendChild(tdStatus);

        ['applied', 'missing', 'na'].forEach(function(key) {
            const td = el('td', 'text-center');
            if (c[key] !== undefined) {
                const count = c[key];
                let cls = 'badge bg-secondary';
                if (key === 'applied') cls = 'badge bg-success';
                else if (key === 'missing') cls = count > 0 ? 'badge bg-danger' : 'badge bg-success';
                td.appendChild(el('span', cls, String(count)));
            } else {
                td.textContent = '—';
            }
            tr.appendChild(td);
        });

        const tdDetails = el('td');
        tdDetails.appendChild(buildDetailsCell(result.status, c));
        tr.appendChild(tdDetails);

        return tr;
    }

    function makeComplianceBadge(execStatus, complianceStatus) {
        if (execStatus === 'pending') return el('span', 'badge bg-secondary', 'Pending');
        if (execStatus === 'running') {
            const b = el('span', 'badge bg-primary');
            append(b, el('span', 'spinner-border spinner-border-sm me-1'));
            b.appendChild(document.createTextNode('Running'));
            return b;
        }
        if (execStatus === 'failed') return el('span', 'badge bg-danger', 'Failed');

        const cs = complianceStatus || 'UNKNOWN';
        if (cs === 'COMPLIANT')     return el('span', 'badge bg-success fs-6', '✓ COMPLIANT');
        if (cs === 'NON_COMPLIANT') return el('span', 'badge bg-danger fs-6',  '✗ NON-COMPLIANT');
        if (cs === 'ERROR')         return el('span', 'badge bg-warning text-dark', 'ERROR');
        return el('span', 'badge bg-secondary', 'UNKNOWN');
    }

    function buildDetailsCell(execStatus, c) {
        const frag = document.createDocumentFragment();
        if (execStatus === 'pending' || execStatus === 'running') {
            frag.appendChild(el('span', 'text-muted small', '—'));
            return frag;
        }

        if (c.missing_advisories && c.missing_advisories.length > 0) {
            const details  = document.createElement('details');
            const summary  = el('summary', 'text-danger small');
            summary.style.cursor = 'pointer';
            const count = c.missing_advisories.length;
            summary.textContent = count + ' missing advisor' + (count > 1 ? 'ies' : 'y');
            details.appendChild(summary);

            const list = el('div', 'mt-1 small');
            c.missing_advisories.forEach(function(adv) {
                const row = el('div');
                row.appendChild(el('code', null, adv));
                // CVE mode: show the fixing RHSA ID alongside the CVE
                const rhsa = c.rhsa_mappings && c.rhsa_mappings[adv];
                if (rhsa) {
                    row.appendChild(document.createTextNode(' → '));
                    row.appendChild(el('span', 'text-muted', rhsa));
                }
                list.appendChild(row);
            });
            details.appendChild(list);
            frag.appendChild(details);
        } else if (c.error) {
            frag.appendChild(el('span', 'text-danger small', c.error));
        } else if (c.hostname) {
            frag.appendChild(el('small', 'text-muted', c.hostname + (c.pkg_mgr ? ' · ' + c.pkg_mgr : '')));
        } else {
            frag.appendChild(el('span', 'text-muted small', '—'));
        }

        return frag;
    }

    function showComplianceStatus(message, type) {
        const statusDiv = document.getElementById('compliance-status');
        if (!statusDiv) return;
        const cls = type === 'success' ? 'alert-success' : type === 'warning' ? 'alert-warning' : 'alert-danger';
        statusDiv.replaceChildren();
        statusDiv.appendChild(el('div', 'alert ' + cls, message));
    }

    // ── Downloads ─────────────────────────────────────────────────────────────

    window.downloadResults = function(format) {
        if (!currentBatchId) return;
        window.location.href = ENDPOINT + '/download-results/' + currentBatchId + '?format=' + format;
    };

})();
