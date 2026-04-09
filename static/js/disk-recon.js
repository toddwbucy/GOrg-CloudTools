'use strict';

// ── State ──────────────────────────────────────────────────────────────────────
let _reportText   = '';
let _commandId    = '';
let _instanceId   = '';
let _accountId    = '';
let _region       = '';
let _environment  = '';
let _polling      = null;

const COM_REGIONS = [
    'us-east-1', 'us-east-2', 'us-west-1', 'us-west-2',
    'eu-west-1', 'eu-west-2', 'eu-central-1',
    'ap-southeast-1', 'ap-southeast-2', 'ap-northeast-1',
];
const GOV_REGIONS = ['us-gov-east-1', 'us-gov-west-1'];

// Terminal SSM states
const TERMINAL_STATES = new Set([
    'Success', 'Failed', 'Cancelled', 'TimedOut',
    'DeliveryTimedOut', 'ExecutionTimedOut', 'Undeliverable',
    'Terminated', 'InvalidPlatform', 'AccessDenied', 'Error',
]);

// ── Region dropdown ────────────────────────────────────────────────────────────
function onEnvChange() {
    const env     = document.getElementById('environment').value;
    const sel     = document.getElementById('region');
    const regions = env === 'gov' ? GOV_REGIONS : COM_REGIONS;

    sel.replaceChildren();
    regions.forEach(r => {
        const opt = document.createElement('option');
        opt.value       = r;
        opt.textContent = r;
        sel.appendChild(opt);
    });
}

// ── Main entry point ──────────────────────────────────────────────────────────
async function runDiskRecon() {
    _environment = document.getElementById('environment').value.trim();
    _accountId   = (document.getElementById('account-id').value || '').trim();
    _region      = (document.getElementById('region').value || '').trim();
    _instanceId  = (document.getElementById('instance-id').value || '').trim();
    const osType = document.querySelector('input[name="os-type"]:checked')?.value || 'linux';

    // Validate
    const errors = [];
    if (!/^\d{12}$/.test(_accountId))       errors.push('Account ID must be 12 digits');
    if (!_region)                             errors.push('Region is required');
    if (!_instanceId.startsWith('i-'))        errors.push('Instance ID must start with "i-"');
    if (errors.length) { hideResults(); showError(errors.join(' | ')); return; }

    hideError();
    hideResults();
    setProgress(true, 'Sending SSM command…');
    setInputsDisabled(true);

    // Cancel any in-flight poll
    if (_polling) { _polling.stop(); _polling = null; }

    try {
        const headers = window.Utils
            ? { 'Content-Type': 'application/json', ...window.Utils.buildCsrfHeaders() }
            : { 'Content-Type': 'application/json' };

        const resp = await fetch('/aws/disk-recon/run', {
            method:  'POST',
            headers,
            body: JSON.stringify({
                environment: _environment,
                account_id:  _accountId,
                region:      _region,
                instance_id: _instanceId,
                os_type:     osType,
            }),
        });

        const json = await resp.json();
        if (!resp.ok) { showError(json.error || `HTTP ${resp.status}`); setProgress(false); setInputsDisabled(false); return; }

        if (!json.command_id) {
            showError('Server did not return a command ID — check your AWS configuration.');
            setProgress(false);
            setInputsDisabled(false);
            return;
        }
        _commandId = json.command_id;
        setProgress(true, 'Command sent — waiting for SSM agent to pick it up…');
        startPolling();

    } catch (err) {
        showError('Network error: ' + err.message);
        setProgress(false);
        setInputsDisabled(false);
    }
}

// ── Polling ────────────────────────────────────────────────────────────────────
function startPolling() {
    if (!window.Utils || !window.Utils.createPolling) {
        // Fallback: serialized loop via setTimeout so a new doPoll is never
        // scheduled before the previous one resolves (unlike setInterval).
        let stopped = false;
        let timer   = null;
        const tick = async () => {
            if (stopped) return;
            const done = await doPoll();
            if (done || stopped) {
                _polling = { stop: () => {} };
                return;
            }
            timer = setTimeout(tick, 4000);
        };
        _polling = { stop: () => { stopped = true; clearTimeout(timer); } };
        timer = setTimeout(tick, 4000);
    } else {
        _polling = window.Utils.createPolling(async () => {
            const done = await doPoll();
            return done ? { continue: false } : { continue: true };
        }, {
            initialInterval:   4000,
            maxInterval:       10000,
            backoffMultiplier: 1.5,
            maxPolls:          60,
            onMaxPollsReached: () => {
                showError('Timed out waiting for SSM results. The command may still be running — check AWS console.');
                setProgress(false);
                setInputsDisabled(false);
            },
            onError: (err) => {
                showError('Polling error: ' + err.message);
                setProgress(false);
                setInputsDisabled(false);
            },
        });
        _polling.start();
    }
}

async function doPoll() {
    const params = new URLSearchParams({
        instance_id: _instanceId,
        account_id:  _accountId,
        region:      _region,
        environment: _environment,
    });

    try {
        const resp = await fetch(`/aws/disk-recon/poll/${encodeURIComponent(_commandId)}?${params}`);
        const json = await resp.json();

        if (!resp.ok) {
            showError(json.error || `Poll error HTTP ${resp.status}`);
            setProgress(false);
            setInputsDisabled(false);
            return true; // stop polling
        }

        const status = json.status || 'Unknown';
        setProgress(true, `SSM status: ${status}…`);

        if (!json.terminal) return false; // keep polling

        // Terminal — handle result
        setProgress(false);
        setInputsDisabled(false);

        if (status === 'Success') {
            _reportText = json.output || '(no output returned)';
            showReport(_reportText, 'success');
        } else {
            const errMsg = json.error || json.output || `Command ${status}`;
            _reportText = errMsg;
            showReport(errMsg, 'error');
            showError(`SSM command ended with status: ${status}. ${json.details || ''}`);
        }
        return true; // stop polling

    } catch (err) {
        showError('Poll network error: ' + err.message);
        setProgress(false);
        setInputsDisabled(false);
        return true;
    }
}

// ── Report display ─────────────────────────────────────────────────────────────
function showReport(text, level) {
    const section = document.getElementById('results-section');
    const pre     = document.getElementById('report-output');
    const badge   = document.getElementById('result-badge');

    pre.textContent = text; // textContent is XSS-safe

    // Status badge
    badge.className   = 'badge ms-2 ' + (level === 'success' ? 'bg-success' : 'bg-danger');
    badge.textContent = level === 'success' ? 'Complete' : 'Error';

    // Show action buttons only on success
    const showBtns = level === 'success';
    document.getElementById('copy-btn').style.display     = showBtns ? '' : 'none';
    document.getElementById('download-btn').style.display = showBtns ? '' : 'none';

    section.style.display = '';
    section.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

function hideResults() {
    document.getElementById('results-section').style.display = 'none';
}

// ── Copy to clipboard ─────────────────────────────────────────────────────────
async function copyReport() {
    if (!_reportText) return;
    const ticketReport = _buildTicketReport();
    try {
        await navigator.clipboard.writeText(ticketReport);
        const btn = document.getElementById('copy-btn');
        const original = btn.textContent;
        btn.textContent = '✓ Copied!';
        btn.disabled = true;
        setTimeout(() => { btn.textContent = original; btn.disabled = false; }, 2000);
    } catch (err) {
        showError('Clipboard copy failed: ' + err.message);
    }
}

function _buildTicketReport() {
    // Prepend context header so the ticket is self-contained
    const header = [
        '================================================================',
        ' DISK RECON — TICKET REPORT',
        '================================================================',
        ` Instance    : ${_instanceId}`,
        ` Account     : ${_accountId}`,
        ` Region      : ${_region}`,
        ` Environment : ${_environment.toUpperCase()}`,
        ` Generated   : ${new Date().toISOString().replace('T', ' ').slice(0, 19)} UTC`,
        '================================================================',
        '',
    ].join('\n');
    return header + _reportText;
}

// ── Download ──────────────────────────────────────────────────────────────────
function downloadReport() {
    if (!_reportText) return;
    const content  = _buildTicketReport();
    const ts       = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
    const filename = `disk-recon_${_instanceId}_${ts}.txt`;
    const blob     = new Blob([content], { type: 'text/plain; charset=utf-8' });
    const url      = URL.createObjectURL(blob);
    const a        = document.createElement('a');
    a.href         = url;
    a.download     = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
}

// ── UI helpers ─────────────────────────────────────────────────────────────────
function setProgress(visible, msg) {
    const div  = document.getElementById('run-progress');
    const span = document.getElementById('progress-text');
    const btn  = document.getElementById('run-btn');
    div.style.display  = visible ? '' : 'none';
    if (msg) span.textContent = msg;
    btn.disabled = visible;
}

function showError(msg) {
    const el = document.getElementById('run-error');
    el.textContent   = msg;
    el.style.display = '';
}

function hideError() {
    document.getElementById('run-error').style.display = 'none';
}

function setInputsDisabled(disabled) {
    ['environment', 'region', 'account-id', 'instance-id'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.disabled = disabled;
    });
    document.querySelectorAll('input[name="os-type"]').forEach(el => {
        el.disabled = disabled;
    });
}

// Expose handlers called from inline HTML attributes to the global scope.
window.onEnvChange    = onEnvChange;
window.runDiskRecon   = runDiskRecon;
window.copyReport     = copyReport;
window.downloadReport = downloadReport;
