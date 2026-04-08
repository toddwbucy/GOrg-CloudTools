'use strict';

// ── State ─────────────────────────────────────────────────────────────────────
let _reportMarkdown = '';
let _instanceId     = '';

const COM_REGIONS = [
    'us-east-1', 'us-east-2', 'us-west-1', 'us-west-2',
    'eu-west-1', 'eu-west-2', 'eu-central-1',
    'ap-southeast-1', 'ap-southeast-2', 'ap-northeast-1',
];
const GOV_REGIONS = ['us-gov-east-1', 'us-gov-west-1'];

// ── DOM helpers ───────────────────────────────────────────────────────────────
function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
}

function append(parent) {
    for (let i = 1; i < arguments.length; i++) {
        if (arguments[i] != null) parent.appendChild(arguments[i]);
    }
    return parent;
}

// ── Region dropdown ───────────────────────────────────────────────────────────
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

// ── Run scan ──────────────────────────────────────────────────────────────────
async function runScan() {
    const environment = document.getElementById('environment').value.trim();
    const accountId   = (document.getElementById('account-id').value || '').trim();
    const region      = (document.getElementById('region').value || '').trim();
    _instanceId       = (document.getElementById('instance-id').value || '').trim();

    // Validate
    const errors = [];
    if (!/^\d{12}$/.test(accountId))          errors.push('Account ID must be 12 digits');
    if (!region)                               errors.push('Region is required');
    if (!_instanceId.startsWith('i-') || _instanceId.length < 10)
                                               errors.push('Instance ID must start with "i-"');
    if (errors.length) { showError(errors.join(' | ')); return; }

    hideError();
    hideResults();
    setProgress(true);
    setInputsDisabled(true);

    try {
        const headers = { 'Content-Type': 'application/json' };
        if (window.Utils && window.Utils.buildCsrfHeaders) {
            Object.assign(headers, window.Utils.buildCsrfHeaders());
        }
        const resp = await fetch('/aws/decom-survey/scan', {
            method:  'POST',
            headers,
            body: JSON.stringify({ environment, account_id: accountId, region, instance_id: _instanceId }),
        });

        const data = await resp.json();

        if (!resp.ok) {
            showError(data.error || `HTTP ${resp.status}`);
            return;
        }

        if (data.error) {
            showError(data.error);
            return;
        }

        _reportMarkdown = data.markdown || '';
        renderSummary(data.summary || {});
        renderReport(_reportMarkdown);

    } catch (err) {
        showError('Network error: ' + err.message);
    } finally {
        setProgress(false);
        setInputsDisabled(false);
    }
}

// ── Summary cards ─────────────────────────────────────────────────────────────
function renderSummary(s) {
    if (s.error) {
        showError(s.error);
        return;
    }

    const status     = s.overall_status || 'UNKNOWN';
    const statusMap  = {
        NOT_READY: { cls: 'bg-danger',   icon: 'bi-x-circle-fill',      label: 'NOT READY' },
        CAUTION:   { cls: 'bg-warning text-dark', icon: 'bi-exclamation-triangle-fill', label: 'CAUTION' },
        READY:     { cls: 'bg-success',   icon: 'bi-check-circle-fill',  label: 'READY' },
    };
    const sm = statusMap[status] || { cls: 'bg-secondary', icon: 'bi-question-circle', label: status };

    const container = document.getElementById('summary-cards');
    container.replaceChildren();

    // Status card
    const statusCard = buildCard(sm.cls, sm.icon, 'Decom Status', sm.label, 'text-white');
    container.appendChild(statusCard);

    // Blockers card
    const blockersColor = s.hard_blockers > 0 ? 'bg-danger' : 'bg-success';
    container.appendChild(buildCard(
        blockersColor, 'bi-slash-circle', 'Hard Blockers',
        String(s.hard_blockers || 0), 'text-white'
    ));

    // Warnings card
    const warnColor = s.warnings > 0 ? 'bg-warning text-dark' : 'bg-success';
    container.appendChild(buildCard(
        warnColor, 'bi-exclamation-triangle', 'Warnings',
        String(s.warnings || 0), s.warnings > 0 ? '' : 'text-white'
    ));

    // Instance info card
    const stateColor = s.instance_state === 'running' ? 'bg-success' : 'bg-secondary';
    container.appendChild(buildCard(
        stateColor, 'bi-pc-display', 'Instance',
        (s.instance_name || _instanceId) + ' · ' + (s.instance_state || '?'), 'text-white',
        s.instance_type || ''
    ));

    // LB card
    const lbColor = s.lb_count > 0 ? 'bg-warning text-dark' : 'bg-secondary text-white';
    container.appendChild(buildCard(
        lbColor, 'bi-distribute-horizontal', 'Load Balancers',
        String(s.lb_count || 0), ''
    ));

    // SG rules card
    const sgColor = s.sg_rules_affected > 0 ? 'bg-warning text-dark' : 'bg-secondary text-white';
    container.appendChild(buildCard(
        sgColor, 'bi-shield-check', 'SG Rules w/ IP Refs',
        String(s.sg_rules_affected || 0), '',
        `of ${s.sgs_scanned || 0} SGs scanned`
    ));

    // ASG card
    const asgColor = s.asg_name ? 'bg-danger' : 'bg-secondary text-white';
    container.appendChild(buildCard(
        asgColor, 'bi-arrow-repeat', 'ASG',
        s.asg_name || 'None', s.asg_name ? 'text-white' : ''
    ));

    // Stats card
    container.appendChild(buildCard(
        'bg-secondary text-white', 'bi-bar-chart-fill', 'Resources',
        `${s.volume_count || 0} vols · ${s.eni_count || 0} ENIs · ${s.eip_count || 0} EIPs`,
        '',
        `${s.cw_alarm_count || 0} CW alarms · ${s.r53_count || 0} R53 health checks`
    ));

    document.getElementById('summary-section').style.display = '';
}

function buildCard(colorCls, iconCls, title, value, textCls, subtitle) {
    const col  = el('div', 'col-md-3 col-sm-6 mb-3');
    const card = el('div', 'card h-100 ' + colorCls);
    const body = el('div', 'card-body');

    const hdr = el('h6', 'card-subtitle mb-1 opacity-75');
    const ico = el('i', 'bi ' + iconCls + ' me-1');
    hdr.appendChild(ico);
    hdr.appendChild(document.createTextNode(title));

    const val = el('div', 'card-title fw-bold fs-5 mb-0 ' + (textCls || ''));
    val.textContent = value;

    append(body, hdr, val);

    if (subtitle) {
        const sub = el('small', 'opacity-75');
        sub.textContent = subtitle;
        body.appendChild(sub);
    }

    card.appendChild(body);
    col.appendChild(card);
    return col;
}

// ── Render report ─────────────────────────────────────────────────────────────
function renderReport(markdown) {
    const output = document.getElementById('report-output');
    output.textContent = markdown;

    // Badge
    const badge = document.getElementById('result-badge');
    if (markdown.includes('NOT READY')) {
        badge.className = 'badge bg-danger ms-2';
        badge.textContent = 'NOT READY';
    } else if (markdown.includes('CAUTION')) {
        badge.className = 'badge bg-warning text-dark ms-2';
        badge.textContent = 'CAUTION';
    } else if (markdown.includes('READY')) {
        badge.className = 'badge bg-success ms-2';
        badge.textContent = 'READY';
    } else {
        badge.className = 'badge bg-secondary ms-2';
        badge.textContent = 'Error';
    }

    document.getElementById('download-btn').style.display = '';
    document.getElementById('results-section').style.display = '';
}

// ── Download ──────────────────────────────────────────────────────────────────
function downloadReport() {
    if (!_reportMarkdown) return;
    const blob = new Blob([_reportMarkdown], { type: 'text/markdown' });
    const url  = URL.createObjectURL(blob);
    const a    = document.createElement('a');
    a.href     = url;
    a.download = `decom-survey-${_instanceId || 'report'}.md`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
}

// ── UI helpers ────────────────────────────────────────────────────────────────
function setProgress(on) {
    document.getElementById('scan-progress').style.display = on ? '' : 'none';
    document.getElementById('scan-btn').disabled = on;
}

function setInputsDisabled(disabled) {
    ['environment', 'region', 'account-id', 'instance-id'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.disabled = disabled;
    });
}

function showError(msg) {
    const div = document.getElementById('scan-error');
    div.textContent = msg;
    div.style.display = '';
}

function hideError() {
    document.getElementById('scan-error').style.display = 'none';
}

function hideResults() {
    document.getElementById('results-section').style.display  = 'none';
    document.getElementById('summary-section').style.display  = 'none';
    document.getElementById('report-output').textContent      = '';
    document.getElementById('result-badge').textContent       = '';
    document.getElementById('download-btn').style.display     = 'none';
}
